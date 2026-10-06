//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// The "Encrypt this store" switch of the Save tab and the key controls of the People tab: the
// browser target (localStorage) wrapped in encrypt.Adapter by the encstore package, with the
// owner's keys set up here, in the page. A static page has no server for a passkey to sign in
// to, so the passkey is a local one (client.CreateLocal / DeriveLocal): the browser's
// authenticator derives the encryption key, nothing is sent anywhere. Encryption is OFF unless
// the person turns it on; the keys' public halves are kept on this device (encConfigKey), the
// private side is in memory only, while unlocked.

const (
	encSlot      = "showcase"            // where client.CreateLocal keeps the passkey's credential ID
	encConfigKey = "showcase-enc:config" // the owner keys' public halves; not under the store's own prefix
	browserLabel = "This browser (localStorage)"
)

// encUI is the encryption state of the page.
type encUI struct {
	store   *encstore.Store
	browser *docstore.BrowserAdapter
	pending *components.Pending
	render  func()

	setup                    bool // the switch was turned on and the form is open
	wantPasskey, wantRecover bool
	sshPub                   string
	recoverySecret           string // shown once, at setup
	peopleSecret             string // shown once, from the People tab
	unlockRecovery           string
	notice                   string

	// the People tab: the current document's keys
	sharedFor       string
	docConfidential bool
	shared          []components.RecipientRow
	draft, confirm  string
	myAdded         *components.RecipientRow
	removedNotice   string
}

func readEncConfig() (cfg encstore.Config) {
	defer func() { _ = recover() }()
	v := js.Global().Get("localStorage").Call("getItem", encConfigKey)
	if v.IsNull() || v.IsUndefined() {
		return encstore.Config{}
	}
	if json.Unmarshal([]byte(v.String()), &cfg) != nil {
		return encstore.Config{}
	}
	return cfg
}

func writeEncConfig(cfg encstore.Config) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("the browser refused to keep the key list")
		}
	}()
	ls := js.Global().Get("localStorage")
	if len(cfg.Owners) == 0 {
		ls.Call("removeItem", encConfigKey)
		return nil
	}
	b, _ := json.Marshal(cfg)
	ls.Call("setItem", encConfigKey, string(b))
	return nil
}

// newEncOff is the page with encryption off after the key list could not be read: why is the
// notice the Save tab shows.
func newEncOff(browser *docstore.BrowserAdapter, render func(), why error) (*encUI, error) {
	st, err := encstore.New(browser, "browser", encstore.Config{})
	if err != nil {
		return nil, err
	}
	st.Persist = writeEncConfig
	e := &encUI{store: st, browser: browser, render: render, notice: "The saved key list could not be read: " + why.Error()}
	e.pending = components.NewPending(render)
	return e, nil
}

func newEnc(browser *docstore.BrowserAdapter, render func()) (*encUI, error) {
	st, err := encstore.New(browser, "browser", readEncConfig())
	if err != nil {
		return nil, err
	}
	st.Persist = writeEncConfig
	e := &encUI{store: st, browser: browser, render: render, wantPasskey: client.Supported(), wantRecover: true}
	e.pending = components.NewPending(render)
	return e, nil
}

// target is the browser target as it stands: the encrypted adapter when encryption is on.
func (e *encUI) target() saver.Target {
	if e.store.Enabled() {
		return saver.Target{Name: "browser", Label: T("enc.browserLabel", "This browser (encrypted)"), Adapter: e.store.Adapter(), Capacity: 5 << 20}
	}
	return saver.Target{Name: "browser", Label: browserLabel, Adapter: e.browser, Capacity: 5 << 20}
}

// state is what #state says about it, for the e2e test.
func (e *encUI) state() map[string]any {
	kinds := []string{}
	for _, o := range e.store.Owners() {
		kinds = append(kinds, o.Kind)
	}
	return map[string]any{"enabled": e.store.Enabled(), "unlocked": e.store.Unlocked(), "owners": kinds, "setup": e.setup,
		"secretShown": e.recoverySecret != "" || e.peopleSecret != ""}
}

func (u *ui) encSwap() { u.saver.Saver.AddTarget(u.enc.target()) }

