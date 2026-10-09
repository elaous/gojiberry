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
// to, so the passkey is a local one (client.CreateLocal / DeriveKey): the browser's
// authenticator derives the encryption key, nothing is sent anywhere. Encryption is OFF unless
// the person turns it on; the keys' public halves are kept on this device (encConfigKey), the
// private side is in memory only, while unlocked.
//
// Every change to the keys is a transaction in encstore (a journal, each file read back, the old
// state put back on failure); this file is the screens around it: a recovery key's secret is
// shown and confirmed BEFORE anything is committed, a passkey alone needs an explicit
// acknowledgement, a change that was cut short is finished or undone from the panel, and a store
// whose keys are all lost can be erased.

const (
	encSlot      = "showcase"            // where client.CreateLocal keeps the passkey's credential ID
	encConfigKey = "showcase-enc:config" // the owner keys' public halves; not under the store's own prefix
	// encSealedKey marks a store that was turned on here. It is only a hint (whoever can write the
	// storage can delete it too): a missing configuration is shown as missing, not as "off", when
	// it is set or when encrypted files are found.
	encSealedKey = "showcase-enc:sealed"
	browserLabel = "This browser (localStorage)"
)

// prepared is a recovery key that was made and whose secret is on screen: it is not an owner key
// until the person has said they saved the secret and confirmed.
type prepared struct {
	key    encstore.NewKey
	secret string
	acked  bool
}

// encUI is the encryption state of the page.
type encUI struct {
	store   *encstore.Store
	browser *docstore.BrowserAdapter
	pending *components.Pending
	render  func()

	setup                    bool // the switch was turned on and the form is open
	wantPasskey, wantRecover bool
	sshPub                   string
	passkeyAck               bool      // the person accepted that a passkey alone puts every document at risk
	prep                     *prepared // setup: the recovery key waiting for its secret to be confirmed
	pendingCred              string    // a passkey that was made and could not be used yet: retry or forget it
	noPRF                    bool      // the authenticator made a passkey that cannot encrypt: the person has to delete it
	unlockRecovery           string
	notice                   string

	eraseTyping bool // the first question was answered: the word is asked
	eraseWord   string

	// broken is why the key configuration cannot be trusted (encstore.ErrConfigMissing: it is gone
	// over encrypted files; encstore.ErrConfigCorrupt: it cannot be read): the store is read-only
	// and the panel offers a restore or Erase, never "encryption off".
	broken         error
	restoreRecover string // the recovery key typed to restore with

	// the People tab: the current document's keys
	sharedFor       string
	docConfidential bool
	shared          []components.RecipientRow
	draft, confirm  string
	myAdded         *components.RecipientRow
	removedNotice   string
	peopleKey       *prepared // the recovery key being added from the People tab

	dir dirUI

	// swap makes the saver use the store's adapter as it stands now (set by the page): called after
	// every change, so a change that left the store interrupted, or one that finished, is followed.
	swap func()
}

// readEncConfig reads the key configuration. Nothing stored is an empty Config and no error;
// something stored that cannot be read is an error (a corrupt configuration is not "encryption
// off").
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

// reloadEncConfig is what the store compares its own Config with before every write: whatever is
// stored now, or an empty Config when it cannot be read (the store that started with a configuration
// it could not read has an empty one too).
func reloadEncConfig() (encstore.Config, error) {
	c, _ := readEncConfig()
	return c, nil
}

func encSealedHint() bool {
	defer func() { _ = recover() }()
	v := js.Global().Get("localStorage").Call("getItem", encSealedKey)
	return !v.IsNull() && !v.IsUndefined() && v.String() == "1"
}

func newEncStore(browser *docstore.BrowserAdapter, cfg encstore.Config) (*encstore.Store, error) {
	st, err := encstore.New(browser, "browser", cfg)
	if err != nil {
		return nil, err
	}
	// Capacity is counted in the characters localStorage keeps (some browsers count 2 bytes each):
	// 5 Mi characters is the least any of them gives.
	st.Persist, st.Reload, st.Capacity = writeEncConfig, reloadEncConfig, 5<<20
	return st, nil
}

// newEncOff is the page with encryption off after the key list could not be read: why is the
// notice the Save tab shows. (Kept for a configuration that parses but cannot be used.)
func newEncOff(browser *docstore.BrowserAdapter, render func(), why error) (*encUI, error) {
	st, err := newEncStore(browser, encstore.Config{})
	if err != nil {
		return nil, err
	}
	e := &encUI{store: st, browser: browser, render: render, notice: T("enc.err.keyListRead", "The saved key list could not be read: ") + why.Error()}
	e.pending = components.NewPending(render)
	e.dir = newDirUI()
	return e, nil
}

