package docstore

import (
	"context"
	"errors"
	"sync"
)

// KeyLocker is a named exclusive lock shared by every process that uses one store: whatever a
// deployment already has for that (a Redis lock, a database advisory lock, a lock service). The
// library ships no client for any of them; MemoryKeyLocker is the one-process kind. LockKey waits
// for its turn on the key; the function it returns gives the lock back and must be called exactly
// once. A lock held by a process that died must expire on the locker's side, or every writer of
// that key waits for ever.
type KeyLocker interface {
	LockKey(ctx context.Context, key string) (unlock func(), err error)
}

// MemoryKeyLocker is a KeyLocker of one process: named locks that waiters take in turn, and that
// are forgotten when nobody holds or waits for them.
type MemoryKeyLocker struct {
	mu   sync.Mutex
	held map[string]*memKeyLock
}

type memKeyLock struct {
	ch   chan struct{} // holds one token while the lock is free
	refs int
}

// LockKey implements KeyLocker.
func (m *MemoryKeyLocker) LockKey(ctx context.Context, key string) (func(), error) {
	m.mu.Lock()
	if m.held == nil {
		m.held = map[string]*memKeyLock{}
	}
	l := m.held[key]
	if l == nil {
		l = &memKeyLock{ch: make(chan struct{}, 1)}
		l.ch <- struct{}{}
		m.held[key] = l
	}
	l.refs++
	m.mu.Unlock()
	release := func() {
		m.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(m.held, key)
		}
		m.mu.Unlock()
	}
	select {
	case <-l.ch:
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
	return func() {
		l.ch <- struct{}{}
		release()
	}, nil
}

// LockedAdapter makes a store that cannot write conditionally behave as if it could, by taking a
// shared lock around every write: SaveIf and DeleteIf read the document under the lock, compare
// its Digest and write before giving the lock back. It is a ConditionalAdapter, a
// ConditionalDeleter and a Locker (under the key "exclusive", apart from the key every write
// takes, so a holder can write through the adapter), so everything built
// on those (StoreLimiter, the passkey AdapterStore and challenge store, the telemetry totals,
// the saver's catalog) works on it.
//
// The guarantee holds only if every process that writes the store does it through a
// LockedAdapter on the same KeyLocker: a writer that bypasses it can still overwrite between
// the read and the write. A store that offers a real conditional write (SupportsConditional)
// does not need this and is safer without it.
type LockedAdapter struct {
	Adapter
	Locks KeyLocker
}

// NewLockedAdapter wraps inner with the locks.
func NewLockedAdapter(inner Adapter, locks KeyLocker) *LockedAdapter {
	return &LockedAdapter{Adapter: inner, Locks: locks}
}

var (
	_ ConditionalAdapter = (*LockedAdapter)(nil)
	_ ConditionalDeleter = (*LockedAdapter)(nil)
	_ Locker             = (*LockedAdapter)(nil)
)

// with runs fn holding the write lock, the key "store": one write at a time over the whole store
// (a per-document key would let a lock service scale better; one key keeps the guarantee simple).
func (a *LockedAdapter) with(ctx context.Context, fn func() error) error {
	unlock, err := a.Locks.LockKey(ctx, "store")
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// Lock implements Locker: exclusive access for a read-change-write of something larger than one
// document (a catalog), until unlock. It is a different lock from the one each write takes, so
// the holder writes through this adapter without waiting for itself.
func (a *LockedAdapter) Lock(ctx context.Context) (func(), error) {
	return a.Locks.LockKey(ctx, "exclusive")
}

// Save implements Adapter under the lock.
func (a *LockedAdapter) Save(ctx context.Context, id string, data []byte) error {
	return a.with(ctx, func() error { return a.Adapter.Save(ctx, id, data) })
}

// Delete implements Adapter under the lock.
func (a *LockedAdapter) Delete(ctx context.Context, id string) error {
	return a.with(ctx, func() error { return a.Adapter.Delete(ctx, id) })
}

// SaveIf implements ConditionalAdapter.
func (a *LockedAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	return a.with(ctx, func() error {
		cur, err := a.Load(ctx, id)
		switch {
		case errors.Is(err, ErrNotFound):
			if prev != "" {
				return ErrConflict
			}
		case err != nil:
			return err
		case prev == "" || Digest(cur.Data) != prev:
			return ErrConflict
		}
		return a.Adapter.Save(ctx, id, data)
	})
}

// DeleteIf implements ConditionalDeleter.
func (a *LockedAdapter) DeleteIf(ctx context.Context, id string, prev string) error {
	return a.with(ctx, func() error {
		cur, err := a.Load(ctx, id)
		if err != nil {
			return err
		}
		if prev == "" || Digest(cur.Data) != prev {
			return ErrConflict
		}
		return a.Adapter.Delete(ctx, id)
	})
}
