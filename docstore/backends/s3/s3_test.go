//go:build !js

package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeS3 is a minimal in-memory stand-in for *s3.Client, implementing
// just the s3API operations Adapter calls. It lets Adapter's
// logic (key prefixing, pagination, not-found translation) be tested
// natively, with no live AWS/MinIO endpoint required.
type fakeS3 struct {
	objects map[string][]byte
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: make(map[string][]byte)} }

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[*in.Key] = data
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data, ok := f.objects[*in.Key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	now := time.Now()
	return &s3.GetObjectOutput{
		Body:         io.NopCloser(bytes.NewReader(data)),
		LastModified: &now,
	}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(f.objects, *in.Key)
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
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
