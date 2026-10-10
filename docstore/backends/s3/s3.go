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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Config is the s3/minio backend's own config, decoded from
// Config.BackendConfig when Config.Backend is "s3" or "minio".
// Region and credentials still come from the AWS SDK's own standard
// environment variables/config files (AWS_REGION, AWS_ACCESS_KEY_ID,
// etc.), not from here -- a MinIO deployment needs AWS_REGION set to
// some value even though MinIO itself ignores it (any non-empty
// string works, e.g. "us-east-1"), since the SDK's own request
// signing requires one regardless of which S3-compatible service is
// actually on the other end.
//
// Endpoint/ForcePathStyle are what real AWS never needs and every
// other S3-compatible server (MinIO, R2, ...) does: Endpoint points
// the client at that server instead of AWS's own regional endpoints,
// and ForcePathStyle switches requests from AWS's default virtual-
// hosted addressing (bucket.host/key) to path-style (host/bucket/key)
// -- MinIO doesn't support virtual-hosted style by default, so this
// needs to be on for it. ForcePathStyle is a *bool, not a plain bool,
// specifically so an explicit `"forcePathStyle": false` in
// BackendConfig can be told apart from simply not setting it at all
// -- see resolvePathStyle, which the "minio" backend registration
// below uses to default this on unless a config explicitly turns it
// off, while "s3" keeps defaulting it off unless explicitly turned on.
type Config struct {
	Bucket         string `json:"bucket"`
	Prefix         string `json:"prefix"`
	Endpoint       string `json:"endpoint"`
	ForcePathStyle *bool  `json:"forcePathStyle"`
}

// resolvePathStyle is the one real decision newS3AdapterFactory makes
// before handing off to the AWS SDK: cfg's own explicit choice always
// wins; absent that, backendDefault applies (true for "minio", false
// for "s3" -- see Config's own doc comment). Pulled out as a pure
// function so this one piece of actual logic has a plain native test,
// rather than needing a real *s3.Client (or a live MinIO/AWS
// endpoint) just to exercise a two-way branch.
func resolvePathStyle(cfg Config, backendDefault bool) bool {
	if cfg.ForcePathStyle != nil {
		return *cfg.ForcePathStyle
	}
	return backendDefault
}

// newS3AdapterFactory builds the docstore.AdapterFactory registered for both
// "s3" and "minio" -- identical in every way except which way
// resolvePathStyle defaults, since both are just Adapter talking to
// *s3.Client with different Options, not two different
// implementations.
func newS3AdapterFactory(backendPathStyleDefault bool) docstore.AdapterFactory {
	return func(ctx context.Context, raw json.RawMessage) (docstore.Adapter, error) {
		var cfg Config
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.Bucket == "" {
			return nil, errors.New(`docstore: s3/minio backend requires a "bucket"`)
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, err
		}
		pathStyle := resolvePathStyle(cfg, backendPathStyleDefault)
		client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if cfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(cfg.Endpoint)
			}
			o.UsePathStyle = pathStyle
		})
		return New(client, cfg.Bucket, cfg.Prefix), nil
	}
}

func init() {
	docstore.RegisterAdapter("s3", newS3AdapterFactory(false))
	// "minio" is the exact same adapter/config shape as "s3" --
	// registered separately only so a deployer pointing at a MinIO
	// instance gets ForcePathStyle defaulting to what MinIO actually
	// needs (see Config's own doc comment) without having to know
	// that MinIO-vs-AWS distinction exists at all, rather than having
	// to spell out `"forcePathStyle": true` by hand every time.
	docstore.RegisterAdapter("minio", newS3AdapterFactory(true))
}

// s3API is the slice of the AWS SDK's S3 client this package actually
// calls. Depending on this narrow interface instead of *s3.Client
// lets tests substitute an in-memory fake instead of a live bucket.
type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// Adapter stores each document as one object, keyed directly by
// document ID (optionally under Prefix), in a single S3 (or
// S3-compatible: MinIO, R2, etc.) bucket.
type Adapter struct {
	client s3API
	bucket string
	prefix string
}

// New wraps an already-configured *s3.Client. Building the
// client (region, credentials, custom endpoint for S3-compatible
// services) is the caller's concern -- this package only ever calls
// the four operations in s3API.
func New(client *s3.Client, bucket, prefix string) *Adapter {
	return newAdapter(client, bucket, prefix)
}

func newAdapter(client s3API, bucket, prefix string) *Adapter {
	return &Adapter{client: client, bucket: bucket, prefix: prefix}
}

func (a *Adapter) key(id string) string {
	return a.prefix + id
}

func (a *Adapter) Save(ctx context.Context, id string, data []byte) error {
	_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(id)),
		Body:   bytes.NewReader(data),
	})
	return err
}

