//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
	"strings"
	"syscall/js"
	"time"
)

const browserLabel = "This browser (localStorage)"

type prepared struct {
	key    encstore.NewKey
	secret string
	acked  bool
}

type encUI struct {
	store   *encstore.Store
	browser docstore.Adapter
	pending *components.Pending
	render  func()

	setup                    bool // the switch was turned on and the form is open
	wantPasskey, wantRecover bool
	sshPub                   string
	passkeyAck               bool
	prep                     *prepared
	pendingCred              string
	noPRF                    bool
	unlockRecovery           string
	notice                   string

	eraseTyping bool // the first question was answered: the word is asked
	eraseWord   string

	broken                error
	restoreRecover        string // the recovery key typed to restore with
	firstUse              *encstore.FirstUseError
	rekeyDone, rekeyTotal int
	checkRecovery         string // never kept past the check
	checkMsg              string // the answer to the last check
	checkOK               bool

	sharedFor       string
	docConfidential bool
	shared          []components.RecipientRow
	draft, confirm  string
	myAdded         *components.RecipientRow
	removedNotice   string
	peopleKey       *prepared // the recovery key being added from the People tab

	dir dirUI

	keyListDraft string // a key list pasted from another device, not yet used

	deviceUses map[string]encrypt.DeviceUse
	usesLoaded bool

	swap func()
}

func readEncConfig() (cfg encstore.Config, err error) {
	defer func() {
		if recover() != nil {
			cfg, err = encstore.Config{}, errors.New("the browser's storage could not be read")
		}
	}()
	v := js.Global().Get("localStorage").Call("getItem", encConfigKey)
	if v.IsNull() || v.IsUndefined() {
		return encstore.Config{}, nil
	}
	if err := json.Unmarshal([]byte(v.String()), &cfg); err != nil {
		return encstore.Config{}, err
	}
	return cfg, nil
}

func writeEncConfig(cfg encstore.Config) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New(T("enc.err.keyList", "The browser refused to keep the key list."))
		}
	}()
	ls := js.Global().Get("localStorage")
	if len(cfg.Owners) == 0 && cfg.Pending == nil {
		ls.Call("removeItem", encConfigKey)
		ls.Call("removeItem", encSealedKey)
		return nil
	}
	b, _ := json.Marshal(cfg)
	ls.Call("setItem", encConfigKey, string(b))
	ls.Call("setItem", encSealedKey, "1")
	return nil
}

func reloadEncConfig() (encstore.Config, error) {
	c, _ := readEncConfig()
	return c, nil
}

func encSealedHint() bool {
	defer func() { _ = recover() }()
	v := js.Global().Get("localStorage").Call("getItem", encSealedKey)
	return !v.IsNull() && !v.IsUndefined() && v.String() == "1"
}

func newEncStore(browser docstore.Adapter, cfg encstore.Config) (*encstore.Store, error) {
	st, err := encstore.New(browser, encTarget, cfg)
	if err != nil {
		return nil, err
	}
	st.Persist, st.Reload = writeEncConfig, reloadEncConfig
	if encTarget == "browser" {
		st.Capacity = 5 << 20 // a server has no such limit
	}
	return st, nil
}

func newEncOff(browser docstore.Adapter, render func(), why error) (*encUI, error) {
	st, err := newEncStore(browser, encstore.Config{})
	if err != nil {
		return nil, err
	}
	e := &encUI{store: st, browser: browser, render: render, notice: T("enc.err.keyListRead", "The saved key list could not be read: ") + why.Error()}
	e.hookStore()
	e.pending = components.NewPending(render)
	e.dir = newDirUI()
	return e, nil
}

func newEnc(browser docstore.Adapter, render func()) (*encUI, error) {
	cfg, rerr := readEncConfig()
	why := encstore.Inspect(context.Background(), browser, cfg, rerr)
	if why == nil && len(cfg.Owners) == 0 && cfg.Pending == nil && encSealedHint() {
		why = encstore.ErrConfigMissing // set once, and the configuration is gone: not "off"
	}
	if why != nil {
		cfg = encstore.Config{}
	}
	st, err := newEncStore(browser, cfg)
	if err != nil {
		return nil, err
	}
	e := &encUI{store: st, browser: browser, render: render, wantPasskey: client.Supported(), wantRecover: true, broken: why}
	e.hookStore()
	e.pending = components.NewPending(render)
	e.dir = newDirUI()
	return e, nil
}