func newEnc(browser *docstore.BrowserAdapter, render func()) (*encUI, error) {
	cfg, rerr := readEncConfig()
	why := encstore.Inspect(context.Background(), browser, cfg, rerr)
	if why == nil && len(cfg.Owners) == 0 && cfg.Pending == nil && encSealedHint() {
		why = encstore.ErrConfigMissing // set once, and the configuration is gone: not "off"
	}
	if why != nil {
		cfg = encstore.Config{} // nothing is trusted: the store stays read-only until restored or erased
	}
	st, err := newEncStore(browser, cfg)
	if err != nil {
		return nil, err
	}
	e := &encUI{store: st, browser: browser, render: render, wantPasskey: client.Supported(), wantRecover: true, broken: why}
	e.pending = components.NewPending(render)
	e.dir = newDirUI()
	return e, nil
}

// target is the browser target as it stands: the encrypted adapter when encryption is on.
func (e *encUI) target() saver.Target {
	if e.broken != nil {
		return saver.Target{Name: "browser", Label: T("enc.browserLabelBroken", "This browser (read-only: key configuration lost)"), Adapter: encstore.ReadOnly(e.browser, e.broken), Capacity: 5 << 20}
	}
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
		"interrupted": e.store.Interrupted(), "prepared": e.prep != nil, "pendingPasskey": e.pendingCred != "",
		"secretShown": e.prep != nil || e.peopleKey != nil, "stale": e.store.Stale(), "broken": e.brokenKind(),
		"dir": map[string]any{"url": e.dir.url, "offer": e.dir.offer, "host": e.dir.host(), "tokenSet": e.dir.token != "", "refused": e.dir.refused}}
}

// brokenKind names why the configuration cannot be trusted for the e2e test: "", "missing" or "corrupt".
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

// --- messages: the errors of encstore and the passkey client, in the interface's language ---

// errHandled says a step stopped for a reason the page already put on screen (a notice): the caller
// returns it and Run drops it.
var errHandled = errors.New("handled")

// shownError is an error with the words to show; the original stays reachable (errors.Is).
type shownError struct {
	msg string
	err error
}

func (s *shownError) Error() string { return s.msg }
func (s *shownError) Unwrap() error { return s.err }

// UserMessage says the text is already in words for a person: components.ExplainError shows it and keeps the
// original error's text behind "Details".
func (s *shownError) UserMessage() string { return s.msg }

// tErr puts the errors this page knows into the interface's language; others pass as they are, and
// components.ExplainError gives them a generic message with the text behind "Details".
func tErr(err error) error {
	if err == nil || errors.Is(err, errHandled) {
		return err
	}
	var shown *shownError
	if errors.As(err, &shown) {
		return err // already in words
	}
	var nr *encstore.NoRoomError
	var ur *encstore.UnreadableError
	var msg string
	switch {
	case errors.Is(err, encstore.ErrRollbackFailed):
		cause := strings.TrimSpace(strings.TrimPrefix(err.Error(), encstore.ErrRollbackFailed.Error()))
		msg = T("enc.err.rollbackFailed", "The change failed and could not be undone completely. Reload this page: the store will offer to finish the change or undo it.") + " " + cause
	case errors.As(err, &nr):
		msg = T("enc.err.noRoom", "There is not enough room in the browser's storage: about %d more bytes are needed and about %d are free. Nothing was changed. Download your documents and delete what you do not need, then try again.", nr.Need, nr.Free)
	case errors.As(err, &ur):
		msg = T("enc.err.unreadable", "%d stored files cannot be opened with the keys unlocked here, so the change would lose them. Nothing was changed. Unlock with another key, or erase the store if its keys are lost.", len(ur.IDs))
	case errors.Is(err, encstore.ErrLocked):
		msg = T("enc.err.locked", "The store is locked. Unlock it with one of its keys first.")
	case errors.Is(err, encstore.ErrNotAKey):
		msg = T("enc.err.notAKey", "That is not one of this store's keys.")
	case errors.Is(err, encstore.ErrStaleConfig):
		msg = T("enc.err.stale", "The keys of this store were changed in another tab or window since this page loaded, so this tab does nothing with them. Reload the page.")
	case errors.Is(err, encstore.ErrConfigChanged):
		msg = T("enc.err.configChanged", "The key configuration on this device was changed since this key last opened the store, so it was not unlocked. If you did not change it, someone who can write this site's storage did: do not use the store here.")
	case errors.Is(err, encstore.ErrInterrupted):
		msg = T("enc.err.interrupted", "An earlier change to this store's keys did not finish. Finish it or undo it first.")
	case errors.Is(err, encstore.ErrSecretNotSaved):
		msg = T("enc.err.secretNotSaved", "The recovery key's secret has not been saved: confirm that you saved it first.")
	case errors.Is(err, encstore.ErrPasskeyOnly):
		msg = T("enc.err.passkeyOnly", "Passkeys alone are refused unless you accept that losing them loses every document in this store.")
	case errors.Is(err, encstore.ErrLastOwner):
		msg = T("enc.err.lastOwner", "That is the store's only key.")
	case errors.Is(err, encstore.ErrWouldLoseRecovery):
		msg = T("enc.err.wouldLoseRecovery", "That would leave only passkeys: keep a recovery key or an SSH key.")
	case errors.Is(err, encstore.ErrUseAnotherKey):
		msg = T("enc.err.useAnotherKey", "Unlock the store with a different key to do that.")
	case errors.Is(err, encstore.ErrNoSuchOwner):
		msg = T("enc.err.noSuchOwner", "That key is not one of the store's.")
	case errors.Is(err, encstore.ErrKeyTwice):
		msg = T("enc.err.keyTwice", "That key is already one of the store's, or was given twice.")
	case errors.Is(err, encstore.ErrNoKeys):
		msg = T("enc.chooseKey", "Choose at least one key: a passkey, a recovery key or an SSH public key.")
	case errors.Is(err, encstore.ErrAlreadyOn):
		msg = T("enc.err.alreadyOn", "Encryption is already on.")
	case errors.Is(err, encstore.ErrDisabled):
		msg = T("enc.err.disabled", "Encryption is not on for this store.")
	case errors.Is(err, encstore.ErrNothingPending):
		msg = T("enc.err.nothingPending", "There is no unfinished change.")
	case errors.Is(err, encstore.ErrConfidentialLeft):
		msg = T("enc.confidentialLeft", "Make the confidential documents and folders not confidential first: turning encryption off would leave their names and files unreadable.")
	case errors.Is(err, encrypt.ErrNeedSecondRecipient):
		msg = T("enc.err.needSecond", "A passkey cannot be the only key: add a recovery key or an SSH key too")
	case errors.Is(err, encrypt.ErrFingerprint):
		msg = T("enc.badFingerprint", "That is not the fingerprint of this key")
	case errors.Is(err, docstore.ErrStorageFull):
		msg = T("enc.err.storageFull", "The browser's storage is full. Nothing was changed.")
	}
	if msg == "" {
		return err
	}
	return &shownError{msg: msg, err: err}
}

