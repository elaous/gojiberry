package encrypt

import (
	"context"
	"errors"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

// ErrWrongPackKey is what open returns when no key fits.
var ErrWrongPackKey error = wrongPackKeyError{}

type wrongPackKeyError struct{}

func (wrongPackKeyError) Error() string {
	return "encrypt: this pack was sealed for another key: none of the keys unlocked here opens it"
}
func (wrongPackKeyError) Is(target error) bool {
	return target == docstore.ErrWrongKey || target == docstore.ErrLocked || target == ErrNoKey
}

// PackSealer returns the two functions a .opendoc pack needs.
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
		if errors.Is(err, ErrNoKey) {
			return nil, ErrWrongPackKey
		}
		if err != nil {
			return nil, err
		}
		return got.Data, nil
	}
	return seal, open
}
