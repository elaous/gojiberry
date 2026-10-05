//go:build !js

package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // "file://" scheme
	_ "gocloud.dev/blob/memblob"  // "mem://" scheme
	"gocloud.dev/gcerrors"
)

// BlobConfig is the "blob" backend's own config, decoded from
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
type BlobConfig struct {
	URL string `json:"url"`
	// Prefix is prepended to every document ID before it becomes a
	// blob key, the same role S3Config.Prefix already plays for
	// S3Adapter -- lets several deployments (or several docstore
	// instances) share one bucket without colliding.
	Prefix string `json:"prefix"`
}

func init() {
	RegisterAdapter("blob", func(ctx context.Context, raw json.RawMessage) (Adapter, error) {
		var cfg BlobConfig
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
		return NewBlobAdapter(bucket, cfg.Prefix), nil
	})
}

// BlobAdapter is docstore.Adapter implemented directly on top of a
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
type BlobAdapter struct {
	bucket *blob.Bucket
	prefix string
}

// NewBlobAdapter wraps an already-opened *blob.Bucket. Opening it
// (parsing the URL, loading whichever driver its scheme selects) is
// the caller's concern -- this type only ever calls the bucket's own
// WriteAll/ReadAll/Delete/List operations.
func NewBlobAdapter(bucket *blob.Bucket, prefix string) *BlobAdapter {
	return &BlobAdapter{bucket: bucket, prefix: prefix}
}

func (a *BlobAdapter) key(id string) string {
	return a.prefix + id
}

func (a *BlobAdapter) Save(ctx context.Context, id string, data []byte) error {
	return a.bucket.WriteAll(ctx, a.key(id), data, nil)
}

func (a *BlobAdapter) Load(ctx context.Context, id string) (Document, error) {
	reader, err := a.bucket.NewReader(ctx, a.key(id), nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil {
		return Document{}, err
	}
	return Document{ID: id, Data: data, UpdatedAt: reader.ModTime()}, nil
}

func (a *BlobAdapter) List(ctx context.Context) ([]Meta, error) {
	var metas []Meta
	iter := a.bucket.List(&blob.ListOptions{Prefix: a.prefix})
	for {
		obj, err := iter.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		metas = append(metas, Meta{
			ID:        obj.Key[len(a.prefix):],
			UpdatedAt: obj.ModTime,
			Size:      int(obj.Size),
		})
	}
	return metas, nil
}

func (a *BlobAdapter) Delete(ctx context.Context, id string) error {
	err := a.bucket.Delete(ctx, a.key(id))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return ErrNotFound
	}
	return err
}
