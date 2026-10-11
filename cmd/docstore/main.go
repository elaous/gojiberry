// Command docstore runs the document persistence HTTP server. The
// backend it stores documents in -- in-memory, local filesystem,
// SQLite, or (with the server in docstore/backends) Postgres, MongoDB, S3 or a Go CDK blob bucket -- is chosen entirely by
// GOJIBERRY_DOCSTORE_BACKEND (and that backend's own env vars); see
// gojiberry/docstore.Config for the full list. This is the server a
// client's "remote" storage adapter talks to, so switching the actual
// storage technology behind a deployment never requires touching the
// client at all.
//
// Two operator commands work on the configured store and exit: `docstore backup <file>` writes every
// stored document, as the store holds it (the ciphertext of an encrypted store, so no key is needed),
// and `docstore restore [-force] <file>` checks the whole file against its manifest before writing
// and refuses a store that already has documents unless -force is given. Each prints a count and an
// audit line (package audit) to stderr, with no document id in either.
package main

import "github.com/elaous/gojiberry/internal/docstorecmd"

func main() { docstorecmd.Main() }
