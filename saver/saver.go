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
	Name, Label string
	Adapter     docstore.Adapter
	Capacity    int64
	// must start with "catalog:" and end with "index"
	CatalogKey string
}

// Saver writes documents to its targets.
type Saver struct {
	Targets []Target
	// Codecs are the formats offered; nil means all of docfile.Formats.
	Codecs            []docfile.Codec
	MaxVersions       int
	PreviousCopies    int
	RepairGrace       time.Duration
	ConfidentialCheck func(ctx context.Context, target, id string) error
	Rekey             func(ctx context.Context, target, id, protection string) error
	PendingCheck      func(ctx context.Context, target string) (pending, stuck []string, err error)
	UnsignedCheck     func(ctx context.Context, target string) (docs []string, err error)

	mu sync.Mutex   // one catalog update at a time
	tm sync.RWMutex // Targets, which AddTarget and RemoveTarget change
}

// Entry is a saved document, as List reports it.
type Entry struct {
	ID      string // the document's UUID: what every method here takes
	Key     string
	Name    string // a label; not unique
	Format  docfile.Codec
	Size    int
	Updated time.Time
	Tags    []string
	Folder  string
	Trashed time.Time
	// Protection is ProtectionServer or ProtectionE2EE.
	Protection string
}

const FontPrefix = "fonts/"

var ErrUnreadable = errors.New("saver: the document's file is missing or damaged")

// DefaultRepairGrace is the RepairGrace used when none is set.
const DefaultRepairGrace = 2 * time.Second

var ErrBusy = errors.New("saver: the store is busy, try again")

var ErrName = errors.New("saver: the name is empty, too long, or has control characters")

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

func (s *Saver) TargetList() []Target {
	s.tm.RLock()
	defer s.tm.RUnlock()
	return append([]Target(nil), s.Targets...)
}

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

func reserved(key string) bool {
	return strings.HasPrefix(key, "acl:") || strings.HasPrefix(key, "collab:") || strings.HasPrefix(key, "catalog:") ||
		strings.HasPrefix(key, VersionPrefix) || strings.HasPrefix(key, docstore.FolderSecretPrefix)
}

func (s *Saver) codecOf(key string) (docfile.Codec, string) {
	for _, c := range s.codecs() {
		if stem, ok := strings.CutSuffix(key, c.Ext()); ok {
			return c, stem
		}
	}
	return nil, ""
}

// Save writes doc to the target in the codec.
func (s *Saver) Save(ctx context.Context, target string, c docfile.Codec, doc docfile.Document) (Entry, error) {
	return s.save(ctx, target, c, doc, false)
}

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
	// a folder's ID is never a document's: refused before any hook
	if s.Rekey == nil && s.ConfidentialCheck == nil {
	} else if err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		if _, ok := cat.Folders[id]; ok {
			return ErrFolderID
		}
		return nil
	}); err != nil {
		return Entry{}, err
	}
	// history lives in versions, not in the document file
	history := doc.History
	doc.History = nil
	var out Entry
	forced := false
	// the in-lock read found a level the first read did not
	for attempt := 0; ; attempt++ {
		prechecked, rekeyed := false, false
		if s.Rekey != nil {
			want := e2ee || forced
			known := false // the document is in the catalog, and as what
			if doc.ID() != "" {
				cur, gerr := s.Get(ctx, target, id)
				switch {
				case gerr == nil:
					known = cur.Protection == ProtectionE2EE
				case errors.Is(gerr, ErrNotFound):
					want = want || doc.Protection() == ProtectionE2EE
				default:
					// never treated as not there: the document may be confidential
					return Entry{}, gerr
				}
			} else {
				want = want || doc.Protection() == ProtectionE2EE
			}
			want = want || known
			if want {
				if !known && s.ConfidentialCheck != nil {
					if err := s.ConfidentialCheck(ctx, target, id); err != nil {
						return Entry{}, fmt.Errorf("saver: not saved as confidential: %w", err)
					}
				}
				prechecked = true
				if err := s.Rekey(ctx, target, id, ProtectionE2EE); err != nil {
					return Entry{}, fmt.Errorf("saver: not saved as confidential: %w", err)
				}
				rekeyed = true
			}
		}
		err := s.saveLocked(ctx, target, c, doc, history, id, e2ee, prechecked, rekeyed, &out)
		if errors.Is(err, errNeedRekey) && attempt < 2 {
			forced = true
			// the level changed since it was read: start over, re-keyed
			continue
		}
		if errors.Is(err, errNeedRekey) {
			return Entry{}, fmt.Errorf("saver: the document's protection changed while it was being saved, try again: %w", ErrBusy)
		}
		return out, err
	}
}

var errNeedRekey = errors.New("saver: the document became confidential after it was read")

func (s *Saver) saveLocked(ctx context.Context, target string, c docfile.Codec, doc docfile.Document, history []docfile.Revision, id string, e2ee, prechecked, rekeyed bool, out *Entry) error {
	err := s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		prev, existed := cat.Docs[id]
		protection := prev.Protection
		if existed {
			// the catalog is where tags are edited: saving restores none
			if e2ee && !prev.confidential() {
				// becoming confidential by this save: the same gate as SetProtection's
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
			if _, ok := cat.Folders[id]; ok {
				return ErrFolderID
			}
			if _, ok := cat.Folders[doc.Folder()]; !ok {
				doc.SetFolder("")
			}
			// a copy or import of a confidential document stays so if allowed
			if protection = doc.Protection(); e2ee {
				protection = ProtectionE2EE
			}
			if protection == ProtectionE2EE && s.ConfidentialCheck != nil && !prechecked {
				if err := s.ConfidentialCheck(ctx, target, id); err != nil {
					return fmt.Errorf("saver: not saved as confidential: %w", err)
				}
			}
		}
		if protection == ProtectionE2EE && s.Rekey != nil && !rekeyed {
			// confidential but keys not asserted: not written
			return errNeedRekey
		}
		doc.SetProtection(protection)
		doc.SetID(id)
		doc.Updated = time.Now().UnixNano()
		data, err := c.Marshal(doc)
		if err != nil {
			return err
		}
		// new file: the replaced one stays as previous copy
		key := genKey(id, c)
		cat.fresh = append(cat.fresh, key)
		err = t.Adapter.Save(ctx, key, data)
		inPlace := false
		if errors.Is(err, docstore.ErrStorageFull) && existed {
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
		*out = cat.entry(s, id)
		return nil
	})
	if err == nil && len(history) > 0 {
		err = s.restoreHistory(ctx, target, id, c, history)
	}
	return err
}

// Load reads a document back by ID.
func (s *Saver) Load(ctx context.Context, target, id string) (docfile.Document, error) {
	doc, _, err := s.LoadChecked(ctx, target, id)
	return doc, err
}

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

func (s *Saver) List(ctx context.Context, target string) ([]Entry, error) {
	var out []Entry
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		out = cat.sorted(s, false)
		return nil
	})
	return out, err
}

func (s *Saver) Lists(ctx context.Context, target string) (live, trashed []Entry, err error) {
	err = s.view(ctx, target, func(_ Target, cat *catalog) error {
		live, trashed = cat.sorted(s, false), cat.sorted(s, true)
		return nil
	})
	return live, trashed, err
}

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

func Build(ctx context.Context, name, label string, cfg docstore.Config) (Target, error) {
	a, err := docstore.BuildAdapter(ctx, cfg)
	if err != nil {
		return Target{}, err
	}
	return Target{Name: name, Label: label, Adapter: a}, nil
}