func (e *encUI) target() saver.Target {
	if e.broken != nil {
		return saver.Target{Name: encTarget, Label: encLabel(T("enc.browserLabelBroken", "This browser (read-only: key configuration lost)"), "%s (read-only: key configuration lost)"), Adapter: encstore.ReadOnly(e.browser, e.broken), Capacity: encCapacity()}
	}
	if e.store.Enabled() {
		return saver.Target{Name: encTarget, Label: encLabel(T("enc.browserLabel", "This browser (encrypted)"), "%s (encrypted)"), Adapter: e.store.Adapter(), Capacity: encCapacity()}
	}
	return saver.Target{Name: encTarget, Label: encLabel(browserLabel, "%s"), Adapter: e.browser, Capacity: encCapacity()}
}

func (e *encUI) state() map[string]any {
	kinds := []string{}
	for _, o := range e.store.Owners() {
		kinds = append(kinds, o.Kind)
	}
	return map[string]any{"enabled": e.store.Enabled(), "unlocked": e.store.Unlocked(), "owners": kinds, "setup": e.setup,
		"interrupted": e.store.Interrupted(), "prepared": e.prep != nil, "pendingPasskey": e.pendingCred != "",
		"secretShown": e.prep != nil || e.peopleKey != nil, "stale": e.store.Stale(), "broken": e.brokenKind(),
		"dir": map[string]any{"url": e.dir.url, "offer": e.dir.offer, "host": e.dir.host(), "tokenSet": e.dir.token != "", "refused": e.dir.refused}}
}

func (e *encUI) brokenKind() string {
	switch {
	case errors.Is(e.broken, encstore.ErrConfigMissing):
		return "missing"
	case e.broken != nil:
		return "corrupt"
	}
	return ""
}

func (u *ui) encSwap() { u.saver.Saver.AddTarget(u.enc.target()) }

func btn(id, label string, on func(), disabled bool) xango.Node {
	attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button"), xango.A("id", id)}
	if disabled || on == nil {
		attrs = append(attrs, xango.A("disabled", "disabled"))
	} else {
		attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
	}
	return xango.El("button", attrs, xango.Text(label))
}

func textarea(id, label, value, placeholder string, rows string, on func(string)) xango.Node {
	return xango.El("textarea", []xango.Attr{xango.A("id", id), xango.A("rows", rows), xango.A("value", value), xango.A("spellcheck", "false"),
		xango.A("aria-label", label), xango.A("placeholder", placeholder), xango.A("style", "width:100%;box-sizing:border-box;font-family:monospace;font-size:12px"),
		xango.On("input", func(ev xango.Event) { on(xango.EventValue[string](ev)) })}, xango.Text(value))
}

func para(id string, text string, alert bool) xango.Node {
	attrs := []xango.Attr{xango.A("id", id), xango.A("style", "font-size:13px;margin:0.4em 0")}
	if alert {
		attrs = append(attrs, xango.A("role", "alert"), xango.A("class", "gj-doc-error"))
	}
	return xango.El("p", attrs, xango.Text(text))
}

