// Package migrate moves documents from other platforms into a Saver: a Source
// lists and fetches Word files (Google Docs exports them, OneDrive keeps them),
// Run walks the folders chosen, converts each file with a Word importer (Options.Import) and hands the
// document to a Sink, and Session is the state behind components.Migrate.
//
// Signing in is not here: a source is made with an access token the host got
// (OAuth is the host's job). Folders are kept as folders.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
)

// Item is a folder or a document at a source.
type Item struct {
	ID       string
	Name     string
	Folder   bool
	Modified time.Time
	Size     int64
}

// Source is a place documents are migrated from.
type Source interface {
	// Label is the source's name for people ("Google Docs").
	Label() string
	// List returns the folders and importable documents in a folder ("" is the top).
	List(ctx context.Context, folder string) ([]Item, error)
	// Fetch returns a document as a Word (.docx) file.
	Fetch(ctx context.Context, item Item) ([]byte, error)
}

// Version is an earlier version of a document at a source, as a Word file.
type Version struct {
	Saved time.Time
	Data  []byte
}

// Versioned is a Source that can give earlier versions of a document (Google Docs keeps a revision
// history). Versions returns up to max of them, oldest first, not counting the current one.
type Versioned interface {
	Versions(ctx context.Context, item Item, max int) ([]Version, error)
}

// Sink is where migrated documents go.
type Sink interface {
	// Save stores doc as a new document called name inside the folders of path
	// (names, from the top of the migration down; empty for directly in it).
	// tag, if set, goes on the document.
	Save(ctx context.Context, path []string, name, tag string, doc docfile.Document) error
}

// Limits on a migration, so one request cannot run away.
const (
	MaxFiles = 2000
	MaxDepth = 16
	// MaxFolders is how many folders are listed in one run: every folder costs a
	// request to the platform, however few files it holds.
	MaxFolders = 500
	// MaxFileBytes is the largest file taken from a source.
	MaxFileBytes = 64 << 20
)

// DefaultImport converts a Word file when Options.Import is nil. It is not set here, so that a program that
// reads Word files elsewhere (in another wasm, as the showcase does) does not link the reader; a program that
// wants it in the same binary sets it to docximport.Import.
var DefaultImport func([]byte) (docfile.Document, error)

// ErrNoImporter is returned by Run when neither Options.Import nor DefaultImport is set.
var ErrNoImporter = errors.New("migrate: no Word importer is set (Options.Import)")

// Options configures Run.
type Options struct {
	// Import converts a Word file; nil is DefaultImport.
	Import func([]byte) (docfile.Document, error)
	// Tag is put on every document made ("google-docs").
	Tag string
	// Ledger says what an earlier run already brought; with it Run skips a file that is there and has
	// not changed at the source since (Item.Modified is not later than when it was brought). nil
	// brings everything.
	Ledger Ledger
	// Again brings files the Ledger knows too (the person asked to bring them again).
	Again bool
	// History is how many earlier versions of each document to bring along, for a Source that is
	// Versioned (0: none). They become the document's History, oldest first.
	History int
	// OnProgress is told before each file is handled: how many are done, how many
	// there are and the name of the one starting.
	OnProgress func(done, total int, name string)
}

// Failure is a file that was not migrated.
type Failure struct {
	Name string
	Err  error
}

// Report says how a migration went.
type Report struct {
	Imported int
	// Skipped is how many files an earlier run had brought already (Options.Ledger) and were left.
	Skipped int
	// Versions is how many earlier versions came along (Options.History), over all documents; a
	// version that could not be read is left out and counted in VersionsFailed.
	Versions, VersionsFailed int
	Failed                   []Failure
	// Truncated is set when the limit on files stopped the walk.
	Truncated bool
}

type file struct {
	item Item
	path []string
}

