package docstore

import (
	"context"
	"testing"
)

func TestLocalFSAdapterConformance(t *testing.T) {
	testAdapterConformance(t, func() Adapter {
		a, err := NewLocalFSAdapter(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalFSAdapter: %v", err)
		}
		return a
	})
}

func TestLocalFSAdapterCreatesMissingDir(t *testing.T) {
	dir := t.TempDir() + "/nested/does/not/exist/yet"
	a, err := NewLocalFSAdapter(dir)
	if err != nil {
		t.Fatalf("NewLocalFSAdapter: %v", err)
	}
	if err := a.Save(context.Background(), "doc1", []byte("x")); err != nil {
		t.Fatalf("Save into freshly created dir: %v", err)
	}
}
