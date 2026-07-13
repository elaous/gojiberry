// Package docstore defines a small, storage-agnostic document
// persistence contract (Adapter) plus a handful of concrete
// implementations (Memory, LocalFS, Postgres, MongoDB, S3) and an
// HTTP server that exposes whichever one an operator configures.
//
// A client (e.g. OnDesign's wasm editor, running in a browser that
// cannot itself speak Postgres/MongoDB/S3 wire protocols) never picks
// among these directly. Instead it talks to this package's Server
// over plain HTTP, and the *server* operator is the one who chooses
// the backing Adapter -- so "changing the storage backend" is a
// server-side config change, never a client rebuild.
package docstore

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Adapter.Load and Adapter.Delete when the
// requested document ID has no stored document.
var ErrNotFound = errors.New("docstore: document not found")

// Document is one stored unit: an opaque payload (OnDesign's own
// marshaled document JSON, from the client's point of view) keyed by
// ID, with server-tracked metadata. Adapters must round-trip Data
// byte-for-byte; they must not interpret or reformat it.
type Document struct {
	ID        string
	Data      []byte
	UpdatedAt time.Time
}

// Meta is a Document's identity and bookkeeping fields without its
// (possibly large) payload, for cheap listing.
type Meta struct {
	ID        string
	UpdatedAt time.Time
	Size      int
}

// Adapter is the storage contract every backend implements. All
// methods must be safe for concurrent use.
type Adapter interface {
	// Save creates or overwrites the document with the given ID.
	Save(ctx context.Context, id string, data []byte) error
	// Load returns ErrNotFound if id does not exist.
	Load(ctx context.Context, id string) (Document, error)
	// List returns metadata for every stored document, in no
	// particular order.
	List(ctx context.Context) ([]Meta, error)
	// Delete returns ErrNotFound if id does not exist.
	Delete(ctx context.Context, id string) error
}
