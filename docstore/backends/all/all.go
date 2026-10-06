// Package all registers every backend of the docstore server that needs a cloud or
// database client: "blob" (Go CDK), "s3" and "minio", "mongo" and "postgres". Import it
// for its side effects in a server binary; a browser or client build must not, it links
// those SDKs.
package all

import (
	_ "github.com/elaous/gojiberry/docstore/backends/blob"
	_ "github.com/elaous/gojiberry/docstore/backends/mongo"
	_ "github.com/elaous/gojiberry/docstore/backends/postgres"
	_ "github.com/elaous/gojiberry/docstore/backends/s3"
)