// passkeyFailed turns a failed ceremony into what the person is told. A closed prompt is a notice,
// not silence and not an alarm; the other failures say what happened and what to do.
func (e *encUI) passkeyFailed(err error) error {
	switch {
	case errors.Is(err, client.ErrTimedOut):
		return &shownError{T("enc.passkey.timedOut", "The passkey prompt timed out before anything was chosen. Nothing was changed: try again."), err}
	case errors.Is(err, client.ErrNotVerified):
		return &shownError{T("enc.passkey.notVerified", "The authenticator could not verify you (a wrong PIN or fingerprint, or no screen lock is set up on this device). Nothing was changed."), err}
	case errors.Is(err, client.ErrCancelled):
		e.notice = T("enc.passkey.cancelled", "The passkey prompt was closed or refused, so nothing was changed. If you did not close it, the authenticator may not have been able to verify you, or the prompt may have timed out: try again.")
		return errHandled
	case errors.Is(err, client.ErrNoPRF):
		e.noPRF = true
		return &shownError{T("enc.passkey.noPRF", "This passkey cannot be used to encrypt (the authenticator does not support the PRF extension). A passkey for this site was still created on it: delete it in your browser's or authenticator's passkey settings (it is called \"%s\"). Use another passkey, an SSH key or a recovery key instead.", T("enc.passkeyUser", "Showcase owner")), err}
	case errors.Is(err, client.ErrPRFLength):
		return &shownError{T("enc.passkey.prfLength", "This passkey answered in a form that cannot be used to encrypt. Use another passkey, an SSH key or a recovery key."), err}
	case errors.Is(err, client.ErrAlreadyRegistered):
		return &shownError{T("enc.passkey.already", "This device already has a passkey for this site; use another device or another security key."), err}
	case errors.Is(err, client.ErrInsecureContext):
		return &shownError{T("enc.passkey.insecure", "Passkeys work only on https pages and on localhost, for the page's own domain."), err}
	case errors.Is(err, client.ErrUnsupportedAuthenticator):
		return &shownError{T("enc.passkey.unsupported", "This authenticator cannot make the passkey the page asked for (a discoverable passkey with user verification)."), err}
	}
	return tErr(err)
}

// run is pending.Run with the page's errors in the interface's language and handled stops dropped.
func (e *encUI) run(f func(ctx context.Context) error) { e.pending.Run(e.wrapDo(f)) }

// wrapDo is run for an operation a component calls itself (it runs it under e.pending).
func (e *encUI) wrapDo(f func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		err := f(ctx)
		if e.swap != nil {
			e.swap()
		}
		if errors.Is(err, errHandled) {
			return nil
		}
		return tErr(err)
	}
}

// --- the passkey ceremony ---

