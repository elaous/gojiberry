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

func TestBuildAdapterPostgresRequiresDSN(t *testing.T) {
	_, err := BuildAdapter(context.Background(), Config{Backend: "postgres"})
	if err == nil {
		t.Fatalf("BuildAdapter(postgres, no config) = nil error, want one requiring dsn")
	}
}

func TestBuildAdapterMongoRequiresURI(t *testing.T) {
	_, err := BuildAdapter(context.Background(), Config{Backend: "mongo"})
	if err == nil {
		t.Fatalf("BuildAdapter(mongo, no config) = nil error, want one requiring uri")
	}
}

func TestBuildAdapterS3RequiresBucket(t *testing.T) {
	_, err := BuildAdapter(context.Background(), Config{Backend: "s3"})
	if err == nil {
		t.Fatalf("BuildAdapter(s3, no config) = nil error, want one requiring bucket")
	}
}

func TestBuildAdapterMinioRequiresBucket(t *testing.T) {
	_, err := BuildAdapter(context.Background(), Config{Backend: "minio"})
	if err == nil {
		t.Fatalf("BuildAdapter(minio, no config) = nil error, want one requiring bucket")
	}
}

func TestBuildAdapterMinioBuildsAnS3Adapter(t *testing.T) {
	cfg := Config{Backend: "minio", BackendConfig: []byte(`{"bucket":"docs","endpoint":"http://localhost:9000"}`)}
	a, err := BuildAdapter(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildAdapter: %v", err)
	}
	if _, ok := a.(*S3Adapter); !ok {
		t.Fatalf("BuildAdapter(minio) = %T, want *S3Adapter -- \"minio\" is the same adapter as \"s3\", just defaulted differently", a)
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