// Run migrates the items (folders are walked) from src into sink. A file that
// fails is reported and the rest go on; only the walk failing, or ctx ending,
// stops it.
func Run(ctx context.Context, src Source, items []Item, sink Sink, o Options) (Report, error) {
	var rep Report
	var files []file
	folders := 0
	var walk func(items []Item, path []string, depth int) error
	walk = func(items []Item, path []string, depth int) error {
		for _, it := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if rep.Truncated {
				return nil // a limit was reached: stop asking for more
			}
			if !it.Folder {
				if len(files) >= MaxFiles {
					rep.Truncated = true
					return nil
				}
				files = append(files, file{it, path})
				continue
			}
			if folders >= MaxFolders {
				rep.Truncated = true
				return nil
			}
			folders++
			if depth >= MaxDepth {
				rep.Failed = append(rep.Failed, Failure{it.Name, errors.New("migrate: folders nest too deep")})
				continue
			}
			kids, err := src.List(ctx, it.ID)
			if err != nil {
				rep.Failed = append(rep.Failed, Failure{it.Name, err})
				continue
			}
			if err := walk(kids, append(append([]string(nil), path...), it.Name), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(items, nil, 0); err != nil {
		return rep, err
	}
	imp := o.Import
	if imp == nil {
		imp = DefaultImport
	}
	if imp == nil {
		return rep, ErrNoImporter
	}
	if o.Ledger != nil && !o.Again && stable(src) {
		kept := files[:0]
		for _, f := range files {
			if at, ok := o.Ledger.Brought(OriginKey(src, f.item)); ok && !f.item.Modified.After(at) {
				rep.Skipped++
				continue
			}
			kept = append(kept, f)
		}
		files = kept
	}
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if o.OnProgress != nil {
			o.OnProgress(i, len(files), f.item.Name)
		}
		if err := one(ctx, src, f, sink, imp, o, &rep); err != nil {
			rep.Failed = append(rep.Failed, Failure{f.item.Name, err})
			continue
		}
		rep.Imported++
	}
	if o.OnProgress != nil {
		o.OnProgress(len(files), len(files), "")
	}
	return rep, nil
}

func one(ctx context.Context, src Source, f file, sink Sink, imp func([]byte) (docfile.Document, error), o Options, rep *Report) error {
	data, err := src.Fetch(ctx, f.item)
	if err != nil {
		return err
	}
	doc, err := imp(data)
	if err != nil {
		return fmt.Errorf("migrate: not a readable Word file: %w", err)
	}
	if v, ok := src.(Versioned); ok && o.History > 0 {
		// A document is not failed for its history: what could not be read is counted and left out.
		versions, err := v.Versions(ctx, f.item, o.History)
		if err != nil {
			rep.VersionsFailed++
		}
		for _, ver := range versions {
			old, err := imp(ver.Data)
			if err != nil {
				rep.VersionsFailed++
				continue
			}
			doc.History = append(doc.History, docfile.Revision{Saved: ver.Saved.UnixNano(), Doc: &old})
			rep.Versions++
		}
	}
	// The document keeps where it came from, so that a later run knows (SaverLedger reads it back).
	if stable(src) {
		doc.SetTags(append(doc.Tags(), OriginTag(src, f.item)))
	}
	return sink.Save(ctx, f.path, DocName(f.item.Name), o.Tag, doc)
}

// DocName is a file name as a document name: the .docx or .doc extension goes.
func DocName(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".docx", ".doc"} {
		if strings.HasSuffix(lower, ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	if name = strings.TrimSpace(name); name == "" {
		return "Untitled"
	}
	return name
}

// Files is a Source of Word files already in memory, for the ones a person picked
// from their own computer. Its items are numbered by position.
type Files []File

// File is a Word file in memory.
type File struct {
	Name string
	Data []byte
}

// Label implements Source.
func (Files) Label() string { return "Word files" }

// Stable says the IDs are positions in this list: they name no file from one run to the next, so Files
// carry no origin tag and are never skipped as already brought.
func (Files) Stable() bool { return false }

// List implements Source: the files, all at the top.
func (f Files) List(_ context.Context, folder string) ([]Item, error) {
	if folder != "" {
		return nil, nil
	}
	items := make([]Item, len(f))
	for i, x := range f {
		items[i] = Item{ID: fmt.Sprint(i), Name: x.Name, Size: int64(len(x.Data))}
	}
	return items, nil
}

// Fetch implements Source.
func (f Files) Fetch(_ context.Context, item Item) ([]byte, error) {
	var i int
	if _, err := fmt.Sscan(item.ID, &i); err != nil || i < 0 || i >= len(f) {
		return nil, errors.New("migrate: no such file")
	}
	return f[i].Data, nil
}

// stable says a source's item IDs name the same file from one run to the next. A source whose IDs are only
// positions in a list (Files) says no with Stable() bool, and its files carry no origin and are never skipped.
func stable(src Source) bool {
	s, ok := src.(interface{ Stable() bool })
	return !ok || s.Stable()
}

// OriginPrefix starts the tag a migrated document carries to say where it came from.
const OriginPrefix = "from:"

// OriginKey names a file at a source: the source's label and the file's ID there.
func OriginKey(src Source, item Item) string { return tagOf(src.Label()) + ":" + item.ID }

// OriginTag is the tag put on a migrated document: "from:<source>:<ID at the source>:<modified, unix
// seconds>". A later run reads it (SaverLedger) to leave the file alone while it has not changed.
func OriginTag(src Source, item Item) string {
	var at int64
	if !item.Modified.IsZero() {
		at = item.Modified.Unix()
	}
	return fmt.Sprintf("%s%s:%d", OriginPrefix, OriginKey(src, item), at)
}

// Ledger says what earlier runs brought.
type Ledger interface {
	// Brought reports whether the file with this key (OriginKey) was brought before, and the time the
	// source said it was modified then.
	Brought(key string) (modified time.Time, ok bool)
}

// MapLedger is a Ledger in memory (and what ParseOrigins returns).
type MapLedger map[string]time.Time

// Brought implements Ledger.
func (m MapLedger) Brought(key string) (time.Time, bool) { t, ok := m[key]; return t, ok }

// ParseOrigins reads origin tags (OriginTag) from any tags into a ledger; other tags are ignored. Of
// several tags for one file the latest time counts.
func ParseOrigins(tags []string) MapLedger {
	out := MapLedger{}
	for _, t := range tags {
		rest, ok := strings.CutPrefix(t, OriginPrefix)
		if !ok {
			continue
		}
		i := strings.LastIndex(rest, ":")
		if i < 0 {
			continue
		}
		var secs int64
		if _, err := fmt.Sscan(rest[i+1:], &secs); err != nil {
			continue
		}
		key, at := rest[:i], time.Unix(secs, 0)
		if old, had := out[key]; !had || at.After(old) {
			out[key] = at
		}
	}
	return out
}
