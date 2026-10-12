package docsearch

import (
	"context"
	"errors"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func TestSubsetShowsOnlyItsKeys(t *testing.T) {
	ctx := context.Background()
	base := docstore.NewMemoryAdapter()
	for _, id := range []string{"a~1.json", "b~1.json", "versions/a~0.json", "catalog:index"} {
		if err := base.Save(ctx, id, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	s := Subset(base, []string{"a~1.json", "b~1.json", "missing.json"})
	metas, err := s.List(ctx)
	if err != nil || len(metas) != 2 {
		t.Fatalf("list: %v, %v", metas, err)
	}
	if _, err := s.Load(ctx, "versions/a~0.json"); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("loaded a key outside the subset: %v", err)
	}
	if err := s.Save(ctx, "catalog:index", []byte("x")); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("wrote a key outside the subset: %v", err)
	}
	if err := s.Delete(ctx, "versions/a~0.json"); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("deleted a key outside the subset: %v", err)
	}
	if d, _ := base.Load(ctx, "catalog:index"); string(d.Data) != "catalog:index" {
		t.Error("the catalog was changed")
	}
	if err := s.Save(ctx, "a~1.json", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Load(ctx, "a~1.json"); string(d.Data) != "new" {
		t.Errorf("save inside the subset: %q", d.Data)
	}
}
