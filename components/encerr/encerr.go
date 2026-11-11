package encerr

import (
	"errors"
	"sync"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

var once sync.Once

// Install registers the groups.
func Install() { once.Do(func() { components.RegisterErrorGroups(Groups()...) }) }

func Groups() []components.ErrorGroup {
	return []components.ErrorGroup{
		{Kind: components.ErrKindUnsigned, ID: "err.unsigned", Default: "A file in this document was saved by something that is not signed in as an owner, so it was not opened. If it is yours, ask the owner of the document to accept it; if you do not know where it came from, do not trust it.",
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrUnsignedAfterFlip) }},
		{Kind: components.ErrKindForged, ID: "err.forged", Default: "A file of this document was not written by one of its owners, so it was not opened. It may have been changed outside the app. Ask the owner of the document, or restore it from a copy you trust.",
			Match: func(err error) bool {
				return errors.Is(err, encrypt.ErrForgedFrame) || errors.Is(err, encrypt.ErrWrongDocument)
			}},
		{Kind: components.ErrKindNoSigner, ID: "err.noSigner", Default: "This document must be saved with an owner's signature, and this device cannot give it. Unlock the store with an owner key, or ask the owner to save it. Nothing was changed.",
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrNoFrameSigner) }},
		{Kind: components.ErrKindUnreadable, ID: "err.unreadable", Default: "A stored file of this document is damaged or cannot be opened here, so nothing was changed. Restore the document from a download or backup, or ask the owner.",
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrUnreadable) }},
		{Kind: components.ErrKindNoKey, ID: "err.noKey", Default: "None of the keys unlocked here opens this document. Unlock the store with the passkey, recovery key or SSH key it was protected with, or ask its owner to add yours.",
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrNoKey) }},
		{Kind: components.ErrKindFloor, ID: "err.floor", Default: "The browser would not keep this app's safety record (a private window or blocked site data can do that). Your work is safe for now, but protection against old copies is lost when you reload. Allow site data for this page.",
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrFloorDegraded) }},
		{Kind: components.ErrKindRecovery, ID: "err.recovery", Default: "A passkey cannot be the only key: add a recovery key or an SSH key too, so losing the passkey does not lose the documents.",
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrNeedSecondRecipient) }},
		{Kind: components.ErrKindRolledBack, As: docstore.ErrDowngrade,
			Match: func(err error) bool { return errors.Is(err, encrypt.ErrStaleRecord) }},
	}
}
