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
	// CatalogKey is where the target keeps its catalog; "" means CatalogKey. It must
	// start with "catalog:" and end with "index" (see UserCatalogKey). Give each person
	// their own when several share one store: the default is one catalog for the store.
	CatalogKey string
}

// Saver writes documents to its targets.
type Saver struct {
	Targets []Target
	// Codecs are the formats offered; nil means all of docfile.Formats.
	Codecs []docfile.Codec
	// MaxVersions is how many snapshots SaveVersion keeps of one document; 0
	// means DefaultMaxVersions.
	MaxVersions int
	// PreviousCopies is how many earlier saves of each document are kept beside the
	// current one, to fall back on if it is lost or damaged (see LoadChecked): 0 means
	// DefaultPreviousCopies (1), negative means none. Only one earlier save is kept: any
	// positive value means that one. It takes space, and Usage counts it.
	PreviousCopies int
	// RepairGrace is how long Repair waits, when it finds the store and its catalog
	// disagreeing, before looking again and repairing what is still wrong: another
	// process may be half way through an operation. 0 means DefaultRepairGrace; a
	// negative value means no wait (for tests).
	RepairGrace time.Duration
	// ConfidentialCheck, when set, is called before a document is made end to end
	// protected (SetProtection, or saving a new document that is marked so), and a
	// non-nil error refuses it. It is where a deployment says what a confidential document
	// needs, such as a recovery key besides a passkey (encrypt.ConfidentialCheck). Nil means
	// no check. The saver does not know about keys: it only asks.
	ConfidentialCheck func(ctx context.Context, target, id string) error
	// Rekey, when set, is what makes the files of a document follow its protection level: it
	// is called with the level the document is moving to (ProtectionServer or ProtectionE2EE)
	// and must leave every stored file of the document (current copy, previous copy, every
	// version snapshot) encrypted to that level's recipients, or fail naming what is left
	// (encrypt.RekeyHook; the saver does not import it). It must be idempotent and cheap when
	// the level is already settled, because SetProtection calls it again on a retry. It runs
	// outside the saver's catalog lock (a re-key takes the store's). Nil means none: the
	// store's keyring alone decides, and copies written before a switch keep their wrapping.
	Rekey func(ctx context.Context, target, id, protection string) error

	mu sync.Mutex   // one catalog update at a time
	tm sync.RWMutex // Targets, which AddTarget and RemoveTarget change
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
	// Protection is ProtectionServer or ProtectionE2EE. For an end-to-end document Name is
	// ConfidentialName and Tags is empty: see Reveal.
	Protection string
}

// FontPrefix starts the store keys of downloaded font files; they are not
// documents.
const FontPrefix = "fonts/"

// ErrUnreadable is wrapped in the error of an operation that met a document whose
// file is missing or does not parse.
var ErrUnreadable = errors.New("saver: the document's file is missing or damaged")

// DefaultRepairGrace is the RepairGrace used when none is set.
const DefaultRepairGrace = 2 * time.Second

// ErrBusy is returned when other processes keep changing the store's catalog and an
// update could not get in after several tries.
var ErrBusy = errors.New("saver: the store is busy, try again")

// ErrName is returned for a name that cannot be used: empty, too long, or with
// control characters.
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
	s.tm.RLock()
	defer s.tm.RUnlock()
	for _, t := range s.Targets {
		if t.Name == name {
			return t, nil
		}
	}
	return Target{}, fmt.Errorf("saver: no target %q", name)
}

// TargetList is a copy of the targets, safe to read while AddTarget and RemoveTarget run.
func (s *Saver) TargetList() []Target {
	s.tm.RLock()
	defer s.tm.RUnlock()
	return append([]Target(nil), s.Targets...)
}

// AddTarget adds a place to save to while the program runs (a folder the person has just
// chosen), replacing the one of the same name.
func (s *Saver) AddTarget(t Target) {
	s.tm.Lock()
	defer s.tm.Unlock()
	for i := range s.Targets {
		if s.Targets[i].Name == t.Name {
			s.Targets[i] = t
			return
		}
	}
	s.Targets = append(s.Targets, t)
}

// RemoveTarget takes a place out (nothing stored in it is touched).
func (s *Saver) RemoveTarget(name string) {
	s.tm.Lock()
	defer s.tm.Unlock()
	for i := range s.Targets {
		if s.Targets[i].Name == name {
			s.Targets = append(s.Targets[:i], s.Targets[i+1:]...)
			return
		}
	}
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
	return s.save(ctx, target, c, doc, false)
}

