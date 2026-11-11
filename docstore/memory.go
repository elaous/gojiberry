package docstore

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

func init() {
	RegisterAdapter("memory", func(_ context.Context, _ json.RawMessage) (Adapter, error) {
		return NewMemoryAdapter(), nil
	})
}

// MemoryAdapter is an in-process, non-persistent Adapter.
type MemoryAdapter struct {
	mu   sync.Mutex
	docs map[string]Document
}

// NewMemoryAdapter returns an empty MemoryAdapter, ready to use.
func NewMemoryAdapter() *MemoryAdapter {
	return &MemoryAdapter{docs: make(map[string]Document)}
}

func (m *MemoryAdapter) Save(_ context.Context, id string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.docs[id] = Document{ID: id, Data: cp, UpdatedAt: time.Now()}
	return nil
}

func (m *MemoryAdapter) Load(_ context.Context, id string) (Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[id]
	if !ok {
		return Document{}, ErrNotFound
	}
	cp := make([]byte, len(doc.Data))
	copy(cp, doc.Data)
	doc.Data = cp
	return doc, nil
}

// LoadHead implements HeadReader.
func (m *MemoryAdapter) LoadHead(_ context.Context, id string, n int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if n > len(doc.Data) {
		n = len(doc.Data)
	}
	return append([]byte(nil), doc.Data[:max(n, 0)]...), nil
}

func (m *MemoryAdapter) List(_ context.Context) ([]Meta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	metas := make([]Meta, 0, len(m.docs))
	for _, doc := range m.docs {
		metas = append(metas, Meta{ID: doc.ID, UpdatedAt: doc.UpdatedAt, Size: len(doc.Data)})
	}
	return metas, nil
}

// ListPrefix implements PrefixLister.
func (m *MemoryAdapter) ListPrefix(_ context.Context, prefix string) ([]Meta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var metas []Meta
	for id, doc := range m.docs {
		if strings.HasPrefix(id, prefix) {
			metas = append(metas, Meta{ID: doc.ID, UpdatedAt: doc.UpdatedAt, Size: len(doc.Data)})
		}
	}
	return metas, nil
}

func (m *MemoryAdapter) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.docs[id]; !ok {
		return ErrNotFound
	}
	delete(m.docs, id)
	return nil
}