func (u *ui) secretBox(prefix string, p *prepared, commitLabel string, commit, cancel func()) xango.Node {
	return xango.El("div", []xango.Attr{xango.A("id", prefix+"-box"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
		para(prefix+"-warning", T("enc.recoveryWarning", "This is your recovery key. It is shown only now. Keep it offline (print it or store it in a password manager). We cannot recover it: if you lose it and your passkey, what you made confidential is lost."), true),
		textarea(prefix+"-secret", T("enc.recoverySecret", "Recovery key (secret)"), p.secret, "", "2", func(string) {}),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
			btn(prefix+"-copy", T("enc.copy", "Copy"), func() { u.saver.Copy(p.secret) }, false),
			btn(prefix+"-download", T("enc.download", "Download"), func() { transfer.Download("recovery-key.txt", "text/plain", []byte(p.secret+"\n")) }, false),
			btn(prefix+"-print", T("enc.printSheet", "Print a sheet"), func() { printRecoverySheet(T("enc.recoveryLabel", "Recovery key"), p.secret) }, false)),
		para(prefix+"-notyet", T("enc.notYet", "Nothing has been changed yet: the key is added only when you confirm below."), false),
		xango.El("div", []xango.Attr{xango.A("id", prefix+"-ack")}, components.Checkbox(components.CheckboxProps{
			Label: T("enc.ackSaved", "I have saved this recovery key somewhere safe"), Checked: p.acked,
			OnChange: func(on bool) { p.acked = on; u.rerender() }})),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
			btn(prefix+"-commit", commitLabel, commit, !p.acked || u.enc.pending.Busy()),
			btn(prefix+"-cancel", T("enc.cancelKey", "Cancel: do not add it"), cancel, u.enc.pending.Busy())))
}

func (u *ui) encPanel() xango.Node {
	e := u.enc
	if e.broken != nil {
		return u.encBrokenPanel()
	}
	enabled, unlocked := e.store.Enabled(), e.store.Unlocked()
	var kids []xango.Node
	if e.store.Stale() {
		kids = append(kids, u.stalePanel()...)
	}
	kids = append(kids, xango.El("div", []xango.Attr{xango.A("id", "enc-switch-box")}, components.Switch(components.SwitchProps{
		Label: T("enc.switch", "Encrypt this store"), Checked: enabled || e.setup, Disabled: e.pending.Busy(),
		OnChange: func(on bool) {
			switch {
			case on && !enabled:
				e.setup = true
				u.rerender()
			case !on && enabled:
				u.saver.Asker.AskWith(T("enc.turnOff", "Turn off encryption"),
					T("enc.confirmOff", "Turn off encryption? Everything saved in this browser is decrypted and stored as plain text again, and the keys are forgotten here. Documents that are confidential must be made not confidential first."),
					u.encDisable)
				u.rerender()
			case !on:
				e.setup, e.prep = false, nil
				u.rerender()
			}
		}})))
	switch {
	case enabled:
		kids = append(kids, u.encStatus(unlocked)...)
	case e.setup:
		kids = append(kids, u.encSetup()...)
	default:
		kids = append(kids, para("enc-hint", T("enc.hint", "Off by default. When on, everything saved in this browser is encrypted to keys only you hold, so a copy of the browser's storage holds no document text. The catalog stays readable: the names, tags, sizes and times of ordinary documents are visible to anyone who can read this browser's storage. Documents you make confidential also hide their names and tags."), false))
	}
	if e.prep != nil && !enabled {
		kids = append(kids, u.secretBox("enc-recovery", e.prep, T("enc.commit", "Add this key and turn on encryption"), u.encCommit, func() { e.prep = nil; u.rerender() }))
	}
	if e.pendingCred != "" || e.noPRF {
		kids = append(kids, u.orphanPasskey()...)
	}
	if enabled {
		kids = append(kids, u.encErasePanel()...)
	}
	if err := e.pending.Err(); err != nil {
		kids = append(kids, components.ErrorView("enc-error", err))
	} else if e.notice != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("id", "enc-message"), xango.A("role", "status"), xango.A("style", "font-size:13px")}, xango.Text(e.notice)))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "enc-panel"), xango.A("style", "margin-bottom:1em")}, kids...)
}

func (u *ui) stalePanel() []xango.Node {
	return []xango.Node{
		para("enc-stale", T("enc.stale", "The keys of this store were changed in another tab or window since this page loaded. This tab is locked so it cannot overwrite that change: reload the page."), true),
		btn("enc-reload", T("enc.reload", "Reload the page"), func() { js.Global().Get("location").Call("reload") }, false)}
}

