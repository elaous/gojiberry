package docstore

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BuildAdapter constructs the Adapter named by cfg.Backend, loading
// whatever extra config that backend needs from the rest of cfg. It
// is the one place a new backend name has to be wired in.
func BuildAdapter(ctx context.Context, cfg Config) (Adapter, error) {
	switch cfg.Backend {
	case "", "memory":
		return NewMemoryAdapter(), nil
	case "localfs":
		return NewLocalFSAdapter(cfg.LocalFSDir)
	case "postgres":
		if cfg.PostgresDSN == "" {
			return nil, fmt.Errorf("docstore: backend %q requires GOJIBERRY_DOCSTORE_POSTGRES_DSN", cfg.Backend)
		}
		return NewPostgresAdapter(ctx, cfg.PostgresDSN)
	case "mongo":
		if cfg.MongoURI == "" {
			return nil, fmt.Errorf("docstore: backend %q requires GOJIBERRY_DOCSTORE_MONGO_URI", cfg.Backend)
		}
		return NewMongoAdapter(ctx, cfg.MongoURI, cfg.MongoDB)
	case "s3":
		if cfg.S3Bucket == "" {
			return nil, fmt.Errorf("docstore: backend %q requires GOJIBERRY_DOCSTORE_S3_BUCKET", cfg.Backend)
		}
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, err
		}
		return NewS3Adapter(s3.NewFromConfig(awsCfg), cfg.S3Bucket, cfg.S3Prefix), nil
	default:
		return nil, fmt.Errorf("docstore: unknown backend %q", cfg.Backend)
	}
}