// makePasskey makes (or, after a derivation that failed, reuses) this device's passkey and derives
// the owner key from it. A passkey that was made and could not be used yet is remembered
// (pendingCred) so retrying does not make another one; Forget drops it.
func (e *encUI) makePasskey(ctx context.Context) (encstore.NewKey, error) {
	e.notice = ""
	if e.pendingCred == "" {
		id, err := client.CreateLocal(ctx, encSlot, T("enc.passkeyRP", "Gojiberry showcase"), T("enc.passkeyUser", "Showcase owner"))
		if err != nil {
			return encstore.NewKey{}, e.passkeyFailed(err)
		}
		e.pendingCred = id
	}
	_, secret, err := client.DeriveKey(ctx, e.pendingCred, encrypt.PRFSalt)
	if err != nil {
		return encstore.NewKey{}, e.passkeyFailed(err)
	}
	k, err := encstore.NewPasskeyKey(secret, T("enc.passkeyLabel", "My passkey"))
	clear(secret) // the secret is the root of the key: do not keep it around
	if err != nil {
		return encstore.NewKey{}, err
	}
	return k, nil
}

func (e *encUI) forgetPasskey() {
	prev := e.pendingCred
	e.pendingCred, e.noPRF = "", false
	if prev != "" && client.LocalCredential(encSlot) == prev {
		client.ForgetLocal(encSlot)
	}
	e.notice = T("enc.passkey.forgotten", "Forgotten here. The passkey is still in your authenticator: delete it in your browser's or authenticator's passkey settings if you do not want it.")
	e.render()
}

// --- unlock and lock ---

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

// lockNow forgets the keys and everything on screen that they opened: the document in the editor
// and what the session knows of it (its name, ID, tag filter, revealed names), the lists' real names
// and what the People tab showed.
func (u *ui) lockNow() {
	e := u.enc
	e.store.Lock()
	e.prep, e.peopleKey, e.unlockRecovery, e.sharedFor, e.shared, e.myAdded = nil, nil, "", "", nil, nil
	e.draft, e.confirm, e.docConfidential, e.removedNotice = "", "", false, ""
	u.saver.Forget()
	u.saver.Clear()
	if u.clearEditor != nil {
		u.clearEditor()
	}
}

// hasUnsavedEdits says whether the document in the editor holds changes that are not stored.
func (u *ui) hasUnsavedEdits() bool {
	switch u.saver.SaveState() {
	case saver.SaveUnsaved, saver.SaveSaving, saver.SaveFailed:
		return true
	}
	return false
}

// lockDiscard locks without saving: the one way to drop unsaved changes on purpose.
func (u *ui) lockDiscard() {
	u.lockNow()
	u.enc.run(u.saver.Refresh)
}

// askLockDiscard asks before lockDiscard: what was changed since the last save is lost.
func (u *ui) askLockDiscard() {
	u.saver.Asker.AskWith(T("enc.lockDiscard", "Lock and discard unsaved changes"),
		T("enc.lockDiscardConfirm", "Discard the changes you have not saved, close the document and lock? What you changed since the last save is lost and cannot be brought back."),
		u.lockDiscard)
	u.rerender()
}

// encLock locks the store. With edits that are not saved it asks first (Save and lock): locking
// closes the document, and would drop them.
func (u *ui) encLock() {
	e := u.enc
	if u.hasUnsavedEdits() {
		u.saver.Asker.AskWith(T("enc.saveAndLock", "Save and lock"),
			T("enc.lockUnsaved", "The document that is open has changes that are not saved. Locking closes it. Save it and lock, or cancel to keep working."),
			func() {
				e.run(func(ctx context.Context) error {
					if err := u.saver.Save(ctx); err != nil {
						return err // not locked: nothing is lost
					}
					u.lockNow()
					return u.saver.Refresh(ctx)
				})
			})
		u.rerender()
		return
	}
	u.lockNow()
	e.run(u.saver.Refresh)
}

func (u *ui) encUnlockPasskey() {
	e := u.enc
	e.notice = ""
	e.run(func(ctx context.Context) error {
		_, secret, err := client.DeriveLocal(ctx, encSlot, encrypt.PRFSalt)
		if err != nil {
			return e.passkeyFailed(err)
		}
		if err := e.store.UnlockPasskey(secret); err != nil { // clears secret on every path
			return err
		}
		return u.afterUnlock(ctx)
	})
}

func (u *ui) encUnlockRecovery() {
	e := u.enc
	key := strings.TrimSpace(e.unlockRecovery)
	e.run(func(ctx context.Context) error {
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
		e.run(func(ctx context.Context) error {
			defer clear(data) // the private key file: dropped on every path (UnlockSSH clears it too); the run is asynchronous, so it is dropped here, not in the callback
			if err != nil {
				return err
			}
			if err := e.store.UnlockSSH(data); err != nil {
				return err
			}
			return u.afterUnlock(ctx)
		})
	})
}

// --- turning encryption on ---

