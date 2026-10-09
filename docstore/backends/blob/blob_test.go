//go:build !js

package blob

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/elaous/gojiberry/docstore"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	_ "gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"
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

func TestBlobAdapterHeadReader(t *testing.T) {
	docstore.CheckHeadReader(t, func() docstore.Adapter { return New(newTestBlobBucket(t), "docs/") })
	docstore.CheckHeadReader(t, func() docstore.Adapter { return New(newTestBlobBucket(t), "") })
}

// rangeDriver is a blob driver holding a few objects whose ranged reads of an empty object fail
// the way S3 and GCS do (416 InvalidRange, which gocloud leaves as an unclassified error).
type rangeDriver struct {
	driver.Bucket
	objs map[string][]byte
}

type rangeReader struct{ r *bytes.Reader }

func (r rangeReader) Read(p []byte) (int, error) { return r.r.Read(p) }
func (rangeReader) Close() error                 { return nil }
func (rangeReader) As(any) bool                  { return false }
func (r rangeReader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{Size: r.r.Size()}
}

var errInvalidRange = errors.New("InvalidRange: the requested range is not satisfiable")

func (d rangeDriver) ErrorCode(err error) gcerrors.ErrorCode {
	if errors.Is(err, os.ErrNotExist) {
		return gcerrors.NotFound
	}
	return gcerrors.Unknown
}

func (d rangeDriver) NewRangeReader(ctx context.Context, key string, offset, length int64, _ *driver.ReaderOptions) (driver.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := d.objs[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	if length >= 0 && len(data) == 0 {
		return nil, errInvalidRange
	}
	if length < 0 || offset+length > int64(len(data)) {
		length = int64(len(data)) - offset
	}
	return rangeReader{bytes.NewReader(data[offset : offset+length])}, nil
}

func (d rangeDriver) Close() error { return nil }

func TestBlobAdapterLoadHeadEmptyObjectOnInvalidRange(t *testing.T) {
	ctx := context.Background()
	drv := rangeDriver{objs: map[string][]byte{"p/empty": {}, "p/long": []byte("0123456789")}}
	a := New(blob.NewBucket(drv), "p/")

	head, err := a.LoadHead(ctx, "empty", 8)
	if err != nil || len(head) != 0 {
		t.Fatalf("empty object: %q, %v", head, err)
	}
	// Through the helper the empty head is the whole document.
	head, whole, err := docstore.LoadHead(ctx, a, "empty", 8)
	if err != nil || len(head) != 0 || !whole {
		t.Fatalf("docstore.LoadHead empty: %q whole=%v err=%v", head, whole, err)
	}
	head, whole, err = docstore.LoadHead(ctx, a, "long", 4)
	if err != nil || string(head) != "0123" || whole {
		t.Fatalf("long: %q whole=%v err=%v", head, whole, err)
	}
	if _, err := a.LoadHead(ctx, "missing", 8); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("missing: %v, want ErrNotFound", err)
	}
	// A cancelled context stays a context error and is not retried as a whole read.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.LoadHead(cctx, "long", 4); err == nil {
		t.Fatal("cancelled context: no error")
	}
}
