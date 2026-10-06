package encrypt

import (
	"context"
	"errors"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

// PackSealer returns the two functions a .opendoc pack needs for confidential documents:
// seal (saver.PackOptions.Seal) encrypts a document's .open message to the keys in to, and
// open (saver.ImportOptions.Open) decrypts an envelope with keys. Both use this package's own
// envelope (the same age file bound to the record's id that Adapter writes), over a scratch
// store that is made for each call and never kept, so nothing is written anywhere and the
// functions are safe to call from several goroutines.
//
// Seal for the owner's keys only (a pack is a backup the owner can open again), not for the
// people a document is shared with; with no recipients seal fails (ErrNoRecipients), and with
// no identities open fails with ErrNoKey (which is docstore.ErrLocked). A caller whose store is
// locked passes neither, and the saver leaves the confidential documents out and says so.
func PackSealer(to []age.Recipient, keys []age.Identity) (seal func(ctx context.Context, docID string, plain []byte) ([]byte, error), open func(ctx context.Context, docID string, env []byte) ([]byte, error)) {
	to, keys = append([]age.Recipient(nil), to...), append([]age.Identity(nil), keys...)
	seal = func(ctx context.Context, docID string, plain []byte) ([]byte, error) {
		scratch := docstore.NewMemoryAdapter()
		if err := New(scratch, Static{To: to}).Save(ctx, docID, plain); err != nil {
			return nil, err
		}
		raw, err := scratch.Load(ctx, docID)
		if err != nil {
			return nil, err
		}
		return raw.Data, nil
	}
	open = func(ctx context.Context, docID string, env []byte) ([]byte, error) {
		if len(keys) == 0 {
			return nil, ErrNoKey
		}
		if !IsEncrypted(env) {
			return nil, errors.New("encrypt: the pack record is not encrypted")
		}
		scratch := docstore.NewMemoryAdapter()
		if err := scratch.Save(ctx, docID, env); err != nil {
			return nil, err
		}
		got, err := New(scratch, Static{Keys: keys}).Load(ctx, docID)
		if err != nil {
			return nil, err
		}
		return got.Data, nil
	}
	return seal, open
}
