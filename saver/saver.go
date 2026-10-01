// Package saver saves and loads documents: a docfile.Document written in
// one of the docfile formats (JSON, protobuf text, protobuf binary) to
// one of several named targets, each a docstore.Adapter -- Postgres,
// SQLite, S3, local files, memory, or an HTTP server in front of any of
// them.
//
// components.Saver is the user interface for it.
package saver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Target is a place documents can be saved to.
type Target struct {
	// Name identifies the target ("postgres"); Label is what a menu shows.
	Name, Label string
	Adapter     docstore.Adapter
}

// Saver writes documents to its targets.
type Saver struct {
	Targets []Target
	// Codecs are the formats offered; nil means all of docfile.Formats.
	Codecs []docfile.Codec
}

// Entry is a saved document, as List reports it.
type Entry struct {
	Key     string // what Load and Delete take
	Name    string // the document's name, without the extension
	Format  docfile.Codec
	Size    int
	Updated time.Time
}

// FontPrefix starts the store keys of downloaded font files; List skips
// them.
const FontPrefix = "fonts/"

// ErrName is returned for a name that cannot be a key.
var ErrName = errors.New("saver: the name is empty or has a slash or other reserved characters")

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

func validName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "/\\\x00") && !strings.HasPrefix(name, "acl:") && name != "." && name != ".."
}

// Key is the store key for a document called name in codec c.
func Key(name string, c docfile.Codec) (string, error) {
	if !validName(name) {
		return "", ErrName
	}
	return name + c.Ext(), nil
}

// Save writes doc, stamped with the current time, to the target in the
// codec, as name + the codec's extension, and returns the key.
func (s *Saver) Save(ctx context.Context, target, name string, c docfile.Codec, doc docfile.Document) (string, error) {
	t, err := s.Target(target)
	if err != nil {
		return "", err
	}
	key, err := Key(name, c)
	if err != nil {
		return "", err
	}
	doc.Updated = time.Now().UnixNano()
	data, err := c.Marshal(doc)
	if err != nil {
		return "", err
	}
	return key, t.Adapter.Save(ctx, key, data)
}

// Load reads a document back. The format comes from the key's
// extension, or from the contents when that says nothing.
func (s *Saver) Load(ctx context.Context, target, key string) (docfile.Document, error) {
	t, err := s.Target(target)
	if err != nil {
		return docfile.Document{}, err
	}
	stored, err := t.Adapter.Load(ctx, key)
	if err != nil {
		return docfile.Document{}, err
	}
	c, _ := s.codecOf(key)
	if c == nil {
		c = docfile.Detect(stored.Data)
	}
	return c.Unmarshal(stored.Data)
}

// Delete removes a saved document.
func (s *Saver) Delete(ctx context.Context, target, key string) error {
	t, err := s.Target(target)
	if err != nil {
		return err
	}
	return t.Adapter.Delete(ctx, key)
}

func (s *Saver) codecOf(key string) (docfile.Codec, string) {
	for _, c := range s.codecs() {
		if name, ok := strings.CutSuffix(key, c.Ext()); ok {
			return c, name
		}
	}
	return nil, ""
}

// List returns the documents saved in the target, newest first. Keys
// that are not a document (downloaded fonts, other files) are left out.
func (s *Saver) List(ctx context.Context, target string) ([]Entry, error) {
	t, err := s.Target(target)
	if err != nil {
		return nil, err
	}
	metas, err := t.Adapter.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, m := range metas {
		if strings.HasPrefix(m.ID, FontPrefix) {
			continue
		}
		if c, name := s.codecOf(m.ID); c != nil {
			out = append(out, Entry{Key: m.ID, Name: name, Format: c, Size: m.Size, Updated: m.UpdatedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.After(out[j].Updated)
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
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