// afterUnlock shows the real names of the confidential documents and folders now that they can
// be read, on the target in use.
func (u *ui) afterUnlock(ctx context.Context) error {
	if t, _ := u.saver.Where(); t != "browser" {
		return nil
	}
	list, trashed, folders, err := u.saver.Saver.Overview(ctx, "browser")
	if err != nil {
		return err
	}
	for _, en := range append(list, trashed...) {
		if en.Protection == saver.ProtectionE2EE {
			_ = u.saver.Reveal(ctx, en.ID) // best effort: one that no key opens stays under its placeholder
		}
	}
	for _, f := range folders {
		if f.Protection == saver.ProtectionE2EE {
			_ = u.saver.RevealFolder(ctx, f.ID)
		}
	}
	return u.saver.Refresh(ctx)
}

func (u *ui) encLock() {
	e := u.enc
	e.store.Lock()
	e.recoverySecret, e.peopleSecret, e.unlockRecovery, e.sharedFor, e.shared, e.myAdded = "", "", "", "", nil, nil
	u.saver.Forget()
	e.pending.Run(u.saver.Refresh)
}

func deriveSecret(ctx context.Context) ([]byte, error) {
	_, secret, err := client.DeriveLocal(ctx, encSlot, encrypt.PRFSalt)
	if errors.Is(err, client.ErrCancelled) {
		return nil, errCancelled
	}
	return secret, err
}

var errCancelled = errors.New("cancelled")

