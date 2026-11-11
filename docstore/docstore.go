package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned for a missing document.
var ErrNotFound = errors.New("docstore: document not found")

// ErrLocked is returned when a document is held elsewhere.
var ErrLocked = errors.New("docstore: the document is locked: no key here opens it")

var ErrWrongKey = errors.New("docstore: sealed for another key")

// ErrDowngrade is returned when an encrypted document would be downgraded.
var ErrDowngrade = errors.New("docstore: the document's protection is lower than it was (its record was deleted or an older one put back, or a file was forged); the owner can restore it with Sharing.Recover, or finish a cut-short switch with Sharing.Repair")

// ErrStorageFull is wrapped in errors when storage is full.
var ErrStorageFull = errors.New("docstore: storage full")

// ErrPermission is returned, wrapped, when access is denied.
var ErrPermission = errors.New("docstore: permission needed")

type Document struct {
	ID        string
	Data      []byte
	UpdatedAt time.Time
}

// Meta is a Document's identity and bookkeeping.
type Meta struct {
	ID        string
	UpdatedAt time.Time
	Size      int
}

// Adapter is the storage contract every backend implements.
type Adapter interface {
	// Save creates or overwrites the document.
	Save(ctx context.Context, id string, data []byte) error
	// Load returns ErrNotFound if id does not exist.
	Load(ctx context.Context, id string) (Document, error)
	// List returns metadata for every stored document.
	List(ctx context.Context) ([]Meta, error)
	// Delete returns ErrNotFound if id does not exist.
	Delete(ctx context.Context, id string) error
}

type Quota struct {
	Used  int64
	Total int64
}

// QuotaReporter is an optional capability an adapter may offer.
type QuotaReporter interface {
	Usage(ctx context.Context) (Quota, error)
}

// PrefixLister is an optional capability to list by key prefix.
type PrefixLister interface {
	ListPrefix(ctx context.Context, prefix string) ([]Meta, error)
}

// ListPrefix lists documents whose ID starts with prefix.
func ListPrefix(ctx context.Context, a Adapter, prefix string) ([]Meta, error) {
	if pl, ok := a.(PrefixLister); ok {
		return pl.ListPrefix(ctx, prefix)
	}
	all, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, m := range all {
		if strings.HasPrefix(m.ID, prefix) {
			out = append(out, m)
		}
	}
	return out, nil
}

// HeadReader is an optional capability to read a document's start.
type HeadReader interface {
	LoadHead(ctx context.Context, id string, n int) ([]byte, error)
}

// LoadHead reads the first n bytes of a document.
func LoadHead(ctx context.Context, a Adapter, id string, n int) (head []byte, whole bool, err error) {
	if hr, ok := a.(HeadReader); ok && n > 0 {
		head, err = hr.LoadHead(ctx, id, n)
		if err != nil {
			return nil, false, err
		}
		if len(head) > n {
			head = head[:n]
		}
		return head, len(head) < n, nil
	}
	d, err := a.Load(ctx, id)
	if err != nil {
		return nil, false, err
	}
	return d.Data, true, nil
}

// AdapterFactory builds an Adapter from a configuration.
type AdapterFactory func(ctx context.Context, raw json.RawMessage) (Adapter, error)

var adapterFactories = map[string]AdapterFactory{}

// RegisterAdapter makes a backend available by name.
func RegisterAdapter(name string, factory AdapterFactory) {
	if _, exists := adapterFactories[name]; exists {
		panic("docstore: adapter already registered: " + name)
	}
	adapterFactories[name] = factory
}

// BuildAdapter constructs the Adapter registered under name.
func BuildAdapter(ctx context.Context, cfg Config) (Adapter, error) {
	name := cfg.Backend
	if name == "" {
		name = "memory"
	}
	factory, ok := adapterFactories[name]
	if !ok {
		return nil, fmt.Errorf("docstore: unknown backend %q (the Postgres, MongoDB, S3/MinIO and blob backends are registered by importing github.com/elaous/gojiberry/docstore/backends/all)", name)
	}
	return factory(ctx, cfg.BackendConfig)
}
