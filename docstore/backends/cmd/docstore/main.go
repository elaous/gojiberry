// Command docstore is the document persistence server with every backend linked in:
// in-memory, local files, SQLite, a Go CDK blob bucket, S3 and MinIO, MongoDB and
// PostgreSQL. The backend is chosen by GOJIBERRY_DOCSTORE_BACKEND; see docstore.Config.
// (The plain cmd/docstore of the main module has only the backends that need no
// cloud or database client.)
package main

import (
	_ "github.com/elaous/gojiberry/docstore/backends/all"
	"github.com/elaous/gojiberry/internal/docstorecmd"
)

func main() { docstorecmd.Main() }
