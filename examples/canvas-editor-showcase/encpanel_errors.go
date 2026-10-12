//go:build js && wasm

package main

import (
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"strings"
)

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
	case errors.Is(err, encstore.ErrStopped):
		msg = T("enc.err.stopped", "Stopped. Nothing was changed: the keys are as they were.")
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
