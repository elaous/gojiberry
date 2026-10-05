// Package saver saves and loads documents: a docfile.Document written in
// one of the docfile formats (JSON, protobuf text, protobuf binary) to
// one of several named targets, each a docstore.Adapter -- Postgres,
// SQLite, S3, local files, memory, or an HTTP server in front of any of
// them.
//
// A document is known by its ID, a UUID kept in the document itself; its name is
// only a label, so two documents can have the same name. Each target keeps a
// catalog of its documents (CatalogKey), which is what List reads.
//
// components.Saver is the user interface for it.
package saver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Target is a place documents can be saved to.
type Target struct {
	// Name identifies the target ("postgres"); Label is what a menu shows.
	Name, Label string
	Adapter     docstore.Adapter
	// Capacity is the space allocated to the store in bytes, for Usage to measure
	// against; 0 means the adapter's own answer if it gives one (a local disk does,
	// a database or bucket does not).
	Capacity int64
}

// Saver writes documents to its targets.
type Saver struct {
	Targets []Target
	// Codecs are the formats offered; nil means all of docfile.Formats.
	Codecs []docfile.Codec
	// MaxVersions is how many snapshots SaveVersion keeps of one document; 0
	// means DefaultMaxVersions.
	MaxVersions int

	mu sync.Mutex // one catalog update at a time
}

// Entry is a saved document, as List reports it.
type Entry struct {
	ID      string // the document's UUID: what every method here takes
	Key     string // where the target stores it: the ID and the format's extension
	Name    string // a label; not unique
	Format  docfile.Codec
	Size    int
	Updated time.Time
	Tags    []string
	// Folder is the ID of the folder it is in, "" at the top level.
	Folder string
	// Trashed is when it was moved to the trash; zero for a document that is not.
	Trashed time.Time
}

// FontPrefix starts the store keys of downloaded font files; they are not
// documents.
const FontPrefix = "fonts/"

// ErrName is returned for a name that cannot be used: empty, too long, or with
// control characters.
// ErrBusy is returned when other processes keep changing the store's catalog and an
// update could not get in after several tries.
var ErrBusy = errors.New("saver: the store is busy, try again")

var ErrName = errors.New("saver: the name is empty, too long, or has control characters")

// ErrID is returned for something that is not a document ID.
var ErrID = errors.New("saver: not a document id")

// MaxNameRunes is the longest document name.
const MaxNameRunes = 200

func (s *Saver) codecs() []docfile.Codec {
	if s.Codecs != nil {
		return s.Codecs
	}
	return docfile.Formats()
}

// Target returns the target called name.
func (s *Saver) Target(name string) (Target, error) {
	for _, t := range s.Targets {
		if t.Name == name {
			return t, nil
		}
	}
	return Target{}, fmt.Errorf("saver: no target %q", name)
}

// Codec returns the format called name.
func (s *Saver) Codec(name string) (docfile.Codec, error) {
	for _, c := range s.codecs() {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("saver: no format %q", name)
}

// ValidName reports whether name can label a document.
func ValidName(name string) bool {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// reserved reports whether key is in a keyspace owned by something else
// (document access lists, the shared-editing hub's documents, the catalog,
// version history).
func reserved(key string) bool {
	return strings.HasPrefix(key, "acl:") || strings.HasPrefix(key, "collab:") || strings.HasPrefix(key, "catalog:") ||
		strings.HasPrefix(key, VersionPrefix)
}

func (s *Saver) codecOf(key string) (docfile.Codec, string) {
	for _, c := range s.codecs() {
		if stem, ok := strings.CutSuffix(key, c.Ext()); ok {
			return c, stem
		}
	}
	return nil, ""
}

// Save writes doc to the target in the codec. The document's name is
// doc.Name() (set it with SetName), and its ID doc.ID(): a document with none is
// a new one and gets a fresh UUID; one with an ID replaces the saved document
// with that ID. Saving does not change the document's tags if doc has none of its
// own. It returns the entry as listed.
func (s *Saver) Save(ctx context.Context, target string, c docfile.Codec, doc docfile.Document) (Entry, error) {
	if !ValidName(doc.Name()) {
		return Entry{}, ErrName
	}
	id := doc.ID()
	if id == "" {
		id = docfile.NewID()
	} else if !docfile.ValidID(id) {
		return Entry{}, ErrID
	}
	var out Entry
	err := s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		prev, existed := cat.Docs[id]
		if existed {
			// The catalog is where tags are edited (SetTags); saving the document
			// does not bring back what was removed there.
			doc.SetTags(prev.Tags)
			doc.SetTrashed(prev.Trashed)
			doc.SetFolder(prev.Folder) // moving is Move's job
		} else if _, ok := cat.Folders[doc.Folder()]; !ok {
			doc.SetFolder("") // a new document in a folder that is not there goes to the top
		}
		doc.SetID(id)
		doc.Updated = time.Now().UnixNano()
		data, err := c.Marshal(doc)
		if err != nil {
			return err
		}
		key := id + c.Ext()
		if err := t.Adapter.Save(ctx, key, data); err != nil {
			return err
		}
		if existed && prev.Key != key { // saved in another format: the old file goes, after the catalog
			old := prev.Key
			*later = append(*later, func() { _ = t.Adapter.Delete(ctx, old) })
		}
		cat.Docs[id] = catalogEntry{Key: key, Name: doc.Name(), Format: c.Name(), Size: len(data), Updated: doc.Updated, Tags: doc.Tags(), Trashed: prev.Trashed, Folder: doc.Folder()}
		out = cat.entry(s, id)
		return nil
	})
	return out, err
}

