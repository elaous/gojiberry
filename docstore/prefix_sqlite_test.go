//go:build !js

package docstore

import (
	"context"
	"path/filepath"
	"testing"
)

func sqliteForTest(t *testing.T) Adapter {
	t.Helper()
	a, err := NewSQLiteAdapter(context.Background(), filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
