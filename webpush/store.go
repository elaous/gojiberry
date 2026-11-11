package webpush

import (
	"context"
	"sync"
	"time"
)

// Entry is a stored subscription and when it was made.
type Entry struct {
	Subscription Subscription
	Added        time.Time
}

type Store interface {
	Add(ctx context.Context, handle string, sub Subscription) error
	Remove(ctx context.Context, endpoint string) error
	// For lists the handle's subscriptions.
	For(ctx context.Context, handle string) ([]Entry, error)
}

// MaxPerHandle is the number of subscriptions one handle may have.
const MaxPerHandle = 5

type MemStore struct {
	mu  sync.Mutex
	by  map[string][]Entry
	Now func() time.Time
}

// MaxHandles bounds the handles a MemStore keeps.
const MaxHandles = 10000

func (m *MemStore) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Add implements Store.
func (m *MemStore) Add(_ context.Context, handle string, sub Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.by == nil {
		m.by = map[string][]Entry{}
	}
	list := m.by[handle]
	for i, e := range list {
		if e.Subscription.Endpoint == sub.Endpoint {
			list[i] = Entry{Subscription: sub, Added: e.Added}
			return nil
		}
	}
	if len(list) >= MaxPerHandle {
		return ErrTooMany
	}
	if _, ok := m.by[handle]; !ok && len(m.by) >= MaxHandles {
		return ErrTooMany
	}
	m.by[handle] = append(list, Entry{Subscription: sub, Added: m.now()})
	return nil
}

// Remove implements Store.
func (m *MemStore) Remove(_ context.Context, endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for h, list := range m.by {
		kept := list[:0]
		for _, e := range list {
			if e.Subscription.Endpoint != endpoint {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(m.by, h)
		} else {
			m.by[h] = kept
		}
	}
	return nil
}

// For implements Store.
func (m *MemStore) For(_ context.Context, handle string) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Entry(nil), m.by[handle]...), nil
}
