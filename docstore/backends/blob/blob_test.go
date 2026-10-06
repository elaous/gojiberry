//go:build !js

package blob

import (
	"context"
	"github.com/elaous/gojiberry/docstore"
	"testing"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/memblob"
)

// newTestBlobBucket opens a fresh, isolated in-memory *blob.Bucket --
// memblob.OpenBucket takes no name/singleton parameter, so each call
// (via blob.OpenBucket's own "mem://" scheme) starts from an empty
// map, exactly like NewMemoryAdapter's own fresh-map-per-call
// contract every other conformance test here relies on.
func newTestBlobBucket(t *testing.T) *blob.Bucket {
	t.Helper()
	bucket, err := blob.OpenBucket(context.Background(), "mem://")
	if err != nil {
		t.Fatalf("opening mem:// bucket: %v", err)
	}
	t.Cleanup(func() { _ = bucket.Close() })
	return bucket
}

func TestBlobAdapterConformance(t *testing.T) {
	docstore.CheckAdapter(t, func() docstore.Adapter {
		return New(newTestBlobBucket(t), "docs/")
	})
}

func TestBlobAdapterUsesPrefixInKeyButNotInID(t *testing.T) {
	bucket := newTestBlobBucket(t)
	a := New(bucket, "docs/")
	ctx := context.Background()
	if err := a.Save(ctx, "myid", []byte("x")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ok, err := bucket.Exists(ctx, "docs/myid")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !ok {
		t.Fatalf("expected object stored under key docs/myid")
	}
	metas, err := a.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "myid" {
		t.Fatalf("List = %v, want ID myid (prefix stripped)", metas)
	}
}

func TestBuildAdapterBlobBackend(t *testing.T) {
	raw := []byte(`{"url":"mem://","prefix":"x/"}`)
	a, err := docstore.BuildAdapter(context.Background(), docstore.Config{Backend: "blob", BackendConfig: raw})
	if err != nil {
		t.Fatalf("BuildAdapter: %v", err)
	}
	ctx := context.Background()
	if err := a.Save(ctx, "doc1", []byte("hello")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := a.Load(ctx, "doc1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got.Data) != "hello" {
		t.Fatalf("Load data = %q, want hello", got.Data)
	}
}

func TestBuildAdapterBlobBackendRequiresURL(t *testing.T) {
	_, err := docstore.BuildAdapter(context.Background(), docstore.Config{Backend: "blob", BackendConfig: []byte(`{}`)})
	if err == nil {
		t.Fatal("BuildAdapter with no url = nil error, want an error")
	}
}

func TestBlobAdapterListPrefix(t *testing.T) {
	docstore.CheckPrefixLister(t, func() docstore.Adapter { return New(newTestBlobBucket(t), "docs/") })
	docstore.CheckPrefixLister(t, func() docstore.Adapter { return New(newTestBlobBucket(t), "") })
	// The adapter's own prefix scopes the listing: another deployment's keys are not seen.
	bucket := newTestBlobBucket(t)
	ctx := context.Background()
	_ = New(bucket, "other/").Save(ctx, "directory/a/1", []byte("x"))
	a := New(bucket, "docs/")
	_ = a.Save(ctx, "directory/a/2", []byte("x"))
	got, err := a.ListPrefix(ctx, "directory/a/")
	if err != nil || len(got) != 1 || got[0].ID != "directory/a/2" {
		t.Fatalf("%v %v", got, err)
	}
}
