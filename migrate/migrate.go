// Package migrate moves documents from other platforms into a Saver: a Source
// lists and fetches Word files (Google Docs exports them, OneDrive keeps them),
// Run walks the folders chosen, converts each file with docximport and hands the
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
	"github.com/elaous/gojiberry/docximport"
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
	// MaxFileBytes is the largest file taken from a source.
	MaxFileBytes = 64 << 20
)

// Options configures Run.
type Options struct {
	// Import converts a Word file; nil is docximport.Import.
	Import func([]byte) (docfile.Document, error)
	// Tag is put on every document made ("google-docs").
	Tag string
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
	Failed   []Failure
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
	var walk func(items []Item, path []string, depth int) error
	walk = func(items []Item, path []string, depth int) error {
		for _, it := range items {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !it.Folder {
				if len(files) >= MaxFiles {
					rep.Truncated = true
					return nil
				}
				files = append(files, file{it, path})
				continue
			}
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
		imp = docximport.Import
	}
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if o.OnProgress != nil {
			o.OnProgress(i, len(files), f.item.Name)
		}
		if err := one(ctx, src, f, sink, imp, o.Tag); err != nil {
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

func one(ctx context.Context, src Source, f file, sink Sink, imp func([]byte) (docfile.Document, error), tag string) error {
	data, err := src.Fetch(ctx, f.item)
	if err != nil {
		return err
	}
	doc, err := imp(data)
	if err != nil {
		return fmt.Errorf("migrate: not a readable Word file: %w", err)
	}
	return sink.Save(ctx, f.path, DocName(f.item.Name), tag, doc)
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