func (u *ui) encUnlockPasskey() {
	e := u.enc
	e.notice = ""
	e.pending.Run(func(ctx context.Context) error {
		secret, err := deriveSecret(ctx)
		if errors.Is(err, errCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
		err = e.store.UnlockPasskey(secret)
		clear(secret) // the secret is the root of the key: do not keep it around
		if err != nil {
			return err
		}
		return u.afterUnlock(ctx)
	})
}

func (u *ui) encUnlockRecovery() {
	e := u.enc
	key := strings.TrimSpace(e.unlockRecovery)
	e.pending.Run(func(ctx context.Context) error {
		if err := e.store.UnlockRecovery(key); err != nil {
			return err
		}
		e.unlockRecovery = ""
		return u.afterUnlock(ctx)
	})
}

func (u *ui) encUnlockSSH() {
	e := u.enc
	transfer.PickFile("", func(_ string, data []byte, err error) {
		e.pending.Run(func(ctx context.Context) error {
			if err != nil {
				return err
			}
			if err := e.store.UnlockSSH(data); err != nil {
				return err
			}
			clear(data)
			return u.afterUnlock(ctx)
		})
	})
}

// encEnable makes the keys that were chosen and turns encryption on.
func (u *ui) encEnable() {
	e := u.enc
	e.notice = ""
	want := struct {
		passkey, recovery bool
		ssh               string
	}{e.wantPasskey, e.wantRecover, strings.TrimSpace(e.sshPub)}
	e.pending.Run(func(ctx context.Context) error {
		var keys []encstore.NewKey
		if want.passkey {
			if _, err := client.CreateLocal(ctx, encSlot, "Gojiberry showcase", "showcase owner"); err != nil {
				if errors.Is(err, client.ErrCancelled) {
					return nil
				}
				return err
			}
			secret, err := deriveSecret(ctx)
			if errors.Is(err, errCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			k, err := encstore.NewPasskeyKey(secret, T("enc.passkeyLabel", "My passkey"))
			clear(secret)
			if err != nil {
				return err
			}
			keys = append(keys, k)
		}
		secret := ""
		if want.recovery {
			k, s, err := encstore.NewRecoveryKey(T("enc.recoveryLabel", "Recovery key"))
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				k.Unlock = nil // the passkey unlocks; the recovery key is for the day it is lost
			}
			keys, secret = append(keys, k), s
		}
		if want.ssh != "" {
			k, err := encstore.NewSSHKey(want.ssh)
			if err != nil {
				return err
			}
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			return errors.New(T("enc.chooseKey", "Choose at least one key: a passkey, a recovery key or an SSH public key."))
		}
		if err := e.store.Enable(ctx, keys...); err != nil {
			return err
		}
		u.encSwap()
		e.setup, e.recoverySecret, e.sshPub = false, secret, ""
		e.sharedFor = ""
		return u.saver.Refresh(ctx)
	})
}

// encDisable turns encryption off: the store must be unlocked and have nothing confidential.
func (u *ui) encDisable() {
	e := u.enc
	e.pending.Run(func(ctx context.Context) error {
		if !e.store.Unlocked() {
			return errors.New(T("enc.unlockFirst", "Unlock the store first: turning encryption off decrypts what is saved."))
		}
		list, trashed, folders, err := u.saver.Saver.Overview(ctx, "browser")
		if err != nil {
			return err
		}
		for _, en := range append(list, trashed...) {
			if en.Protection == saver.ProtectionE2EE {
				return fmt.Errorf("%s", T("enc.confidentialLeft", "Make the confidential documents and folders not confidential first: turning encryption off would leave their names and files unreadable."))
			}
		}
		for _, f := range folders {
			if f.Protection == saver.ProtectionE2EE {
				return fmt.Errorf("%s", T("enc.confidentialLeft", "Make the confidential documents and folders not confidential first: turning encryption off would leave their names and files unreadable."))
			}
		}
		if err := e.store.Disable(ctx); err != nil {
			return err
		}
		client.ForgetLocal(encSlot)
		u.encSwap()
		u.saver.Forget()
		e.recoverySecret, e.peopleSecret, e.sharedFor, e.shared, e.myAdded = "", "", "", nil, nil
		return u.saver.Refresh(ctx)
	})
}

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

// secretBox shows a recovery key once: the secret, Copy, Download and "I have saved it", which
// drops it from the page.
func (u *ui) secretBox(prefix, secret string, done func()) xango.Node {
	return xango.El("div", []xango.Attr{xango.A("id", prefix+"-box"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
		para(prefix+"-warning", T("enc.recoveryWarning", "This is your recovery key. It is shown only now. Keep it offline (print it or store it in a password manager). We cannot recover it: if you lose it and your passkey, what you made confidential is lost."), true),
		textarea(prefix+"-secret", T("enc.recoverySecret", "Recovery key (secret)"), secret, "", "2", func(string) {}),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
			btn(prefix+"-copy", T("enc.copy", "Copy"), func() { u.saver.Copy(secret) }, false),
			btn(prefix+"-download", T("enc.download", "Download"), func() { transfer.Download("recovery-key.txt", "text/plain", []byte(secret+"\n")) }, false),
			btn(prefix+"-done", T("enc.saved", "I have saved it"), done, false)))
}

// encPanel is the "Encrypt this store" control above the save form.
func (u *ui) encPanel() xango.Node {
	e := u.enc
	enabled, unlocked := e.store.Enabled(), e.store.Unlocked()
	var kids []xango.Node
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
				e.setup = false
				u.rerender()
			}
		}})))
	switch {
	case enabled:
		kids = append(kids, u.encStatus(unlocked)...)
	case e.setup:
		kids = append(kids, u.encSetup()...)
	default:
		kids = append(kids, para("enc-hint", T("enc.hint", "Off by default. When on, everything saved in this browser is encrypted to keys only you hold, and documents can be made confidential: their names and tags are hidden too."), false))
	}
	if e.recoverySecret != "" {
		kids = append(kids, u.secretBox("enc-recovery", e.recoverySecret, func() { e.recoverySecret = ""; u.rerender() }))
	}
	if err := e.pending.Err(); err != nil {
		kids = append(kids, para("enc-error", err.Error(), true))
	} else if e.notice != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("id", "enc-message"), xango.A("role", "status")}, xango.Text(e.notice)))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "enc-panel"), xango.A("style", "margin-bottom:1em")}, kids...)
}

func (u *ui) rerender() { u.enc.render() }

// encPeopleOrNone is encPeople, or nothing while encryption is off.
func (u *ui) encPeopleOrNone() xango.Node {
	if !u.enc.store.Enabled() {
		return xango.Frag()
	}
	return u.encPeople()
}