func (u *ui) watchEncConfig() {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		key := args[0].Get("key")
		if key.IsNull() || key.IsUndefined() || key.String() == encConfigKey {
			u.encConfigChanged()
		}
		return nil
	})
	js.Global().Call("addEventListener", "storage", cb) // kept for the life of the page
}

func (u *ui) encConfigChanged() {
	e := u.enc
	e.store.ConfigChangedElsewhere()
	if e.store.Stale() {
		u.lockNow()
		e.notice = ""
		e.render()
	}
}

func (u *ui) orphanPasskey() []xango.Node {
	e := u.enc
	if e.pendingCred == "" {
		return []xango.Node{para("enc-orphan", T("enc.orphanNoPRF", "A passkey was made on your authenticator but cannot encrypt (no PRF). Delete it in your browser's or authenticator's passkey settings."), true),
			btn("enc-orphan-forget", T("enc.orphanDismiss", "Dismiss"), e.forgetPasskey, false)}
	}
	return []xango.Node{
		para("enc-orphan", T("enc.orphan", "A passkey was made on this device but not used yet, so nothing was changed. You can try it again (no new passkey is made), or forget it here and delete it in your browser's or authenticator's passkey settings."), false),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px")},
			btn("enc-orphan-retry", T("enc.orphanRetry", "Try the passkey again"), func() {
				if e.store.Enabled() {
					u.rerender()
					return
				}
				u.encCommit()
			}, e.pending.Busy()),
			btn("enc-orphan-forget", T("enc.orphanForget", "Forget it"), e.forgetPasskey, e.pending.Busy()))}
}

func (u *ui) encBrokenPanel() xango.Node {
	e := u.enc
	title := T("enc.missingTitle", "Configuration missing: encrypted files found")
	if e.brokenKind() == "corrupt" {
		title = T("enc.corruptTitle", "The key configuration could not be read")
	}
	kids := []xango.Node{
		para("enc-broken", title, true),
		para("enc-broken-text", T("enc.brokenText", "The documents in this browser's store are encrypted, but the list of their keys is gone or damaged, so this page does not know the store's keys. The store is read-only: nothing new is saved here (saving plain text next to the ciphertext would be wrong). If you hold one of the store's keys, restore from the files: that key opens them and becomes the store's only key (add the others again afterwards; files whose lists were signed by another key than the one you restore with may not restore, and then only Erase is left). Otherwise erase the store."), false)}
	kids = append(kids, u.keyListImportNodes()...)
	if client.Supported() && client.LocalCredential(encSlot) != "" {
		kids = append(kids, btn("enc-restore-passkey", T("enc.restorePasskey", "Restore with my passkey"), func() {
			u.encRestore(func(ctx context.Context) (encstore.NewKey, error) {
				_, secret, err := client.DeriveLocal(ctx, encSlot, encrypt.PRFSalt)
				if err != nil {
					return encstore.NewKey{}, e.passkeyFailed(err)
				}
				defer clear(secret)
				return encstore.NewPasskeyKey(secret, T("enc.passkeyLabel", "My passkey"))
			})
		}, e.pending.Busy()))
	}
	kids = append(kids,
		textarea("enc-restore-recovery-input", T("enc.recoveryInput", "Recovery key"), e.restoreRecover, "AGE-SECRET-KEY-1…", "2", func(v string) { e.restoreRecover = v; e.render() }),
		btn("enc-restore-recovery", T("enc.restoreRecovery", "Restore with the recovery key"), func() {
			secret := strings.TrimSpace(e.restoreRecover)
			u.encRestore(func(context.Context) (encstore.NewKey, error) {
				return encstore.RecoveryKeyFromSecret(T("enc.recoveryLabel", "Recovery key"), secret)
			})
		}, e.pending.Busy() || strings.TrimSpace(e.restoreRecover) == ""),
		btn("enc-restore-ssh", T("enc.restoreSSH", "Restore with an SSH private key file…"), func() {
			transfer.PickFile("", func(_ string, data []byte, err error) {
				u.encRestore(func(context.Context) (encstore.NewKey, error) {
					defer clear(data)
					if err != nil {
						return encstore.NewKey{}, err
					}
					return encstore.SSHKeyFromPrivate(data)
				})
			})
		}, e.pending.Busy()))
	kids = append(kids, u.encErasePanel()...)
	if err := e.pending.Err(); err != nil {
		kids = append(kids, components.ErrorView("enc-error", err))
	} else if e.notice != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("id", "enc-message"), xango.A("role", "status"), xango.A("style", "font-size:13px")}, xango.Text(e.notice)))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "enc-panel"), xango.A("style", "margin-bottom:1em")}, kids...)
}