// save is Save; with e2ee the document is written end to end protected in this one step,
// whatever its previous level was: over an ordinary document the ConfidentialCheck, if
// there is one, is consulted first (a refusal leaves the saved document untouched), and the
// catalog and the file never hold the real name and tags as an ordinary document's. The
// document's own tags are the ones kept (they are the new content's).
func (s *Saver) save(ctx context.Context, target string, c docfile.Codec, doc docfile.Document, e2ee bool) (Entry, error) {
	if !ValidName(doc.Name()) {
		return Entry{}, ErrName
	}
	id := doc.ID()
	if id == "" {
		id = docfile.NewID()
	} else if !docfile.ValidID(id) {
		return Entry{}, ErrID
	}
	// A document that carries its history (an imported .open file) keeps it in versions,
	// not in the document file.
	history := doc.History
	doc.History = nil
	// A document that is written end to end here (a new confidential one, an import, a copy)
	// has its files switched to people's keys before the first byte of it is written: the
	// gate runs first, the re-key second, and the in-lock gates below are then not repeated.
	prechecked := false
	want := e2ee
	if s.Rekey != nil && !want && doc.Protection() == ProtectionE2EE {
		_, gerr := s.Get(ctx, target, id)
		want = errors.Is(gerr, ErrNotFound) // only a new document takes its level from the file
	}
	if s.Rekey != nil && want {
		if s.ConfidentialCheck != nil {
			if err := s.ConfidentialCheck(ctx, target, id); err != nil {
				return Entry{}, fmt.Errorf("saver: not saved as confidential: %w", err)
			}
		}
		prechecked = true
		if err := s.Rekey(ctx, target, id, ProtectionE2EE); err != nil {
			return Entry{}, fmt.Errorf("saver: not saved as confidential: %w", err)
		}
	}
	var out Entry
	err := s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		prev, existed := cat.Docs[id]
		protection := prev.Protection
		if existed {
			// The catalog is where tags are edited (SetTags); saving the document
			// does not bring back what was removed there. For an end-to-end document
			// the catalog has none: the saved document's own are kept.
			if e2ee && !prev.confidential() {
				// Becoming confidential by this save: the same gate as SetProtection's.
				if s.ConfidentialCheck != nil && !prechecked {
					if err := s.ConfidentialCheck(ctx, target, id); err != nil {
						return fmt.Errorf("saver: not saved as confidential: %w", err)
					}
				}
				protection = ProtectionE2EE
			} else if prev.confidential() {
				old, _, _, err := s.readEntry(ctx, t, prev)
				switch {
				case err == nil:
					doc.SetTags(old.Tags())
				case !errors.Is(err, ErrUnreadable):
					return err // a locked document is not overwritten blind
				}
			} else {
				doc.SetTags(prev.Tags)
			}
			doc.SetTrashed(prev.Trashed)
			doc.SetFolder(prev.Folder) // moving is Move's job
		} else {
			if _, ok := cat.Folders[doc.Folder()]; !ok {
				doc.SetFolder("") // a new document in a folder that is not there goes to the top
			}
			// A new document marked confidential stays so (a copy or an import of one),
			// if the check allows it.
			if protection = doc.Protection(); e2ee {
				protection = ProtectionE2EE
			}
			if protection == ProtectionE2EE && s.ConfidentialCheck != nil && !prechecked {
				if err := s.ConfidentialCheck(ctx, target, id); err != nil {
					return fmt.Errorf("saver: not saved as confidential: %w", err)
				}
			}
		}
		doc.SetProtection(protection)
		doc.SetID(id)
		doc.Updated = time.Now().UnixNano()
		data, err := c.Marshal(doc)
		if err != nil {
			return err
		}
		// A new file, never the one the catalog points at: the catalog switches to it once
		// it is written, and the file it replaces stays as the previous copy.
		key := genKey(id, c)
		cat.fresh = append(cat.fresh, key)
		err = t.Adapter.Save(ctx, key, data)
		inPlace := false
		if errors.Is(err, docstore.ErrStorageFull) && existed {
			// No room for a second whole file beside the current one and its previous
			// copy. Give up the backup first, and if that is not enough, write over the
			// current file, as before copies existed: a save that fits is better than
			// one refused.
			if prev.Prev != "" {
				_ = t.Adapter.Delete(ctx, prev.Prev)
				prev.Prev, prev.PrevSum = "", ""
				err = t.Adapter.Save(ctx, key, data)
			}
			if errors.Is(err, docstore.ErrStorageFull) {
				if err = t.Adapter.Save(ctx, prev.Key, data); err == nil {
					inPlace = true
				}
			}
		}
		if err != nil {
			return err
		}
		e := catalogEntry{Key: prev.Key, Sum: prev.Sum, Prev: prev.Prev, PrevSum: prev.PrevSum}
		if inPlace {
			e.Sum, e.Size = docstore.Digest(data), len(data)
		} else {
			s.rotate(ctx, t, &e, key, data, later)
		}
		e.Name, e.Format, e.Updated, e.Tags, e.Trashed, e.Folder, e.Protection = doc.Name(), c.Name(), doc.Updated, doc.Tags(), prev.Trashed, doc.Folder(), protection
		if e.confidential() {
			e.conceal()
		}
		cat.Docs[id] = e
		out = cat.entry(s, id)
		return nil
	})
	if err == nil && len(history) > 0 {
		err = s.restoreHistory(ctx, target, id, c, history)
	}
	return out, err
}

// Load reads a document back by ID. A document whose file is missing or damaged is read
// from its previous copy when there is one (see LoadChecked).
func (s *Saver) Load(ctx context.Context, target, id string) (docfile.Document, error) {
	doc, _, err := s.LoadChecked(ctx, target, id)
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
			if e.Prev != "" {
				_ = t.Adapter.Delete(ctx, e.Prev)
			}
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