// encEnable is the setup form's button. With a recovery key chosen it only makes the key and shows
// its secret (nothing is committed); the person confirms they saved it, and encCommit does the
// rest. Without one it commits at once.
func (u *ui) encEnable() {
	e := u.enc
	e.notice = ""
	if e.prep == nil && e.wantRecover {
		k, secret, err := encstore.NewRecoveryKey(T("enc.recoveryLabel", "Recovery key"))
		if err != nil {
			e.run(func(context.Context) error { return err })
			return
		}
		e.prep = &prepared{key: k, secret: secret}
		u.rerender()
		return
	}
	u.encCommit()
}

// encCommit makes the passkey (if chosen), takes the SSH key, and turns encryption on with them and
// the recovery key whose secret was confirmed.
func (u *ui) encCommit() {
	e := u.enc
	want := struct {
		passkey bool
		ssh     string
	}{e.wantPasskey && client.Supported(), strings.TrimSpace(e.sshPub)}
	e.run(func(ctx context.Context) error {
		var keys []encstore.NewKey
		if want.passkey {
			k, err := e.makePasskey(ctx)
			if err != nil {
				return err
			}
			keys = append(keys, k)
		}
		if e.prep != nil {
			rk := e.prep.key
			rk.SecretSaved = e.prep.acked
			rk.Forget = len(keys) > 0 // the passkey unlocks; the recovery key is for the day it is lost
			keys = append(keys, rk)
		}
		if want.ssh != "" {
			k, err := encstore.NewSSHKey(want.ssh)
			if err != nil {
				return err
			}
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			return encstore.ErrNoKeys
		}
		if err := e.store.Enable(ctx, encstore.Options{AcceptPasskeyOnly: e.passkeyAck}, keys...); err != nil {
			return err // the passkey (if made) stays pending: retrying does not make another
		}
		e.setup, e.sshPub, e.prep, e.pendingCred, e.passkeyAck, e.noPRF = false, "", nil, "", false, false
		e.sharedFor = ""
		u.encSwap()
		return u.saver.Refresh(ctx)
	})
}

// --- turning it off, erasing, recovering ---

// encDisable turns encryption off: the store must be unlocked and have nothing confidential.
func (u *ui) encDisable() {
	e := u.enc
	e.run(func(ctx context.Context) error {
		if !e.store.Unlocked() {
			return errors.New(T("enc.unlockFirst", "Unlock the store first: turning encryption off decrypts what is saved."))
		}
		list, trashed, folders, err := u.saver.Saver.Overview(ctx, "browser")
		if err != nil {
			return err
		}
		for _, en := range append(list, trashed...) {
			if en.Protection == saver.ProtectionE2EE {
				return encstore.ErrConfidentialLeft
			}
		}
		for _, f := range folders {
			if f.Protection == saver.ProtectionE2EE {
				return encstore.ErrConfidentialLeft
			}
		}
		if err := e.store.Disable(ctx); err != nil {
			return err
		}
		client.ForgetLocal(encSlot)
		u.encSwap()
		u.saver.Forget()
		e.prep, e.peopleKey, e.sharedFor, e.shared, e.myAdded = nil, nil, "", nil, nil
		return u.saver.Refresh(ctx)
	})
}

// encErase deletes the encrypted store's documents and forgets its keys.
func (u *ui) encErase() {
	e := u.enc
	e.run(func(ctx context.Context) error {
		if err := e.store.Erase(ctx); err != nil {
			return err
		}
		client.ForgetLocal(encSlot)
		e.eraseTyping, e.eraseWord, e.setup, e.broken = false, "", false, nil
		e.prep, e.peopleKey, e.pendingCred, e.noPRF = nil, nil, "", false
		u.encSwap()
		u.lockNow()
		e.notice = T("enc.erased", "The encrypted store was erased. Other places you saved to were not touched. A passkey you used stays in your authenticator: delete it there if you do not want it.")
		return u.saver.Refresh(ctx)
	})
}

