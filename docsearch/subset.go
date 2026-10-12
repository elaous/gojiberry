package docsearch

import (
	"context"
	"slices"

	"github.com/elaous/gojiberry/docstore"
)

// Subset is a view of store that holds only the given keys: List names just those (that exist), Load and
// Save work for them, and any other key is not found. The saver keeps old versions, a catalog and other
// bookkeeping in the same store as the documents; searching or replacing through a Subset of the
// documents' own keys cannot touch those.
func Subset(store docstore.Adapter, keys []string) docstore.Adapter {
	return subset{store: store, keys: slices.Clone(keys)}
}

type subset struct {
	store docstore.Adapter
	keys  []string
}

func (s subset) allowed(id string) bool { return slices.Contains(s.keys, id) }

func (s subset) List(ctx context.Context) ([]docstore.Meta, error) {
	all, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []docstore.Meta
	for _, m := range all {
		if s.allowed(m.ID) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s subset) Load(ctx context.Context, id string) (docstore.Document, error) {
	if !s.allowed(id) {
		return docstore.Document{}, docstore.ErrNotFound
	}
	return s.store.Load(ctx, id)
}

func (s subset) Save(ctx context.Context, id string, data []byte) error {
	if !s.allowed(id) {
		return docstore.ErrNotFound
	}
	return s.store.Save(ctx, id, data)
}

func (s subset) Delete(ctx context.Context, id string) error {
	if !s.allowed(id) {
		return docstore.ErrNotFound
	}
	return s.store.Delete(ctx, id)
}
