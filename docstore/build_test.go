//go:build !js

package docstore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBuildAdapterDefaultsToMemory(t *testing.T) {
	a, err := BuildAdapter(context.Background(), Config{})
	if err != nil {
		t.Fatalf("BuildAdapter: %v", err)
	}
	if _, ok := a.(*MemoryAdapter); !ok {
		t.Fatalf("BuildAdapter{} = %T, want *MemoryAdapter", a)
	}
}

func TestBuildAdapterMemoryExplicit(t *testing.T) {
	a, err := BuildAdapter(context.Background(), Config{Backend: "memory"})
	if err != nil {
		t.Fatalf("BuildAdapter: %v", err)
	}
	if _, ok := a.(*MemoryAdapter); !ok {
		t.Fatalf("BuildAdapter = %T, want *MemoryAdapter", a)
	}
}

func TestBuildAdapterLocalFSDefaultsDir(t *testing.T) {
	dir := t.TempDir() + "/sub"
	cfg := Config{Backend: "localfs", BackendConfig: []byte(`{"dir":"` + dir + `"}`)}
	a, err := BuildAdapter(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildAdapter: %v", err)
	}
	if _, ok := a.(*LocalFSAdapter); !ok {
		t.Fatalf("BuildAdapter = %T, want *LocalFSAdapter", a)
	}
}

func TestBuildAdapterUnknownBackend(t *testing.T) {
	_, err := BuildAdapter(context.Background(), Config{Backend: "nope"})
	if err == nil {
		t.Fatalf("BuildAdapter(unknown backend) = nil error, want one")
	}
}

func TestBuildAdapterInvalidJSONConfig(t *testing.T) {
	_, err := BuildAdapter(context.Background(), Config{Backend: "localfs", BackendConfig: []byte(`{not json`)})
	if err == nil {
		t.Fatalf("BuildAdapter(malformed BackendConfig) = nil error, want one")
	}
}

func TestRegisterAdapterPanicsOnDuplicateName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("RegisterAdapter(duplicate name) did not panic")
		}
	}()
	RegisterAdapter("memory", func(context.Context, json.RawMessage) (Adapter, error) { return nil, nil })
}
