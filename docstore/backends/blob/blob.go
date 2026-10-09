//go:build !js

package blob

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"io"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // "file://" scheme
	_ "gocloud.dev/blob/memblob"  // "mem://" scheme
	"gocloud.dev/gcerrors"
)

// Config is the "blob" backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "blob". URL is a
// gocloud.dev/blob bucket URL -- the scheme picks the driver
// (blank-imported below for "file"/"mem"; a deployer wanting "s3"/
// "gs"/"azblob"/etc. needs their own build that blank-imports the
// matching gocloud.dev/blob/* driver package too, the same way this
// package's own S3Adapter needs the real AWS SDK compiled in --
// go-cloud's own drivers aren't loaded by name at runtime, only by
// which packages a given binary actually imports). See
// https://gocloud.dev/concepts/urls/ for the full URL-scheme
// reference; a few concrete examples: "file:///var/docstore-data",
// "s3://my-bucket?region=us-west-1", "gs://my-bucket",
// "mem://" (in-process only, gone on restart -- this package's own
// MemoryAdapter already covers that case more directly, so "blob"
// with a mem:// URL exists mainly for tests exercising this adapter
// against a real *blob.Bucket without a real cloud credential).
type Config struct {
	URL string `json:"url"`
	// Prefix is prepended to every document ID before it becomes a
	// blob key, the same role S3Config.Prefix already plays for
	// S3Adapter -- lets several deployments (or several docstore
	// instances) share one bucket without colliding.
	Prefix string `json:"prefix"`
}

func init() {
	docstore.RegisterAdapter("blob", func(ctx context.Context, raw json.RawMessage) (docstore.Adapter, error) {
		var cfg Config
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.URL == "" {
			return nil, errors.New(`docstore: blob backend requires a "url"`)
		}
		bucket, err := blob.OpenBucket(ctx, cfg.URL)
		if err != nil {
			return nil, err
		}
		return New(bucket, cfg.Prefix), nil
	})
}

// Adapter is docstore.Adapter implemented directly on top of a
// gocloud.dev/blob *blob.Bucket -- the "genuinely different kind of
// tool" task #174's own roadmap entry settled on go-cloud for: unlike
// this package's own S3Adapter (a hand-written wrapper around the AWS
// SDK) or a hypothetical GCSAdapter that would duplicate most of it,
// this one adapter already speaks every blob storage backend go-cloud
// itself supports (local filesystem, S3, GCS, Azure Blob Storage, and
// more) purely by which URL scheme its own config points at, with no
// backend-specific Go code in this package at all. S3Adapter/
// LocalFSAdapter both stay as their own separate, still-supported
// backends -- this is a new option alongside them, not a replacement,
// since existing deployments configured for "s3"/"localfs" shouldn't
// have to change anything.
type Adapter struct {
	bucket *blob.Bucket
	prefix string
}

// New wraps an already-opened *blob.Bucket. Opening it
// (parsing the URL, loading whichever driver its scheme selects) is
// the caller's concern -- this type only ever calls the bucket's own
// WriteAll/ReadAll/Delete/List operations.
func New(bucket *blob.Bucket, prefix string) *Adapter {
	return &Adapter{bucket: bucket, prefix: prefix}
}

func (a *Adapter) key(id string) string {
	return a.prefix + id
}

func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	return a.bucket.WriteAll(ctx, a.key(id), data, nil)
}

func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	reader, err := a.bucket.NewReader(ctx, a.key(id), nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return docstore.Document{}, docstore.ErrNotFound
	}
	if err != nil {
		return docstore.Document{}, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return docstore.Document{}, err
	}
	return docstore.Document{ID: id, Data: data, UpdatedAt: reader.ModTime()}, nil
}

// LoadHead implements docstore.HeadReader: a ranged read of the first n bytes.
func (a *Adapter) LoadHead(ctx context.Context, id string, n int) ([]byte, error) {
	if n <= 0 {
		return []byte{}, nil
	}
	reader, err := a.bucket.NewRangeReader(ctx, a.key(id), 0, int64(n), nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, docstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(io.LimitReader(reader, int64(n)))
}

func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) {
	return a.ListPrefix(ctx, "")
}

// ListPrefix implements docstore.PrefixLister: the bucket lists only the keys under the
// adapter's prefix followed by prefix.
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	var metas []docstore.Meta
	iter := a.bucket.List(&blob.ListOptions{Prefix: a.prefix + prefix})
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		metas = append(metas, docstore.Meta{
			ID:        obj.Key[len(a.prefix):],
			UpdatedAt: obj.ModTime,
			Size:      int(obj.Size),
		})
	}
	return metas, nil
}

func (a *Adapter) Delete(ctx context.Context, id string) error {
	err := a.bucket.Delete(ctx, a.key(id))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return docstore.ErrNotFound
	}
	return err
}
