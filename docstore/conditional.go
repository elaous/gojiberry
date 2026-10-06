package docstore

import (
	"context"
	"crypto/rand"
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

// ConditionalDeleter is an optional capability of an Adapter: a delete that only happens if the
// document is still the one the caller read. It is what lets a sweeper remove a record without
// removing a change somebody made between its read and its delete (SweepRateLimits).
type ConditionalDeleter interface {
	Adapter
	// DeleteIf removes the document if its content has digest prev (see Digest). A document
	// that is no longer there is ErrNotFound; one that changed is ErrConflict.
	DeleteIf(ctx context.Context, id string, prev string) error
}

// DeleteIf implements ConditionalDeleter.
func (m *MemoryAdapter) DeleteIf(_ context.Context, id string, prev string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.docs[id]
	if !ok {
		return ErrNotFound
	}
	if prev == "" || Digest(cur.Data) != prev {
		return ErrConflict
	}
	delete(m.docs, id)
	return nil
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

// Locker is an optional capability of an Adapter: exclusive access to the store for
// everything else that shares it (other tabs of the same browser, other processes), for
// as long as the caller holds it. It is for a store that cannot write conditionally
// (ConditionalAdapter): the saver takes the lock around a read-change-write of the
// catalog, so two of them cannot interleave. Lock waits for its turn; the function it
// returns gives the lock back and must be called exactly once.
type Locker interface {
	Lock(ctx context.Context) (unlock func(), err error)
}

// SupportsConditional probes whether store really writes conditionally: it has the SaveIf method
// and the store behind it honours it (a wrapper, an HTTP client of a server on a store that
// cannot, answers ErrConditionalUnsupported). The probe creates a record under
// "directory-log/probe/" twice (the second create must conflict) and deletes it; a store that
// fails to do any of that counts as not supporting it.
func SupportsConditional(ctx context.Context, store Adapter) bool {
	c, ok := store.(ConditionalAdapter)
	if !ok {
		return false
	}
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	id := DirectoryLogPrefix + "probe/" + hex.EncodeToString(raw[:])
	if err := c.SaveIf(ctx, id, []byte("probe"), ""); err != nil {
		return false
	}
	defer func() { _ = store.Delete(ctx, id) }()
	return errors.Is(c.SaveIf(ctx, id, []byte("probe"), ""), ErrConflict)
}
