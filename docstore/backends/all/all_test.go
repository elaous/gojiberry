package all_test

import (
	"context"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	_ "github.com/elaous/gojiberry/docstore/backends/all"
	"github.com/elaous/gojiberry/docstore/backends/s3"
)

func TestBuildAdapterPostgresRequiresDSN(t *testing.T) {
	_, err := docstore.BuildAdapter(context.Background(), docstore.Config{Backend: "postgres"})
	if err == nil {
		t.Fatalf("docstore.BuildAdapter(postgres, no config) = nil error, want one requiring dsn")
	}
}

func TestBuildAdapterMongoRequiresURI(t *testing.T) {
	_, err := docstore.BuildAdapter(context.Background(), docstore.Config{Backend: "mongo"})
	if err == nil {
		t.Fatalf("docstore.BuildAdapter(mongo, no config) = nil error, want one requiring uri")
	}
}

func TestBuildAdapterS3RequiresBucket(t *testing.T) {
	_, err := docstore.BuildAdapter(context.Background(), docstore.Config{Backend: "s3"})
	if err == nil {
		t.Fatalf("docstore.BuildAdapter(s3, no config) = nil error, want one requiring bucket")
	}
}

func TestBuildAdapterMinioRequiresBucket(t *testing.T) {
	_, err := docstore.BuildAdapter(context.Background(), docstore.Config{Backend: "minio"})
	if err == nil {
		t.Fatalf("docstore.BuildAdapter(minio, no config) = nil error, want one requiring bucket")
	}
}

func TestBuildAdapterMinioBuildsAnS3Adapter(t *testing.T) {
	cfg := docstore.Config{Backend: "minio", BackendConfig: []byte(`{"bucket":"docs","endpoint":"http://localhost:9000"}`)}
	a, err := docstore.BuildAdapter(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildAdapter: %v", err)
	}
	if _, ok := a.(*s3.Adapter); !ok {
		t.Fatalf("docstore.BuildAdapter(minio) = %T, want *s3.Adapter -- \"minio\" is the same adapter as \"s3\", just defaulted differently", a)
	}
}