// encSetup is the form that chooses the owner's keys.
func (u *ui) encSetup() []xango.Node {
	e := u.enc
	supported := client.Supported()
	passkeyOn := e.wantPasskey && supported
	var rows []xango.Node
	rows = append(rows, para("enc-intro", T("enc.intro", "Choose the keys that open this store. The private side stays with you; this page keeps only the public halves, and forgets the private ones when you lock it."), false))
	pk := components.Checkbox(components.CheckboxProps{Label: T("enc.optPasskey", "A passkey on this device (needs the PRF extension)"), Checked: passkeyOn, Disabled: !supported,
		OnChange: func(on bool) { e.wantPasskey = on; u.rerender() }})
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("id", "enc-opt-passkey")}, pk))
	if !supported {
		rows = append(rows, para("enc-unsupported", T("enc.noPasskeys", "Passkeys need a secure page (https or localhost) and a browser that supports them."), false))
	}
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("id", "enc-opt-recovery")}, components.Checkbox(components.CheckboxProps{
		Label: T("enc.optRecovery", "A recovery key, shown once"), Checked: e.wantRecover, OnChange: func(on bool) { e.wantRecover = on; u.rerender() }})))
	rows = append(rows,
		xango.El("label", []xango.Attr{xango.A("style", "font-size:13px;display:block;margin-top:6px")}, xango.Text(T("enc.optSSH", "An SSH public key (ed25519), optional:"))),
		textarea("enc-ssh", T("enc.sshPublic", "SSH public key"), e.sshPub, "ssh-ed25519 AAAA… you@laptop", "2", func(v string) { e.sshPub = v; e.render() }))
	if passkeyOn && !e.wantRecover && strings.TrimSpace(e.sshPub) == "" {
		rows = append(rows, para("enc-second", encrypt.ErrNeedSecondRecipient.Error(), true))
	}
	rows = append(rows, btn("enc-enable", T("enc.enable", "Turn on encryption"), u.encEnable, e.pending.Busy()))
	return rows
}

// encStatus is the state of an encrypted store: locked or not, and how to change that.
func (u *ui) encStatus(unlocked bool) []xango.Node {
	e := u.enc
	var rows []xango.Node
	state := T("enc.locked", "Locked")
	if unlocked {
		state = T("enc.unlocked", "Unlocked")
	}
	head := []xango.Node{xango.El("strong", []xango.Attr{xango.A("id", "enc-status")}, xango.Text(state))}
	if unlocked {
		head = append(head, btn("enc-lock", T("enc.lock", "Lock"), u.encLock, false))
	}
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:8px;align-items:center;margin:6px 0")}, head...))
	var kinds []string
	for _, o := range e.store.Owners() {
		kinds = append(kinds, o.Label+" ("+o.Kind+")")
	}
	rows = append(rows, para("enc-owners", T("enc.keys", "Keys")+": "+strings.Join(kinds, ", "), false))
	if unlocked {
		rows = append(rows, para("enc-note", T("enc.unlockedNote", "Lock it to forget the keys: what is saved stays saved, and nothing opens until a key unlocks it again."), false))
		return rows
	}
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
	}
	return rows
}

// --- the People tab ---

func recipientRows(rs []encrypt.Recipient) []components.RecipientRow {
	var out []components.RecipientRow
	for _, r := range rs {
		out = append(out, components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint})
	}
	return out
}

func parseRecipient(text string) (encrypt.Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return encrypt.ParseAgeRecipient(text)
	}
	return encrypt.ParseSSHRecipient(text)
}

// refreshShared reads the current document's list of keys (and whether it is confidential) in the
// background when something it depends on changed.
func (u *ui) refreshShared() {
	e := u.enc
	id := u.saver.ID()
	key := fmt.Sprintf("%s|%v|%d", id, e.store.Unlocked(), len(e.store.Owners()))
	if key == e.sharedFor {
		return
	}
	e.sharedFor = key
	go func() {
		ctx := context.Background()
		conf, rows := false, []components.RecipientRow(nil)
		if id != "" {
			if en, err := u.saver.Saver.Get(ctx, "browser", id); err == nil {
				conf = en.Protection == saver.ProtectionE2EE
			}
			if sh, err := e.store.Sharing(); err == nil && conf {
				own := map[string]bool{}
				for _, o := range e.store.Recipients() {
					own[o.Fingerprint] = true
				}
				if list, err := sh.List(ctx, id); err == nil {
					for _, s := range list {
						if !own[s.Fingerprint] {
							rows = append(rows, components.RecipientRow{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint})
						}
					}
				}
			}
		}
		e.docConfidential, e.shared = conf, rows
		e.render()
	}()
}

