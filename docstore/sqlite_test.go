//go:build !js

package docstore

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSQLiteAdapterConformance(t *testing.T) {
	testAdapterConformance(t, func() Adapter {
		a, err := NewSQLiteAdapter(context.Background(), filepath.Join(t.TempDir(), "d.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	})
}

func TestSQLiteBuildAdapter(t *testing.T) {
	ctx := context.Background()
	if _, err := BuildAdapter(ctx, Config{Backend: "sqlite"}); err == nil {
		t.Fatal("sqlite needs a path")
	}
	if _, err := BuildAdapter(ctx, Config{Backend: "sqlite", BackendConfig: []byte(`{`)}); err == nil {
		t.Fatal("bad config")
	}
	a, err := BuildAdapter(ctx, Config{Backend: "sqlite", BackendConfig: []byte(`{"path":":memory:"}`)})
	if err != nil {
		t.Fatal(err)
	}
	s := a.(*SQLiteAdapter)
	if err := s.Save(ctx, "x", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSQLiteAdapter(ctx, filepath.Join(t.TempDir(), "no", "such", "dir.db")); err == nil {
		t.Fatal("unopenable path")
	}
}