// encRecover finishes or undoes a change that was cut short.
func (u *ui) encRecover(finish bool) {
	e := u.enc
	e.run(func(ctx context.Context) error {
		if err := e.store.Recover(ctx, finish); err != nil {
			if errors.Is(err, encstore.ErrConfigChanged) && !errors.Is(err, encstore.ErrStaleConfig) {
				return &shownError{T("enc.err.journalRefused", "This unfinished change is not vouched for by the key unlocked here: it does not belong to this store's keys, so it may not have been made by you. Unlock with another key of the store, undo it, or erase the store."), err}
			}
			return err
		}
		e.sharedFor = ""
		u.encSwap()
		if finish {
			e.notice = T("enc.recovered", "The change was finished.")
		} else {
			e.notice = T("enc.undone", "The change was undone: the store is as it was before.")
		}
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

// secretBox shows a recovery key before it is committed: the secret, Copy and Download, a
// confirmation that it was saved, and the button that adds the key. The key is not an owner key
// until that button is pressed, which needs the confirmation.
func (u *ui) secretBox(prefix string, p *prepared, commitLabel string, commit, cancel func()) xango.Node {
	return xango.El("div", []xango.Attr{xango.A("id", prefix+"-box"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:8px 0")},
		para(prefix+"-warning", T("enc.recoveryWarning", "This is your recovery key. It is shown only now. Keep it offline (print it or store it in a password manager). We cannot recover it: if you lose it and your passkey, what you made confidential is lost."), true),
		textarea(prefix+"-secret", T("enc.recoverySecret", "Recovery key (secret)"), p.secret, "", "2", func(string) {}),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
			btn(prefix+"-copy", T("enc.copy", "Copy"), func() { u.saver.Copy(p.secret) }, false),
			btn(prefix+"-download", T("enc.download", "Download"), func() { transfer.Download("recovery-key.txt", "text/plain", []byte(p.secret+"\n")) }, false)),
		para(prefix+"-notyet", T("enc.notYet", "Nothing has been changed yet: the key is added only when you confirm below."), false),
		xango.El("div", []xango.Attr{xango.A("id", prefix+"-ack")}, components.Checkbox(components.CheckboxProps{
			Label: T("enc.ackSaved", "I have saved this recovery key somewhere safe"), Checked: p.acked,
			OnChange: func(on bool) { p.acked = on; u.rerender() }})),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:6px")},
			btn(prefix+"-commit", commitLabel, commit, !p.acked || u.enc.pending.Busy()),
			btn(prefix+"-cancel", T("enc.cancelKey", "Cancel: do not add it"), cancel, u.enc.pending.Busy())))
}

// encPanel is the "Encrypt this store" control above the save form.
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

// stalePanel is shown by a tab whose key configuration was changed elsewhere: it is locked and
// refuses every change until the page is reloaded.
func (u *ui) stalePanel() []xango.Node {
	return []xango.Node{
		para("enc-stale", T("enc.stale", "The keys of this store were changed in another tab or window since this page loaded. This tab is locked so it cannot overwrite that change: reload the page."), true),
		btn("enc-reload", T("enc.reload", "Reload the page"), func() { js.Global().Get("location").Call("reload") }, false)}
}

// watchEncConfig locks this tab when another one changes the key configuration (the browser's
// storage event, which fires in the other tabs of the origin, not in the one that wrote).
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

// encConfigChanged is the storage event's work: the store checks whether the stored configuration is
// still its own and, if not, locks; the editor is cleared like on any lock.
func (u *ui) encConfigChanged() {
	e := u.enc
	e.store.ConfigChangedElsewhere()
	if e.store.Stale() {
		u.lockNow()
		e.notice = ""
		e.render()
	}
}

// orphanPasskey explains a passkey that was made and is not in use: retry it, or forget it.
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
					u.rerender() // the retry is the action that needed it: Add my passkey (People tab)
					return
				}
				u.encCommit()
			}, e.pending.Busy()),
			btn("enc-orphan-forget", T("enc.orphanForget", "Forget it"), e.forgetPasskey, e.pending.Busy()))}
}

// encBrokenPanel is what the Save tab shows when the key configuration is gone (over encrypted
// files) or cannot be read: not "encryption off", but a read-only store with the ways out, which
// are a restore from a key that opens the files, or Erase.
func (u *ui) encBrokenPanel() xango.Node {
	e := u.enc
	title := T("enc.missingTitle", "Configuration missing: encrypted files found")
	if e.brokenKind() == "corrupt" {
		title = T("enc.corruptTitle", "The key configuration could not be read")
	}
	kids := []xango.Node{
		para("enc-broken", title, true),
		para("enc-broken-text", T("enc.brokenText", "The documents in this browser's store are encrypted, but the list of their keys is gone or damaged, so this page does not know the store's keys. The store is read-only: nothing new is saved here (saving plain text next to the ciphertext would be wrong). If you hold one of the store's keys, restore from the files: that key opens them and becomes the store's only key (add the others again afterwards; files whose lists were signed by another key than the one you restore with may not restore, and then only Erase is left). Otherwise erase the store."), false)}
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

// encRestore rebuilds the configuration from the key make returns (encstore.Store.Restore), leaves
// the store unlocked with it and shows the names it opens.
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

// encErasePanel is "Erase the encrypted store": the way out when every key is lost. Two questions:
// a confirmation that says what it does, and the word typed.
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

// encStatus is the state of an encrypted store: locked or not, and how to change that.
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
		}
	}
	rows = append(rows, para("enc-trust", T("enc.trustNote", "The list of this store's keys is kept on this device next to the store. The keys sign it, so a list that someone changed is refused when you unlock (an SSH-only store checks nothing until its key has unlocked it once). Someone who can write this site's storage can still delete the list or put back an older one: do not rely on this on a computer you do not control."), false))
	return rows
}

// ownerList says a list of owner keys in words, with the fingerprint of each: what a journal would
// make the store's keys, shown so the person can tell their own keys from someone else's.
func ownerList(list []encstore.Owner) string {
	var parts []string
	for _, o := range list {
		parts = append(parts, fmt.Sprintf("%s (%s, %s)", o.Label, o.Kind, o.Fingerprint()))
	}
	return strings.Join(parts, "; ")
}

