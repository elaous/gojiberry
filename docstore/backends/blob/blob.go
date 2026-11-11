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

// Config is the blob backend's configuration.
type Config struct {
	URL string `json:"url"`
	// Prefix is prepended to every document key.
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

// Adapter is a docstore.Adapter over a Go CDK bucket.
type Adapter struct {
	bucket *blob.Bucket
	prefix string
}

// New wraps an already-opened *blob.Bucket.
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

// LoadHead implements docstore.HeadReader.
func (a *Adapter) LoadHead(ctx context.Context, id string, n int) ([]byte, error) {
	if n <= 0 {
		return []byte{}, nil
	}
	reader, err := a.bucket.NewRangeReader(ctx, a.key(id), 0, int64(n), nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, docstore.ErrNotFound
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		d, lerr := a.Load(ctx, id)
		if lerr != nil {
			return nil, lerr
		}
		return d.Data[:min(n, len(d.Data))], nil
	}
	defer func() { _ = reader.Close() }()
	return io.ReadAll(io.LimitReader(reader, int64(n)))
}

func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) {
	return a.ListPrefix(ctx, "")
}

// ListPrefix implements docstore.PrefixLister.
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
