package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config is the s3 backend's own config, decoded from
// Config.BackendConfig when Config.Backend == "s3". Region and
// credentials come from the AWS SDK's own standard environment
// variables/config files (AWS_REGION, AWS_ACCESS_KEY_ID, etc.), not
// from here -- this only ever holds what's specific to *this*
// deployment's choice of bucket.
type S3Config struct {
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
}

func init() {
	RegisterAdapter("s3", func(ctx context.Context, raw json.RawMessage) (Adapter, error) {
		var cfg S3Config
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
		}
		if cfg.Bucket == "" {
			return nil, errors.New(`docstore: s3 backend requires a "bucket"`)
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, err
		}
		return NewS3Adapter(s3.NewFromConfig(awsCfg), cfg.Bucket, cfg.Prefix), nil
	})
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

// S3Adapter stores each document as one object, keyed directly by
// document ID (optionally under Prefix), in a single S3 (or
// S3-compatible: MinIO, R2, etc.) bucket.
type S3Adapter struct {
	client s3API
	bucket string
	prefix string
}

// NewS3Adapter wraps an already-configured *s3.Client. Building the
// client (region, credentials, custom endpoint for S3-compatible
// services) is the caller's concern -- this package only ever calls
// the four operations in s3API.
func NewS3Adapter(client *s3.Client, bucket, prefix string) *S3Adapter {
	return newS3Adapter(client, bucket, prefix)
}

func newS3Adapter(client s3API, bucket, prefix string) *S3Adapter {
	return &S3Adapter{client: client, bucket: bucket, prefix: prefix}
}

func (a *S3Adapter) key(id string) string {
	return a.prefix + id
}

func (a *S3Adapter) Save(ctx context.Context, id string, data []byte) error {
	_, err := a.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(id)),
		Body:   bytes.NewReader(data),
	})
	return err
}

func (a *S3Adapter) Load(ctx context.Context, id string) (Document, error) {
	out, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(a.key(id)),
	})
	if isS3NotFound(err) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return Document{}, err
	}
	updatedAt := time.Now()
	if out.LastModified != nil {
		updatedAt = *out.LastModified
	}
	return Document{ID: id, Data: data, UpdatedAt: updatedAt}, nil
}

func (a *S3Adapter) List(ctx context.Context) ([]Meta, error) {
	var metas []Meta
	var token *string
	for {
		out, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(a.bucket),
			Prefix:            aws.String(a.prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			id := (*obj.Key)[len(a.prefix):]
			meta := Meta{ID: id, Size: int(aws.ToInt64(obj.Size))}
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

func (a *S3Adapter) Delete(ctx context.Context, id string) error {
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