// interruptedPanel is the banner of a change that was cut short. The journal is data in the browser's
// storage, which another script or person may have written: so it shows what it would make the
// store's keys, with fingerprints, and Finish and Undo ask for an explicit confirmation naming them.
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

// askRecover asks before a journal is finished or undone, naming the keys it concerns.
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

// ownerRows are the store's keys with whether each can be removed and why not.
func (e *encUI) ownerRows() []components.RecipientRow {
	rows := recipientRows(e.store.Recipients())
	for i := range rows {
		err := e.store.CanRemoveOwner(rows[i].Fingerprint)
		rows[i].CanRemove = err == nil
		if err != nil {
			rows[i].RemoveBlocked = tErr(err).Error()
		}
	}
	return rows
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
							v := e.dir.verifiedAdded[s.Fingerprint]
							rows = append(rows, components.RecipientRow{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint, Verified: v, VerifiedBy: e.dir.verifiedBy(v)})
						}
					}
				}
			}
		}
		e.docConfidential, e.shared = conf, rows
		e.render()
	}()
}

// encPeople is the keys control of the People tab: the store's owner keys, with "Add my passkey",
// "Create a recovery key", removing and replacing keys (the same host code as
// examples/people-keys-demo, over the encrypted store), and, for a confidential document, who it is
// shared with.
func (u *ui) encPeople() xango.Node {
	e := u.enc
	u.refreshShared()
	unlocked := e.store.Unlocked() && !e.store.Interrupted()
	docID := u.saver.ID()
	props := components.RecipientsProps{
		Owner: e.ownerRows(), Shared: e.shared,
		Pending: e.pending, Ask: u.saver.Asker.Ask,
		Confidential: true, SecondKeyMessage: T("enc.err.needSecond", "A passkey cannot be the only key: add a recovery key or an SSH key too"),
		PasskeySupported: client.Supported(),
		Copy:             func(text string) { u.saver.Copy(text) },
		Download:         func(name, text string) { transfer.Download(name, "text/plain", []byte(text)) },
	}
	var note string
	switch {
	case e.store.Interrupted():
		note = T("enc.peopleInterrupted", "A change to the keys did not finish (Save tab): finish it or undo it before changing keys here.")
	case !unlocked:
		note = T("enc.peopleLocked", "Unlock the store (Save tab) to add keys or share documents.")
	default:
		props.AddMyPasskey, props.MyPasskeyAdded = e.wrapDo(e.addMyPasskey), e.myAdded
		props.RemoveOwner = func(ctx context.Context, fp string) error {
			return e.wrapDo(func(ctx context.Context) error { return e.removeOwner(ctx, u, fp) })(ctx)
		}
		props.ReplaceOwner = func(ctx context.Context, fp string) error {
			return e.wrapDo(func(ctx context.Context) error { return e.replacePasskey(ctx, u, fp) })(ctx)
		}
		if !e.store.HasKind(encstore.KindRecovery) || e.peopleKey != nil {
			props.CreateRecoveryKey = func(ctx context.Context) (string, error) { return e.prepareRecoveryKey() }
			props.OnRecoveryKey = func(string) { e.render() }
			props.CommitRecoveryKey = e.wrapDo(e.commitRecoveryKey)
			props.CancelRecoveryKey = func() { e.peopleKey = nil; e.render() }
			props.OnRecoveryAcknowledge = func(on bool) {
				if e.peopleKey != nil {
					e.peopleKey.acked = on
				}
				e.render()
			}
			if e.peopleKey != nil {
				props.RecoverySecret, props.RecoveryAcknowledged = e.peopleKey.secret, e.peopleKey.acked
			}
		}
		props.OnRecoveryDone = nil
		switch {
		case docID == "":
			note = T("enc.peopleNoDoc", "Save the document to share it with other keys.")
		case !e.docConfidential:
			note = T("enc.peopleNotConfidential", "This document is encrypted to your keys. Make it confidential (Save tab) to share it with other keys.")
		default:
			props.Draft, props.Confirm = e.draft, e.confirm
			if strings.TrimSpace(e.draft) != "" {
				if r, err := parseRecipient(e.draft); err == nil {
					props.Parsed = &components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Verified: e.dir.verified[r.Fingerprint], VerifiedBy: e.dir.verifiedBy(e.dir.verified[r.Fingerprint])}
				} else {
					props.ParseError = err.Error()
				}
			}
			props.OnDraft = func(k, c string) { e.draft, e.confirm = k, c; e.render() }
			props.Add = func(ctx context.Context, key, confirm string) error {
				return e.wrapDo(func(ctx context.Context) error {
					r, err := parseRecipient(key)
					if err != nil {
						return err
					}
					sh, err := e.store.Sharing()
					if err != nil {
						return err
					}
					if err := sh.Add(ctx, docID, r, confirm); err != nil {
						return err
					}
					e.dir.verifiedAdded[r.Fingerprint] = e.dir.verified[r.Fingerprint]
					e.draft, e.confirm, e.sharedFor = "", "", ""
					return nil
				})(ctx)
			}
			props.Remove = func(ctx context.Context, fp string) (int, error) {
				sh, err := e.store.Sharing()
				if err != nil {
					return 0, tErr(err)
				}
				n, err := sh.Remove(ctx, docID, fp)
				e.sharedFor = ""
				return n, tErr(err)
			}
			props.OnRemoved = func(_ string, n int) { e.removedNotice = components.RemovedNotice(n) }
			props.Notice = e.removedNotice
		}
		e.dir.wire(&props, e)
	}
	nodes := []xango.Node{components.Recipients(props)}
	nodes = append(nodes, e.dir.settingsPanel(e)...)
	if note != "" {
		nodes = append([]xango.Node{para("enc-people-note", note, false)}, nodes...)
	}
	if e.notice != "" && u.tabIsPeople() {
		nodes = append([]xango.Node{xango.El("p", []xango.Attr{xango.A("id", "enc-people-message"), xango.A("role", "status"), xango.A("style", "font-size:13px")}, xango.Text(e.notice))}, nodes...)
	}
	return xango.El("div", []xango.Attr{xango.A("id", "enc-people"), xango.A("style", "margin-top:1.2em")}, nodes...)
}