// SaveIf implements docstore.ConditionalAdapter with S3's conditional writes: a new
// document is written with If-None-Match: * (refused when the key exists), and a changed
// one with If-Match: the ETag of the object whose bytes were just read and found to be
// what prevDigest says, so the write only lands if nothing replaced it in between. A
// refused condition (412 PreconditionFailed, or 409 ConditionalRequestConflict while
// another write to the key is in flight) is docstore.ErrConflict.
func (a *Adapter) SaveIf(ctx context.Context, id string, data []byte, prevDigest string) error {
	in := &s3.PutObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(a.key(id)), Body: bytes.NewReader(data)}
	if prevDigest == "" {
		in.IfNoneMatch = aws.String("*")
	} else {
		out, err := a.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(a.key(id))})
		if isS3NotFound(err) {
			return docstore.ErrConflict
		}
		if err != nil {
			return err
		}
		cur, err := io.ReadAll(out.Body)
		_ = out.Body.Close()
		if err != nil {
			return err
		}
		if docstore.Digest(cur) != prevDigest || out.ETag == nil {
			return docstore.ErrConflict
		}
		in.IfMatch = out.ETag
	}
	if _, err := a.client.PutObject(ctx, in); err != nil {
		if isS3Conflict(err) {
			return docstore.ErrConflict
		}
		return err
	}
	return nil
}

// DeleteIf implements docstore.ConditionalDeleter with S3's conditional delete: the object is read,
// found to be what prevDigest says, and deleted with If-Match: the ETag of the bytes just read, so the
// delete only lands if nothing replaced the object in between (412 PreconditionFailed is
// docstore.ErrConflict). An object that is not there is docstore.ErrNotFound. A store that ignores
// If-Match on DeleteObject would delete whatever is there: the S3 service and MinIO honor it, and
// an S3-compatible service that does not should not be used for a document expiry that must keep a
// changed document.
func (a *Adapter) DeleteIf(ctx context.Context, id string, prevDigest string) error {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(a.key(id))})
	if isS3NotFound(err) {
		return docstore.ErrNotFound
	}
	if err != nil {
		return err
	}
	cur, err := io.ReadAll(out.Body)
	_ = out.Body.Close()
	if err != nil {
		return err
	}
	if prevDigest == "" || docstore.Digest(cur) != prevDigest || out.ETag == nil {
		return docstore.ErrConflict
	}
	if _, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(a.key(id)), IfMatch: out.ETag}); err != nil {
		switch {
		case isS3Conflict(err):
			return docstore.ErrConflict
		case isS3NotFound(err):
			return docstore.ErrNotFound
		}
		return err
	}
	return nil
}

func isS3Conflict(err error) bool {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict":
			return true
		}
	}
	return false
}

func (a *Adapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(id)),
	})
	if isS3NotFound(err) {
		return docstore.Document{}, docstore.ErrNotFound
	}
	if err != nil {
		return docstore.Document{}, err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return docstore.Document{}, err
	}
	updatedAt := time.Now()
	if out.LastModified != nil {
		updatedAt = *out.LastModified
	}
	return docstore.Document{ID: id, Data: data, UpdatedAt: updatedAt}, nil
}

// LoadHead implements docstore.HeadReader: a ranged GET of the first n bytes. An empty object
// has no byte range to ask for (the service answers InvalidRange), so it is read whole, which is
// free.
func (a *Adapter) LoadHead(ctx context.Context, id string, n int) ([]byte, error) {
	if n <= 0 {
		return []byte{}, nil
	}
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(id)),
		Range:  aws.String(fmt.Sprintf("bytes=0-%d", n-1)),
	})
	if isS3NotFound(err) {
		return nil, docstore.ErrNotFound
	}
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "InvalidRange" {
		d, lerr := a.Load(ctx, id)
		if lerr != nil {
			return nil, lerr
		}
		return d.Data[:min(n, len(d.Data))], nil
	}
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(io.LimitReader(out.Body, int64(n)))
}

func (a *Adapter) List(ctx context.Context) ([]docstore.Meta, error) {
	return a.ListPrefix(ctx, "")
}

// ListPrefix implements docstore.PrefixLister: ListObjectsV2 with Prefix set to the adapter's
// prefix followed by prefix, so the service filters (and pages) the keys.
func (a *Adapter) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	var metas []docstore.Meta
	var token *string
	for {
		out, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(a.bucket),
			Prefix:            aws.String(a.prefix + prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			id := (*obj.Key)[len(a.prefix):]
			meta := docstore.Meta{ID: id, Size: int(aws.ToInt64(obj.Size))}
			if obj.LastModified != nil {
				meta.UpdatedAt = *obj.LastModified
			}
			metas = append(metas, meta)
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		token = out.NextContinuationToken
	}
	return metas, nil
}

func (a *Adapter) Delete(ctx context.Context, id string) error {
	if _, err := a.Load(ctx, id); err != nil {
		return err
	}
	_, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(id)),
	})
	return err
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var nf *types.NotFound
	return errors.As(err, &nf)
}
