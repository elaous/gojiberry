package docstore

import (
	"context"
	"errors"
	"testing"
)

// CheckAdapter runs the same behavioral contract against
// any Adapter implementation, so every backend (Memory, LocalFS,
// Postgres, MongoDB, S3) is held to identical semantics without
// duplicating the assertions per-file.
func CheckAdapter(t *testing.T, newAdapter func() Adapter) {
	t.Helper()
	ctx := context.Background()

	t.Run("load missing returns ErrNotFound", func(t *testing.T) {
		a := newAdapter()
		if _, err := a.Load(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Load(missing) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete missing returns ErrNotFound", func(t *testing.T) {
		a := newAdapter()
		if err := a.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(missing) err = %v, want ErrNotFound", err)
		}
	})

	t.Run("save then load round-trips the payload", func(t *testing.T) {
		a := newAdapter()
		want := []byte(`{"blocks":[]}`)
		if err := a.Save(ctx, "doc1", want); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := a.Load(ctx, "doc1")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if string(got.Data) != string(want) {
			t.Fatalf("Load data = %q, want %q", got.Data, want)
		}
		if got.ID != "doc1" {
			t.Fatalf("Load ID = %q, want doc1", got.ID)
		}
		if got.UpdatedAt.IsZero() {
			t.Fatalf("Load UpdatedAt is zero, want a real timestamp")
		}
	})

	t.Run("save overwrites an existing document", func(t *testing.T) {
		a := newAdapter()
		if err := a.Save(ctx, "doc1", []byte("v1")); err != nil {
			t.Fatalf("Save v1: %v", err)
		}
		if err := a.Save(ctx, "doc1", []byte("v2")); err != nil {
			t.Fatalf("Save v2: %v", err)
		}
		got, err := a.Load(ctx, "doc1")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if string(got.Data) != "v2" {
			t.Fatalf("Load data = %q, want v2", got.Data)
		}
	})

	t.Run("list reflects saved and deleted documents", func(t *testing.T) {
		a := newAdapter()
		if err := a.Save(ctx, "a", []byte("aaa")); err != nil {
			t.Fatalf("Save a: %v", err)
		}
		if err := a.Save(ctx, "b", []byte("bb")); err != nil {
			t.Fatalf("Save b: %v", err)
		}
		metas, err := a.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(metas) != 2 {
			t.Fatalf("List returned %d entries, want 2", len(metas))
		}
		sizes := map[string]int{}
		for _, m := range metas {
			sizes[m.ID] = m.Size
		}
		if sizes["a"] != 3 || sizes["b"] != 2 {
			t.Fatalf("List sizes = %v, want a=3 b=2", sizes)
		}

		if err := a.Delete(ctx, "a"); err != nil {
			t.Fatalf("Delete a: %v", err)
		}
		metas, err = a.List(ctx)
		if err != nil {
			t.Fatalf("List after delete: %v", err)
		}
		if len(metas) != 1 || metas[0].ID != "b" {
			t.Fatalf("List after delete = %v, want just [b]", metas)
		}
	})

	t.Run("deleted document is truly gone", func(t *testing.T) {
		a := newAdapter()
		if err := a.Save(ctx, "doc1", []byte("x")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := a.Delete(ctx, "doc1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := a.Load(ctx, "doc1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Load after delete err = %v, want ErrNotFound", err)
		}
	})

	t.Run("empty store lists as empty, not nil-panicking", func(t *testing.T) {
		a := newAdapter()
		metas, err := a.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(metas) != 0 {
			t.Fatalf("List on empty store = %v, want empty", metas)
		}
	})
}
