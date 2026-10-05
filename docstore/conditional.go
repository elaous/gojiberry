package docstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ErrConflict is returned by SaveIf when the document is no longer what the
// caller read: somebody else saved it in between.
var ErrConflict = errors.New("docstore: the document changed since it was read")

// ErrConditionalUnsupported is returned by an adapter that has the SaveIf method
// but whose store cannot do it (an HTTP client of a server on a store that can't).
var ErrConditionalUnsupported = errors.New("docstore: conditional save is not supported by this store")

// Digest is the revision of a document's content that SaveIf compares: the hex
// SHA-256 of it.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ConditionalAdapter is an optional capability of an Adapter: a save that only
// happens if the document is still the one the caller read. It is what lets two
// processes share a store without one overwriting the other's change to a record
// both edit (a catalog): read, change, SaveIf with the Digest of what was read,
// and on ErrConflict read again and redo the change.
type ConditionalAdapter interface {
	Adapter
	// SaveIf stores data if the document's content has digest prev (see Digest),
	// or, when prev is "", if there is no such document. Otherwise it stores
	// nothing and returns ErrConflict. The check and the write are one step.
	SaveIf(ctx context.Context, id string, data []byte, prev string) error
}

// SaveIf implements ConditionalAdapter.
func (m *MemoryAdapter) SaveIf(_ context.Context, id string, data []byte, prev string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.docs[id]
	if (prev == "") == ok || ok && Digest(cur.Data) != prev {
		return ErrConflict
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	m.docs[id] = Document{ID: id, Data: cp, UpdatedAt: time.Now()}
	return nil
}