// encPeople is the keys control of the People tab: the store's owner keys, with "Add my passkey"
// and "Create a recovery key" (the same host code as examples/people-keys-demo, over the
// encrypted store), and, for a confidential document, who it is shared with.
func (u *ui) encPeople() xango.Node {
	e := u.enc
	u.refreshShared()
	unlocked := e.store.Unlocked()
	docID := u.saver.ID()
	props := components.RecipientsProps{
		Owner: recipientRows(e.store.Recipients()), Shared: e.shared,
		Pending: e.pending, Ask: u.saver.Asker.Ask,
		Confidential: true, SecondKeyMessage: encrypt.ErrNeedSecondRecipient.Error(),
		PasskeySupported: client.Supported(),
		Copy:             func(text string) { u.saver.Copy(text) },
		Download:         func(name, text string) { transfer.Download(name, "text/plain", []byte(text)) },
	}
	var note string
	switch {
	case !unlocked:
		note = T("enc.peopleLocked", "Unlock the store (Save tab) to add keys or share documents.")
	default:
		props.AddMyPasskey, props.MyPasskeyAdded = e.addMyPasskey, e.myAdded
		props.CreateRecoveryKey = e.createRecoveryKey
		props.OnRecoveryKey = func(s string) { e.peopleSecret = s; e.render() }
		props.RecoverySecret = e.peopleSecret
		props.OnRecoveryDone = func() { e.peopleSecret = ""; e.render() }
		switch {
		case docID == "":
			note = T("enc.peopleNoDoc", "Save the document to share it with other keys.")
		case !e.docConfidential:
			note = T("enc.peopleNotConfidential", "This document is encrypted to your keys. Make it confidential (Save tab) to share it with other keys.")
		default:
			props.Draft, props.Confirm = e.draft, e.confirm
			if strings.TrimSpace(e.draft) != "" {
				if r, err := parseRecipient(e.draft); err == nil {
					props.Parsed = &components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint}
				} else {
					props.ParseError = err.Error()
				}
			}
			props.OnDraft = func(k, c string) { e.draft, e.confirm = k, c; e.render() }
			props.Add = func(ctx context.Context, key, confirm string) error {
				r, err := parseRecipient(key)
				if err != nil {
					return err
				}
				sh, err := e.store.Sharing()
				if err != nil {
					return err
				}
				if err := sh.Add(ctx, docID, r, confirm); err != nil {
					if errors.Is(err, encrypt.ErrFingerprint) {
						return errors.New(T("enc.badFingerprint", "That is not the fingerprint of this key"))
					}
					return err
				}
				e.draft, e.confirm, e.sharedFor = "", "", ""
				return nil
			}
			props.Remove = func(ctx context.Context, fp string) (int, error) {
				sh, err := e.store.Sharing()
				if err != nil {
					return 0, err
				}
				n, err := sh.Remove(ctx, docID, fp)
				e.sharedFor = ""
				return n, err
			}
			props.OnRemoved = func(_ string, n int) { e.removedNotice = components.RemovedNotice(n) }
			props.Notice = e.removedNotice
		}
	}
	nodes := []xango.Node{components.Recipients(props)}
	if note != "" {
		nodes = append([]xango.Node{para("enc-people-note", note, false)}, nodes...)
	}
	return xango.El("div", []xango.Attr{xango.A("id", "enc-people"), xango.A("style", "margin-top:1.2em")}, nodes...)
}

func (e *encUI) addMyPasskey(ctx context.Context) error {
	if e.store.HasKind(encstore.KindPasskey) {
		return errors.New(T("enc.havePasskey", "Your passkey is already one of this store's keys."))
	}
	if _, err := client.CreateLocal(ctx, encSlot, "Gojiberry showcase", "showcase owner"); err != nil {
		if errors.Is(err, client.ErrCancelled) {
			return nil
		}
		return err
	}
	secret, err := deriveSecret(ctx)
	if errors.Is(err, errCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	k, err := encstore.NewPasskeyKey(secret, T("enc.passkeyLabel", "My passkey"))
	clear(secret)
	if err != nil {
		return err
	}
	if err := e.store.AddOwner(ctx, k); err != nil {
		return err
	}
	row := components.RecipientRow{Label: k.Label, Type: k.Recipient.Type, Fingerprint: k.Recipient.Fingerprint}
	e.myAdded, e.sharedFor = &row, ""
	return nil
}

func (e *encUI) createRecoveryKey(ctx context.Context) (string, error) {
	k, secret, err := encstore.NewRecoveryKey(T("enc.recoveryLabel", "Recovery key"))
	if err != nil {
		return "", err
	}
	k.Unlock = nil
	if err := e.store.AddOwner(ctx, k); err != nil {
		return "", err
	}
	e.sharedFor = ""
	return secret, nil
}