// Load reads a document back by ID.
func (s *Saver) Load(ctx context.Context, target, id string) (docfile.Document, error) {
	var doc docfile.Document
	err := s.view(ctx, target, func(t Target, cat *catalog) error {
		e, ok := cat.Docs[id]
		if !ok {
			return ErrNotFound
		}
		stored, err := t.Adapter.Load(ctx, e.Key)
		if err != nil {
			return err
		}
		c, _ := s.codecOf(e.Key)
		if c == nil {
			c = docfile.Detect(stored.Data)
		}
		doc, err = c.Unmarshal(stored.Data)
		if err != nil {
			return err
		}
		doc.SetID(id)
		doc.SetName(e.Name)
		doc.SetTags(e.Tags)
		doc.SetFolder(e.Folder)
		return nil
	})
	return doc, err
}

// Delete removes a document, its history and its place in the trash for good.
func (s *Saver) Delete(ctx context.Context, target, id string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		e, ok := cat.Docs[id]
		if !ok {
			return ErrNotFound
		}
		delete(cat.Docs, id)
		*later = append(*later, func() {
			_ = t.Adapter.Delete(ctx, e.Key)
			s.dropVersions(ctx, t, id)
		})
		return nil
	})
}

// List returns the documents saved in the target that are not in the trash,
// newest first.
func (s *Saver) List(ctx context.Context, target string) ([]Entry, error) {
	var out []Entry
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		out = cat.sorted(s, false)
		return nil
	})
	return out, err
}

// Lists returns the documents in the target and those in its trash from one read
// of the catalog, so a document is in exactly one of them.
func (s *Saver) Lists(ctx context.Context, target string) (live, trashed []Entry, err error) {
	err = s.view(ctx, target, func(_ Target, cat *catalog) error {
		live, trashed = cat.sorted(s, false), cat.sorted(s, true)
		return nil
	})
	return live, trashed, err
}

// Overview is Lists and the folders, from one read of the catalog.
func (s *Saver) Overview(ctx context.Context, target string) (live, trashed []Entry, folders []Folder, err error) {
	err = s.view(ctx, target, func(_ Target, cat *catalog) error {
		live, trashed, folders = cat.sorted(s, false), cat.sorted(s, true), cat.folderList()
		return nil
	})
	return live, trashed, folders, err
}

// Get returns one document's entry.
func (s *Saver) Get(ctx context.Context, target, id string) (Entry, error) {
	var out Entry
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		if _, ok := cat.Docs[id]; !ok {
			return ErrNotFound
		}
		out = cat.entry(s, id)
		return nil
	})
	return out, err
}

// Build makes a Target from a docstore config, the way a server's
// settings choose a backend ("postgres" with {"dsn": ...}, "sqlite" with
// {"path": ...}, "s3", "localfs", "memory").
func Build(ctx context.Context, name, label string, cfg docstore.Config) (Target, error) {
	a, err := docstore.BuildAdapter(ctx, cfg)
	if err != nil {
		return Target{}, err
	}
	return Target{Name: name, Label: label, Adapter: a}, nil
}
