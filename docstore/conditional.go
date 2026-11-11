package docstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ErrConflict is returned by SaveIf when the document changed.
var ErrConflict = errors.New("docstore: the document changed since it was read")

// ErrConditionalUnsupported is returned by stores without conditional writes.
var ErrConditionalUnsupported = errors.New("docstore: conditional save is not supported by this store")

// Digest is the revision of a document's content.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ConditionalAdapter is an optional capability for compare-and-swap saves.
type ConditionalAdapter interface {
	Adapter
	// SaveIf stores data if the document's revision is prev.
	SaveIf(ctx context.Context, id string, data []byte, prev string) error
}

// ConditionalDeleter is an optional capability for compare-and-swap deletes.
type ConditionalDeleter interface {
	Adapter
	// DeleteIf removes the document if its revision is prev.
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

// Locker is an optional capability for locking a document name.
type Locker interface {
	Lock(ctx context.Context) (unlock func(), err error)
}

// SupportsConditional probes whether a store supports conditional writes.
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