func (u *ui) encRestore(make func(ctx context.Context) (encstore.NewKey, error)) {
	e := u.enc
	e.run(func(ctx context.Context) error {
		k, err := make(ctx)
		if err != nil {
			return err
		}
		if err := e.store.Restore(ctx, k); err != nil {
			return err
		}
		e.broken, e.restoreRecover, e.sharedFor = nil, "", ""
		u.encSwap()
		e.notice = T("enc.restored", "Restored: this key opens the store and is now its only key. Add your other keys again in the People tab.")
		return u.afterUnlock(ctx)
	})
}

func (u *ui) encErasePanel() []xango.Node {
	e := u.enc
	word := T("enc.eraseWord", "ERASE")
	nodes := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("id", "enc-erase-box"), xango.A("style", "margin-top:10px;border-top:1px solid #d1d5db;padding-top:8px")},
			para("enc-erase-hint", T("enc.eraseHint", "Lost every key? Without a key nothing here can be read, and nothing can bring it back. Erasing deletes everything this browser store holds (documents, versions, folders, the catalog) and forgets its keys, so you can start again. It touches nothing saved anywhere else (memory, a folder on this computer, a server)."), false),
			btn("enc-erase", T("enc.erase", "Erase the encrypted store…"), func() {
				u.saver.Asker.AskWith(T("enc.erase", "Erase the encrypted store…"),
					T("enc.eraseConfirm", "Erase the encrypted store? Every document, version and folder saved in this browser's encrypted store is deleted for good. This cannot be undone, and nothing can read them afterwards, with or without a key. Other places you saved to are not touched."),
					func() { e.eraseTyping = true; u.rerender() })
				u.rerender()
			}, e.pending.Busy()))}
	if e.eraseTyping {
		typed := eraseWordMatches(e.eraseWord, word)
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "enc-erase-typing"), xango.A("style", "margin-top:6px")},
			xango.El("label", []xango.Attr{xango.A("style", "font-size:13px;display:block")}, xango.Text(T("enc.eraseType", "Type %s to confirm:", word))),
			xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("id", "enc-erase-word"), xango.A("value", e.eraseWord), xango.A("autocomplete", "off"),
				xango.A("aria-label", T("enc.eraseTypeLabel", "Word typed to confirm erasing")),
				xango.On("input", func(ev xango.Event) { e.eraseWord = xango.EventValue[string](ev); u.rerender() })}),
			xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
				btn("enc-erase-go", T("enc.eraseGo", "Erase everything"), u.encErase, !typed || e.pending.Busy()),
				btn("enc-erase-cancel", T("enc.cancel", "Cancel"), func() { e.eraseTyping, e.eraseWord = false, ""; u.rerender() }, false))))
	}
	return nodes
}

func (u *ui) rerender() { u.enc.render() }

