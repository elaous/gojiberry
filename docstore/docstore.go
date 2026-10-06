// Package docstore defines a small, storage-agnostic document
// persistence contract (Adapter) plus a handful of concrete
// implementations (Memory, LocalFS, Postgres, MongoDB, S3 -- the
// "minio" backend is the same S3Adapter, registered a second time
// under a name that defaults its own config for MinIO instead of
// real AWS, see s3.go's own doc comment) and an HTTP server that
// exposes whichever one an operator configures.
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
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned by Adapter.Load and Adapter.Delete when the
// requested document ID has no stored document.
var ErrNotFound = errors.New("docstore: document not found")

// ErrStorageFull is wrapped in the error of a Save that failed because the store has no
// room (a browser's quota, a full disk). A caller may free what it can do without and try
// again.
var ErrStorageFull = errors.New("docstore: storage full")

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

// Quota is one backend's own answer to "how much space is left" --
// Used/Total in bytes. Not every backend can answer this the same
// way (a local disk has a real, checkable free-space number; an S3
// bucket's own "quota" is usually a billing-tier concept this package
// has no way to query at all), so QuotaReporter below is optional,
// not part of Adapter itself -- see its own doc comment.
type Quota struct {
	Used  int64
	Total int64
}

// QuotaReporter is an optional capability an Adapter can implement if
// (and only if) it has a real, checkable notion of "how much space is
// left" -- LocalFSAdapter does (the underlying disk's own free
// space), Postgres/MongoDB/S3/Memory don't have anything this package
// can query generically, so they simply don't implement it. Server
// checks `adapter.(QuotaReporter)` (server.go's own handleQuota) and
// reports "not available" rather than a wrong or made-up number when
// an adapter doesn't. Mirrors this package's own "narrow, optional-
// feature interface" shape rather than forcing every Adapter to
// answer a question some of them structurally can't.
type QuotaReporter interface {
	Usage(ctx context.Context) (Quota, error)
}

// AdapterFactory builds an Adapter from that backend's own
// JSON-encoded config (raw is nil/empty when nothing was configured).
// Each backend defines its own config shape and unmarshals raw into
// it itself -- this package never needs to know Postgres takes a
// "dsn" or Mongo takes a "uri"/"db", so adding a new backend never
// requires touching Config, BuildAdapter, or any other backend's
// code.
type AdapterFactory func(ctx context.Context, raw json.RawMessage) (Adapter, error)

var adapterFactories = map[string]AdapterFactory{}

// RegisterAdapter makes a backend available under name for
// BuildAdapter/Config.Backend to select. Backends call this from
// their own init(), the same self-registration convention
// database/sql.Register or image.RegisterFormat use. Panics on a
// duplicate name -- a programming error (two backends claiming the
// same name), not a runtime condition to recover from.
func RegisterAdapter(name string, factory AdapterFactory) {
	if _, exists := adapterFactories[name]; exists {
		panic("docstore: adapter already registered: " + name)
	}
	adapterFactories[name] = factory
}

// BuildAdapter constructs the Adapter registered under cfg.Backend,
// passing it cfg.BackendConfig to decode itself. "" is an alias for
// "memory", the zero-dependency default.
func BuildAdapter(ctx context.Context, cfg Config) (Adapter, error) {
	name := cfg.Backend
	if name == "" {
		name = "memory"
	}
	factory, ok := adapterFactories[name]
	if !ok {
		return nil, fmt.Errorf("docstore: unknown backend %q", name)
	}
	return factory(ctx, cfg.BackendConfig)
}