func (u *ui) tabIsPeople() bool { return u.tab == "people" }

func (e *encUI) addMyPasskey(ctx context.Context) error {
	if e.store.HasKind(encstore.KindPasskey) {
		return errors.New(T("enc.havePasskey", "Your passkey is already one of this store's keys."))
	}
	k, err := e.makePasskey(ctx)
	if err != nil {
		return err
	}
	if err := e.store.AddOwner(ctx, k); err != nil {
		return err // the passkey stays pending: pressing the button again does not make another
	}
	e.pendingCred = ""
	row := components.RecipientRow{Label: k.Label, Type: k.Recipient.Type, Fingerprint: k.Recipient.Fingerprint}
	e.myAdded, e.sharedFor = &row, ""
	return nil
}

// replacePasskey swaps the passkey for a new one in one transaction. The slot that remembers which
// passkey to ask is put back if the swap fails, so the next unlock still asks the one that works.
func (e *encUI) replacePasskey(ctx context.Context, u *ui, fp string) error {
	prev := client.LocalCredential(encSlot)
	id, err := client.CreateLocal(ctx, encSlot, T("enc.passkeyRP", "Gojiberry showcase"), T("enc.passkeyUser", "Showcase owner"))
	if err != nil {
		client.SetLocalCredential(encSlot, prev)
		return e.passkeyFailed(err)
	}
	restore := func() { client.SetLocalCredential(encSlot, prev) }
	_, secret, err := client.DeriveKey(ctx, id, encrypt.PRFSalt)
	if err != nil {
		restore()
		return e.passkeyFailed(err)
	}
	k, err := encstore.NewPasskeyKey(secret, T("enc.passkeyLabel", "My passkey"))
	clear(secret)
	if err != nil {
		restore()
		return err
	}
	if err := e.store.ReplaceOwner(ctx, fp, k); err != nil {
		restore()
		return err
	}
	e.sharedFor, e.myAdded = "", nil
	e.removedNotice = T("enc.replaced", "The passkey was replaced: the new one opens the store from now on. The old one is still in your authenticator: delete it in your browser's or authenticator's passkey settings.")
	return u.saver.Refresh(ctx)
}

func (e *encUI) removeOwner(ctx context.Context, u *ui, fp string) error {
	if err := e.store.RemoveOwner(ctx, fp); err != nil {
		return err
	}
	e.sharedFor, e.myAdded = "", nil
	e.removedNotice = T("enc.keyRemoved", "The key was removed and everything saved was encrypted again without it. Whoever held it keeps what they already read or copied.")
	if !e.store.HasKind(encstore.KindPasskey) {
		client.ForgetLocal(encSlot)
	}
	return u.saver.Refresh(ctx)
}

// prepareRecoveryKey makes a recovery key and returns its secret to be shown. Nothing is added to
// the store: commitRecoveryKey does that once the person confirmed they saved the secret.
func (e *encUI) prepareRecoveryKey() (string, error) {
	k, secret, err := encstore.NewRecoveryKey(T("enc.recoveryLabel", "Recovery key"))
	if err != nil {
		return "", err
	}
	e.peopleKey = &prepared{key: k, secret: secret}
	return secret, nil
}

func (e *encUI) commitRecoveryKey(ctx context.Context) error {
	p := e.peopleKey
	if p == nil {
		return nil
	}
	k := p.key
	k.SecretSaved, k.Forget = p.acked, true // another key is unlocked; this one is for the day it is lost
	if err := e.store.AddOwner(ctx, k); err != nil {
		return err // the secret stays on screen: it can be tried again
	}
	e.peopleKey = nil
	e.sharedFor = ""
	return nil
}
