//go:build !js

package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// fakeS3 is a minimal in-memory stand-in for *s3.Client, implementing
// just the s3API operations Adapter calls. It lets Adapter's
// logic (key prefixing, pagination, not-found translation) be tested
// natively, with no live AWS/MinIO endpoint required.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	etags   map[string]string
	n       int
	ranges  []string // the Range of every GetObject, "" for none
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: make(map[string][]byte), etags: make(map[string]string)}
}

// apiErr is an S3 error with a code, as the SDK's service errors have.
type apiErr struct{ code string }

func (e apiErr) Error() string                 { return e.code }
func (e apiErr) ErrorCode() string             { return e.code }
func (e apiErr) ErrorMessage() string          { return e.code }
func (e apiErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := *in.Key
	_, exists := f.objects[key]
	if in.IfNoneMatch != nil && *in.IfNoneMatch == "*" && exists {
		return nil, apiErr{"PreconditionFailed"}
	}
	if in.IfMatch != nil && (!exists || f.etags[key] != *in.IfMatch) {
		return nil, apiErr{"PreconditionFailed"}
	}
	f.n++
	f.objects[key] = data
	f.etags[key] = fmt.Sprintf("\"etag-%d\"", f.n)
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[*in.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	now := time.Now()
	etag := f.etags[*in.Key]
	f.ranges = append(f.ranges, aws.ToString(in.Range))
	if in.Range != nil {
		var last int
		if _, err := fmt.Sscanf(*in.Range, "bytes=0-%d", &last); err != nil {
			return nil, apiErr{"InvalidArgument"}
		}
		if len(data) == 0 {
			return nil, apiErr{"InvalidRange"} // what S3 answers for a range of an empty object
		}
		if last+1 < len(data) {
			data = data[:last+1]
		}
	}
	return &s3.GetObjectOutput{
		Body:         io.NopCloser(bytes.NewReader(data)),
		LastModified: &now,
		ETag:         &etag,
	}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.IfMatch != nil {
		if _, ok := f.objects[*in.Key]; !ok {
			return nil, apiErr{"NoSuchKey"}
		}
		if f.etags[*in.Key] != *in.IfMatch {
			return nil, apiErr{"PreconditionFailed"}
		}
	}
	delete(f.objects, *in.Key)
	delete(f.etags, *in.Key)
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := aws.ToString(in.Prefix)
	var contents []types.Object
	for key, data := range f.objects {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		size := int64(len(data))
		now := time.Now()
		k := key
		contents = append(contents, types.Object{Key: &k, Size: &size, LastModified: &now})
	}
	falseVal := false
	return &s3.ListObjectsV2Output{Contents: contents, IsTruncated: &falseVal}, nil
}

func TestS3AdapterSaveIf(t *testing.T) {
	docstore.CheckConditional(t, func() docstore.ConditionalAdapter { return newAdapter(newFakeS3(), "test-bucket", "docs/") })
}

// A write another process makes between the read and the write is refused by S3 itself
// (If-Match on the ETag read), not only by the digest compared before.
func TestS3SaveIfRefusesAWriteThatLandedInBetween(t *testing.T) {
	fake := newFakeS3()
	a := newAdapter(fake, "b", "")
	ctx := context.Background()
	_ = a.SaveIf(ctx, "x", []byte("one"), "")
	racer := &racingS3{fakeS3: fake, before: func() { _ = newAdapter(fake, "b", "").Save(ctx, "x", []byte("one")) }} // the same bytes, a new version
	err := newAdapter(racer, "b", "").SaveIf(ctx, "x", []byte("two"), docstore.Digest([]byte("one")))
	if !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("%v", err)
	}
	if d, _ := a.Load(ctx, "x"); string(d.Data) != "one" {
		t.Errorf("%q", d.Data)
	}
}

// DeleteIf removes what was read and refuses what changed, what is gone and a version swapped in
// between the read and the delete (If-Match on the ETag, not only the digest compared before).
func TestS3DeleteIf(t *testing.T) {
	fake := newFakeS3()
	a := newAdapter(fake, "b", "p/")
	ctx := context.Background()
	var _ docstore.ConditionalDeleter = a
	_ = a.Save(ctx, "x", []byte("one"))
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("other"))); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("a changed document: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", ""); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("no digest: %v", err)
	}
	if _, err := a.Load(ctx, "x"); err != nil {
		t.Fatalf("a refused delete removed it: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Load(ctx, "x"); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("still there: %v", err)
	}
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("a document that is gone: %v", err)
	}
	// the same bytes written again between the read and the delete is a new version: kept
	_ = a.Save(ctx, "y", []byte("one"))
	racer := &racingDelete{fakeS3: fake, before: func() { _ = newAdapter(fake, "b", "p/").Save(ctx, "y", []byte("one")) }}
	if err := newAdapter(racer, "b", "p/").DeleteIf(ctx, "y", docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("a version swapped in: %v", err)
	}
	if _, err := a.Load(ctx, "y"); err != nil {
		t.Fatalf("the newer version was deleted: %v", err)
	}
}

