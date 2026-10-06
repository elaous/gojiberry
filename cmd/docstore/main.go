// Command docstore runs the document persistence HTTP server. The
// backend it stores documents in -- in-memory, local filesystem,
// SQLite, or (with the server in docstore/backends) Postgres, MongoDB, S3 or a Go CDK blob bucket -- is chosen entirely by
// GOJIBERRY_DOCSTORE_BACKEND (and that backend's own env vars); see
// gojiberry/docstore.Config for the full list. This is the server a
// client's "remote" storage adapter talks to, so switching the actual
// storage technology behind a deployment never requires touching the
// client at all.
package main

import "github.com/elaous/gojiberry/internal/docstorecmd"

func main() { docstorecmd.Main() }
