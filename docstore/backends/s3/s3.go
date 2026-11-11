//go:build !js

package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Config is the s3 and minio backend's configuration.
type Config struct {
	Bucket         string `json:"bucket"`
	Prefix         string `json:"prefix"`
	Endpoint       string `json:"endpoint"`
	ForcePathStyle *bool  `json:"forcePathStyle"`
}

func resolvePathStyle(cfg Config, backendDefault bool) bool {
	if cfg.ForcePathStyle != nil {
		return *cfg.ForcePathStyle
	}
	return backendDefault
}

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
	docstore.RegisterAdapter("minio", newS3AdapterFactory(true))
}

type s3API interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// Adapter stores each document as one object.
type Adapter struct {
	client s3API
	bucket string
	prefix string

	probeMu  sync.Mutex
	deleteIf int
}

// New wraps an already-configured *s3.Client.
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

// SaveIf implements docstore.ConditionalAdapter.
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

// DeleteIf implements docstore.ConditionalDeleter.
func (a *Adapter) DeleteIf(ctx context.Context, id string, prevDigest string) error {
	switch ok, err := a.honoursDeleteIf(ctx); {
	case err != nil:
		return err
	case !ok:
		return docstore.ErrConditionalUnsupported
	}
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

func (a *Adapter) honoursDeleteIf(ctx context.Context) (bool, error) {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	if a.deleteIf != 0 {
		return a.deleteIf == 1, nil
	}
	ok, err := a.probeDeleteIf(ctx)
	if err != nil {
		return false, err
	}
	a.deleteIf = 2
	if ok {
		a.deleteIf = 1
	} else {
		log.Printf("docstore: the S3-compatible service at bucket %q ignores If-Match on DeleteObject: conditional deletes are off for it (a delete cannot be tied to the version that was read); use a service that honours it for the best protection of a document renewed while it is being expired", a.bucket)
	}
	return ok, nil
}

func (a *Adapter) probeDeleteIf(ctx context.Context) (bool, error) {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	key := a.key(docstore.DirectoryLogPrefix + "probe/delete-if-" + hex.EncodeToString(raw[:]))
	if _, err := a.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(key), Body: bytes.NewReader([]byte("probe"))}); err != nil {
		return false, fmt.Errorf("s3: probing conditional delete: %w", err)
	}
	defer func() {
		_, _ = a.client.DeleteObject(context.WithoutCancel(ctx), &s3.DeleteObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(key)})
	}()
	_, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(key), IfMatch: aws.String(`"gojiberry-no-such-etag"`)})
	if err != nil && !isS3Conflict(err) && !isS3NotFound(err) {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NotImplemented" {
			return false, nil
		}
		return false, fmt.Errorf("s3: probing conditional delete: %w", err)
	}
	_, gerr := a.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(a.bucket), Key: aws.String(key)})
	switch {
	case gerr == nil:
		return true, nil // still there: the condition was honoured
	case isS3NotFound(gerr):
		return false, nil // gone: the condition was ignored
	default:
		return false, fmt.Errorf("s3: probing conditional delete: %w", gerr)
	}
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

// LoadHead implements docstore.HeadReader with a range read.
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

// ListPrefix implements docstore.PrefixLister.
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
