//go:build js && wasm

package main

import (
	"context"
	"errors"
	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
	"strings"
)

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
				var fu *encstore.FirstUseError
				if errors.As(err, &fu) {
					e.firstUse = fu // nothing is unlocked: the person is asked to check the list first
					return nil
				}
				return err
			}
			e.firstUse = nil
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
		forgetStoreMemory()
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
		forgetStoreMemory()
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