func (u *ui) encSetup() []xango.Node {
	e := u.enc
	supported := client.Supported()
	passkeyOn := e.wantPasskey && supported
	locked := e.prep != nil // the secret is on screen: the choice is made
	var rows []xango.Node
	rows = append(rows, para("enc-intro", T("enc.intro", "Choose the keys that open this store. The private side stays with you; this page keeps only the public halves, and forgets the private ones when you lock it. Everything saved here is encrypted to these keys, so what you choose is what keeps your documents readable."), false))
	pk := components.Checkbox(components.CheckboxProps{Label: T("enc.optPasskey", "A passkey on this device (needs the PRF extension)"), Checked: passkeyOn, Disabled: !supported || locked,
		OnChange: func(on bool) { e.wantPasskey = on; u.rerender() }})
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("id", "enc-opt-passkey")}, pk))
	if !supported {
		rows = append(rows, para("enc-unsupported", T("enc.noPasskeys", "Passkeys need a secure page (https or localhost) and a browser that supports them."), false))
	}
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("id", "enc-opt-recovery")}, components.Checkbox(components.CheckboxProps{
		Label: T("enc.optRecovery", "A recovery key, shown once"), Checked: e.wantRecover, Disabled: locked, OnChange: func(on bool) { e.wantRecover = on; u.rerender() }})))
	rows = append(rows,
		xango.El("label", []xango.Attr{xango.A("style", "font-size:13px;display:block;margin-top:6px")}, xango.Text(T("enc.optSSH", "An SSH public key (ed25519), optional:"))),
		textarea("enc-ssh", T("enc.sshPublic", "SSH public key"), e.sshPub, "ssh-ed25519 AAAA… you@laptop", "2", func(v string) { e.sshPub = v; e.render() }))
	passkeyOnly := passkeyOn && !e.wantRecover && strings.TrimSpace(e.sshPub) == ""
	if passkeyOnly {
		rows = append(rows,
			para("enc-second", T("enc.passkeyOnlyWarn", "A passkey alone is risky. In this store EVERY document is encrypted to your keys, not only the confidential ones: if you lose the passkey (a new device, a reset, deleting it), everything saved here becomes unreadable for good. A recovery key (or an SSH key) is the way back. We strongly recommend choosing one above."), true),
			xango.El("div", []xango.Attr{xango.A("id", "enc-passkey-ack")}, components.Checkbox(components.CheckboxProps{
				Label: T("enc.passkeyOnlyAck", "I understand: if I lose the passkey, all documents in this store are lost. Use the passkey alone anyway."), Checked: e.passkeyAck,
				OnChange: func(on bool) { e.passkeyAck = on; u.rerender() }})))
	}
	if !locked {
		rows = append(rows, btn("enc-enable", T("enc.enable", "Turn on encryption"), u.encEnable, e.pending.Busy() || (passkeyOnly && !e.passkeyAck)))
	}
	return rows
}

