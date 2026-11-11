package docstore

import (
	"context"
	"errors"
	"sync"
)

// KeyLocker is a named exclusive lock on a key.
type KeyLocker interface {
	LockKey(ctx context.Context, key string) (unlock func(), err error)
}

// MemoryKeyLocker is a KeyLocker of one process.
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

// LockedAdapter makes a store that can lock its writes.
type LockedAdapter struct {
	Adapter
	Locks KeyLocker
}

// NewLockedAdapter wraps inner with the lock.
func NewLockedAdapter(inner Adapter, locks KeyLocker) *LockedAdapter {
	return &LockedAdapter{Adapter: inner, Locks: locks}
}

var (
	_ ConditionalAdapter = (*LockedAdapter)(nil)
	_ ConditionalDeleter = (*LockedAdapter)(nil)
	_ Locker             = (*LockedAdapter)(nil)
)

// with runs fn holding the write lock.
func (a *LockedAdapter) with(ctx context.Context, fn func() error) error {
	unlock, err := a.Locks.LockKey(ctx, "store")
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// Lock implements Locker with exclusive access.
func (a *LockedAdapter) Lock(ctx context.Context) (func(), error) {
	return a.Locks.LockKey(ctx, "exclusive")
}

// Save implements Adapter under the write lock.
func (a *LockedAdapter) Save(ctx context.Context, id string, data []byte) error {
	return a.with(ctx, func() error { return a.Adapter.Save(ctx, id, data) })
}

// Delete implements Adapter under the write lock.
func (a *LockedAdapter) Delete(ctx context.Context, id string) error {
	return a.with(ctx, func() error { return a.Adapter.Delete(ctx, id) })
}

// SaveIf implements ConditionalAdapter under the write lock.
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

// DeleteIf implements ConditionalDeleter under the write lock.
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
