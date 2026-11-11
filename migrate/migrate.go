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
	List(ctx context.Context, folder string) ([]Item, error)
	// Fetch returns a document as a Word (.docx) file.
	Fetch(ctx context.Context, item Item) ([]byte, error)
}

type Version struct {
	Saved time.Time
	Data  []byte
}

type Versioned interface {
	Versions(ctx context.Context, item Item, max int) ([]Version, error)
}

// Sink is where migrated documents go.
type Sink interface {
	Save(ctx context.Context, path []string, name, tag string, doc docfile.Document) error
}

// Limits on a migration, so one request cannot run away.
const (
	MaxFiles   = 2000
	MaxDepth   = 16
	MaxFolders = 500
	// MaxFileBytes is the largest file taken from a source.
	MaxFileBytes = 64 << 20
)

// DefaultImport converts a Word file when Options.Import is nil.
var DefaultImport func([]byte) (docfile.Document, error)

var ErrNoImporter = errors.New("migrate: no Word importer is set (Options.Import)")

// Options configures Run.
type Options struct {
	// Import converts a Word file; nil is DefaultImport.
	Import func([]byte) (docfile.Document, error)
	// Tag is put on every document made ("google-docs").
	Tag        string
	Ledger     Ledger
	Again      bool
	History    int
	OnProgress func(done, total int, name string)
}

// Failure is a file that was not migrated.
type Failure struct {
	Name string
	Err  error
}

// Report says how a migration went.
type Report struct {
	Imported                 int
	Skipped                  int
	Versions, VersionsFailed int
	Failed                   []Failure
	Truncated                bool
}

type file struct {
	item Item
	path []string
}

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
	if stable(src) {
		doc.SetTags(append(doc.Tags(), OriginTag(src, f.item)))
	}
	return sink.Save(ctx, f.path, DocName(f.item.Name), o.Tag, doc)
}

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

type Files []File

// File is a Word file in memory.
type File struct {
	Name string
	Data []byte
}

// Label implements Source.
func (Files) Label() string { return "Word files" }

// IDs are positions, not names: never skipped as already brought
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

func stable(src Source) bool {
	s, ok := src.(interface{ Stable() bool })
	return !ok || s.Stable()
}

const OriginPrefix = "from:"

func OriginKey(src Source, item Item) string { return tagOf(src.Label()) + ":" + item.ID }

func OriginTag(src Source, item Item) string {
	var at int64
	if !item.Modified.IsZero() {
		at = item.Modified.Unix()
	}
	return fmt.Sprintf("%s%s:%d", OriginPrefix, OriginKey(src, item), at)
}

// Ledger says what earlier runs brought.
type Ledger interface {
	Brought(key string) (modified time.Time, ok bool)
}

// MapLedger is a Ledger in memory (and what ParseOrigins returns).
type MapLedger map[string]time.Time

// Brought implements Ledger.
func (m MapLedger) Brought(key string) (time.Time, bool) { t, ok := m[key]; return t, ok }

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