func (u *ui) encStatus(unlocked bool) []xango.Node {
	e := u.enc
	var rows []xango.Node
	state := T("enc.locked", "Locked")
	if unlocked {
		state = T("enc.unlocked", "Unlocked")
	}
	head := []xango.Node{xango.El("strong", []xango.Attr{xango.A("id", "enc-status"), xango.A("role", "status"), xango.A("aria-live", "polite")}, xango.Text(state))}
	if unlocked && !e.store.Interrupted() {
		head = append(head, btn("enc-lock", T("enc.lock", "Lock"), u.encLock, false))
		if u.hasUnsavedEdits() {
			head = append(head, btn("enc-lock-discard", T("enc.lockDiscard", "Lock and discard unsaved changes"), u.askLockDiscard, e.pending.Busy()))
		}
	}
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:8px;align-items:center;margin:6px 0")}, head...))
	var kinds []string
	for _, o := range e.store.Owners() {
		kinds = append(kinds, o.Label+" ("+o.Kind+")")
	}
	rows = append(rows, para("enc-owners", T("enc.keys", "Keys")+": "+strings.Join(kinds, ", "), false))
	rows = append(rows, e.rekeyProgress()...)
	if len(e.store.Owners()) > 0 {
		rows = append(rows, para("enc-list-fingerprint", T("enc.listFingerprint", "Key list fingerprint: %s", e.store.ListFingerprint()), false))
	}
	rows = append(rows, u.recoveryAdvice()...)
	if p := e.store.PendingChange(); p != nil {
		rows = append(rows, u.interruptedPanel(p)...)
	}
	if unlocked {
		rows = append(rows, para("enc-note", T("enc.unlockedNote", "Lock forgets the keys and closes the document that is open: it is cleared from the editor (with unsaved changes you are asked to save first), the names of confidential documents show their placeholder again, and nothing opens until a key unlocks the store again. What is saved stays saved."), false))
	} else {
		rows = append(rows, para("enc-lockedNote", T("enc.lockedNote", "Unlock it with one of its keys to open or save documents. Confidential documents show a placeholder until then."), false))
		if e.store.HasKind(encstore.KindPasskey) && client.Supported() {
			rows = append(rows, btn("enc-unlock-passkey", T("enc.unlockPasskey", "Unlock with my passkey"), u.encUnlockPasskey, e.pending.Busy()))
		}
		if e.store.HasKind(encstore.KindRecovery) {
			rows = append(rows,
				textarea("enc-recovery-input", T("enc.recoveryInput", "Recovery key"), e.unlockRecovery, "AGE-SECRET-KEY-1…", "2", func(v string) { e.unlockRecovery = v; e.render() }),
				btn("enc-unlock-recovery", T("enc.unlockRecovery", "Unlock with the recovery key"), u.encUnlockRecovery, e.pending.Busy() || strings.TrimSpace(e.unlockRecovery) == ""))
		}
		if e.store.HasKind(encstore.KindSSH) {
			rows = append(rows, btn("enc-unlock-ssh", T("enc.unlockSSH", "Unlock with an SSH private key file…"), u.encUnlockSSH, e.pending.Busy()))
			if fu := e.firstUse; fu != nil {
				rows = append(rows, xango.El("div", []xango.Attr{xango.A("id", "enc-first-use"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
					para("enc-first-use-text", T("enc.firstUse", "This store's key list is new to this browser, and an SSH key has nothing older to check it against. Its fingerprint is below. Compare it with the one shown where you made the store (or on another device) before you go on: if it differs, someone changed the list."), true),
					xango.El("code", []xango.Attr{xango.A("id", "enc-first-use-fingerprint")}, xango.Text(fu.Fingerprint)),
					xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
						btn("enc-first-use-ok", T("enc.firstUseOK", "It matches: continue"), func() { e.store.TrustList(fu.Digest); e.firstUse = nil; u.encUnlockSSH() }, e.pending.Busy()),
						btn("enc-first-use-cancel", T("enc.firstUseCancel", "Cancel"), func() { e.firstUse = nil; u.rerender() }, false))))
			}
		}
	}
	rows = append(rows, para("enc-trust", T("enc.trustNote", "The list of this store's keys is kept on this device next to the store. The keys sign it, so a list that someone changed is refused when you unlock (an SSH-only store checks nothing until its key has unlocked it once). Someone who can write this site's storage can still delete the list or put back an older one: do not rely on this on a computer you do not control."), false))
	return rows
}

func (u *ui) interruptedPanel(p *encstore.Pending) []xango.Node {
	e := u.enc
	list := T("enc.interruptedOff", "turning encryption off")
	if len(p.To) > 0 {
		list = T("enc.interruptedKeys", "making these the store's keys: %s", ownerList(p.To))
	}
	return []xango.Node{
		xango.El("div", []xango.Attr{xango.A("id", "enc-interrupted"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
			para("enc-interrupted-text", T("enc.interrupted", "An earlier change to this store's keys (%s, %s) did not finish. The store is read-only until it is finished or undone. Finishing completes it; undoing puts the store back as it was before. Re-encrypting needs the store unlocked (sealing files that are still plain text does not).", p.Op, list), true),
			para("enc-interrupted-warn", T("enc.interruptedWarn", "If you did not start this change, do not finish it: someone who can write this site's storage may have made it. Check that these are your keys, and undo it otherwise."), false),
			xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px")},
				btn("enc-finish", T("enc.finish", "Finish re-encrypting"), func() { u.askRecover(true, p) }, e.pending.Busy()),
				btn("enc-undo", T("enc.undo", "Undo the change"), func() { u.askRecover(false, p) }, e.pending.Busy())))}
}

func (u *ui) askRecover(finish bool, p *encstore.Pending) {
	e := u.enc
	if finish {
		keys := T("enc.confirmFinishOff", "turn encryption off")
		if len(p.To) > 0 {
			keys = T("enc.confirmFinishKeys", "make these the only keys of this store: %s", ownerList(p.To))
		}
		u.saver.Asker.AskWith(T("enc.finish", "Finish re-encrypting"),
			T("enc.confirmFinish", "Finish the unfinished change? It will %s. Everything saved is encrypted to exactly these keys, so a key you do not know gets all of it. If these are not your keys, cancel and undo the change.", keys),
			func() { u.encRecover(true) })
	} else {
		keys := ownerList(e.store.Owners())
		if keys == "" {
			keys = T("enc.confirmUndoNone", "no keys (encryption off)")
		}
		u.saver.Asker.AskWith(T("enc.undo", "Undo the change"),
			T("enc.confirmUndo", "Undo the unfinished change? The store goes back to these keys: %s. If they are not your keys, do not use this store here.", keys),
			func() { u.encRecover(false) })
	}
	u.rerender()
}

func (u *ui) recoveryAdvice() []xango.Node {
	e := u.enc
	rows := []xango.Node{para("enc-limit", T("enc.limitPlain", "If you lose every key of this store, nobody can open its documents, including us. There is no way back."), false)}
	if len(e.store.Owners()) == 1 {
		rows = append(rows, para("enc-one-key", T("enc.oneKey", "This store has only one key. If you lose it, its documents are lost for good. Add a second key (a recovery key kept offline, or an SSH key) now."), true))
	}
	if e.store.HasKind(encstore.KindRecovery) {
		rows = append(rows,
			textarea("enc-check-input", T("enc.checkRecoveryInput", "Check your recovery key"), e.checkRecovery, "AGE-SECRET-KEY-1…", "2", func(v string) { e.checkRecovery = v; e.checkMsg = ""; e.render() }),
			btn("enc-check-recovery", T("enc.checkRecovery", "Check that this is my recovery key"), u.encCheckRecovery, strings.TrimSpace(e.checkRecovery) == ""))
		if due, never := recoveryCheckDue(); due {
			text := T("enc.checkDue", "You last checked your recovery key more than 90 days ago. Check that you still have it.")
			if never {
				text = T("enc.checkNever", "You have not checked your recovery key in this browser. Check that you still have it.")
			}
			rows = append(rows, para("enc-check-due", text, false),
				btn("enc-check-later", T("enc.checkLater", "Remind me in a week"), func() {
					lsSet(recoverySnoozeKey, time.Now().UTC().Add(encstore.SnoozeFor).Format(time.RFC3339))
					u.rerender()
				}, false))
		}
		if e.checkMsg != "" {
			rows = append(rows, xango.El("p", []xango.Attr{xango.A("id", "enc-check-result"), xango.A("role", "status"), xango.A("style", checkStyle(e.checkOK))}, xango.Text(e.checkMsg)))
		}
	}
	return rows
}

func checkStyle(ok bool) string {
	if ok {
		return "margin:4px 0"
	}
	return "margin:4px 0;color:var(--gj-danger)"
}

func (u *ui) encCheckRecovery() {
	e := u.enc
	err := e.store.CheckRecoveryKey(e.checkRecovery)
	e.checkRecovery = ""
	switch {
	case err == nil:
		e.checkOK = true
		e.checkMsg = T("enc.checkOK", "Yes: this is a recovery key of this store. It would unlock it.")
		lsSet(recoveryCheckedKey, time.Now().UTC().Format(time.RFC3339))
	case errors.Is(err, encstore.ErrNotAKey):
		e.checkOK = false
		e.checkMsg = T("enc.checkNo", "No: this is a key, but not one of this store's recovery keys.")
	default:
		e.checkOK = false
		e.checkMsg = T("enc.checkBad", "That is not a recovery key.")
	}
	u.rerender()
}

func recoveryCheckDue() (due, never bool) {
	at := func(key string) time.Time {
		t, _ := time.Parse(time.RFC3339, lsGet(key))
		return t
	}
	last := at(recoveryCheckedKey)
	return encstore.CheckDue(time.Now(), last, at(recoverySnoozeKey), encstore.ReminderEvery), last.IsZero()
}