// racingDelete runs a write just before the conditional DeleteObject.
type racingDelete struct {
	*fakeS3
	before func()
}

func (r *racingDelete) DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, o ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	if in.IfMatch != nil && r.before != nil && !strings.Contains(*in.Key, "/probe/") {
		r.before()
		r.before = nil
	}
	return r.fakeS3.DeleteObject(ctx, in, o...)
}

// racingS3 runs a write just before the PutObject of a conditional save.
type racingS3 struct {
	*fakeS3
	before func()
}

func (r *racingS3) PutObject(ctx context.Context, in *s3.PutObjectInput, o ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if in.IfMatch != nil && r.before != nil {
		r.before()
		r.before = nil
	}
	return r.fakeS3.PutObject(ctx, in, o...)
}

func TestS3AdapterConformance(t *testing.T) {
	docstore.CheckAdapter(t, func() docstore.Adapter {
		return newAdapter(newFakeS3(), "test-bucket", "docs/")
	})
}

func TestS3AdapterUsesPrefixInKeyButNotInID(t *testing.T) {
	fake := newFakeS3()
	a := newAdapter(fake, "test-bucket", "docs/")
	ctx := context.Background()
	if err := a.Save(ctx, "myid", []byte("x")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, ok := fake.objects["docs/myid"]; !ok {
		t.Fatalf("object stored under key %v, want docs/myid present", fake.objects)
	}
	metas, err := a.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != "myid" {
		t.Fatalf("List = %v, want ID myid (prefix stripped)", metas)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestResolvePathStyleDefaultsApplyWhenUnset(t *testing.T) {
	if got := resolvePathStyle(Config{}, false); got != false {
		t.Errorf("resolvePathStyle({}, false) = %v, want false (the \"s3\" backend's own default)", got)
	}
	if got := resolvePathStyle(Config{}, true); got != true {
		t.Errorf("resolvePathStyle({}, true) = %v, want true (the \"minio\" backend's own default)", got)
	}
}

func TestResolvePathStyleExplicitConfigWins(t *testing.T) {
	if got := resolvePathStyle(Config{ForcePathStyle: boolPtr(false)}, true); got != false {
		t.Errorf("resolvePathStyle({forcePathStyle: false}, true) = %v, want false -- an explicit false must override the \"minio\" backend's own true default", got)
	}
	if got := resolvePathStyle(Config{ForcePathStyle: boolPtr(true)}, false); got != true {
		t.Errorf("resolvePathStyle({forcePathStyle: true}, false) = %v, want true -- an explicit true must override the \"s3\" backend's own false default", got)
	}
}

func TestS3ConfigDecodesEndpointAndForcePathStyle(t *testing.T) {
	var cfg Config
	raw := []byte(`{"bucket":"docs","endpoint":"http://localhost:9000","forcePathStyle":true}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if cfg.Endpoint != "http://localhost:9000" {
		t.Errorf("Endpoint = %q, want http://localhost:9000", cfg.Endpoint)
	}
	if cfg.ForcePathStyle == nil || !*cfg.ForcePathStyle {
		t.Errorf("ForcePathStyle = %v, want a pointer to true", cfg.ForcePathStyle)
	}
}

func TestS3AdapterListPrefix(t *testing.T) {
	docstore.CheckPrefixLister(t, func() docstore.Adapter { return newAdapter(newFakeS3(), "test-bucket", "docs/") })
	docstore.CheckPrefixLister(t, func() docstore.Adapter { return newAdapter(newFakeS3(), "test-bucket", "") })
}

// ListPrefix asks the service to filter: the request carries the adapter's prefix followed by
// the caller's, and a truncated listing is followed to its end.
type pagingS3 struct {
	*fakeS3
	prefixes []string
}

func (p *pagingS3) ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, o ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	p.prefixes = append(p.prefixes, aws.ToString(in.Prefix))
	out, err := p.fakeS3.ListObjectsV2(ctx, in, o...)
	if err != nil || len(out.Contents) < 2 {
		return out, err
	}
	sort.Slice(out.Contents, func(i, j int) bool { return *out.Contents[i].Key < *out.Contents[j].Key })
	if in.ContinuationToken == nil { // first page: one object and a token for the rest
		tok, truncated := "t", true
		return &s3.ListObjectsV2Output{Contents: out.Contents[:1], IsTruncated: &truncated, NextContinuationToken: &tok}, nil
	}
	return &s3.ListObjectsV2Output{Contents: out.Contents[1:]}, nil
}

func TestS3ListPrefixSendsThePrefixAndFollowsPages(t *testing.T) {
	fake := newFakeS3()
	p := &pagingS3{fakeS3: fake}
	a := newAdapter(p, "b", "docs/")
	ctx := context.Background()
	for _, id := range []string{"directory/a/1", "directory/a/2", "directory/b/1"} {
		_ = a.Save(ctx, id, []byte(id))
	}
	got, err := a.ListPrefix(ctx, "directory/a/")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if len(p.prefixes) != 2 || p.prefixes[0] != "docs/directory/a/" || p.prefixes[1] != "docs/directory/a/" {
		t.Errorf("prefixes sent: %v", p.prefixes)
	}
}

func TestS3AdapterHeadReader(t *testing.T) {
	docstore.CheckHeadReader(t, func() docstore.Adapter { return newAdapter(newFakeS3(), "test-bucket", "docs/") })
	docstore.CheckHeadReader(t, func() docstore.Adapter { return newAdapter(newFakeS3(), "test-bucket", "") })
}

// LoadHead asks the service for a byte range, not the object.
func TestS3LoadHeadSendsARange(t *testing.T) {
	fake := newFakeS3()
	a := newAdapter(fake, "b", "docs/")
	ctx := context.Background()
	_ = a.Save(ctx, "d", make([]byte, 1000))
	got, err := a.LoadHead(ctx, "d", 100)
	if err != nil || len(got) != 100 {
		t.Fatalf("%d %v", len(got), err)
	}
	if len(fake.ranges) != 1 || fake.ranges[0] != "bytes=0-99" {
		t.Errorf("ranges sent: %q", fake.ranges)
	}
}

// ignoresIfMatch is a service that does not know If-Match on DeleteObject: it deletes whatever is there.
type ignoresIfMatch struct{ *fakeS3 }

func (r *ignoresIfMatch) DeleteObject(ctx context.Context, in *s3.DeleteObjectInput, o ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	c := *in
	c.IfMatch = nil
	return r.fakeS3.DeleteObject(ctx, &c, o...)
}

// A service that ignores If-Match on delete is found out by the first DeleteIf, which then declines for
// good (ErrConditionalUnsupported) and deletes nothing; the probe object does not stay.
func TestS3DeleteIfDeclinesOnAServiceThatIgnoresIfMatch(t *testing.T) {
	fake := newFakeS3()
	a := newAdapter(&ignoresIfMatch{fake}, "b", "p/")
	ctx := context.Background()
	_ = a.Save(ctx, "x", []byte("one"))
	for range 2 {
		if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrConditionalUnsupported) {
			t.Fatalf("%v", err)
		}
	}
	if _, err := a.Load(ctx, "x"); err != nil {
		t.Fatalf("DeleteIf deleted on a service it cannot trust: %v", err)
	}
	if metas, _ := a.List(ctx); len(metas) != 1 {
		t.Errorf("the probe object stayed: %v", metas)
	}
}

// A probe that cannot run (no permission to write) is an error, not an answer: it is asked again next time.
func TestS3DeleteIfProbeFailureIsNotRemembered(t *testing.T) {
	fake := newFakeS3()
	failing := &failPut{fakeS3: fake, fail: true}
	a := newAdapter(failing, "b", "p/")
	ctx := context.Background()
	_ = fake.putDirect("p/x", []byte("one"))
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); err == nil || errors.Is(err, docstore.ErrConditionalUnsupported) {
		t.Fatalf("%v", err)
	}
	failing.fail = false
	if err := a.DeleteIf(ctx, "x", docstore.Digest([]byte("one"))); err != nil {
		t.Fatalf("the probe was not asked again: %v", err)
	}
}

type failPut struct {
	*fakeS3
	fail bool
}

func (f *failPut) PutObject(ctx context.Context, in *s3.PutObjectInput, o ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.fail {
		return nil, apiErr{"AccessDenied"}
	}
	return f.fakeS3.PutObject(ctx, in, o...)
}

func (f *fakeS3) putDirect(key string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = data
	f.etags[key] = `"` + docstore.Digest(data)[:16] + `"`
	return nil
}
