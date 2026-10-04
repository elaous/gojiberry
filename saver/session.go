package saver

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
)

// Session is the state behind components.Saver: the chosen target and
// format, the document name, what is saved in the target, and the last
// message. Props gives the component its props, with handlers that save,
// open and delete in the background and call OnChange when something
// changed so the page can render again.
type Session struct {
	Saver *Saver
	// Get returns the document to save (the editor's current content).
	Get func() docfile.Document
	// OnOpen receives a document the user opened.
	OnOpen func(docfile.Document)
	// OnChange is called after any state change, possibly from another
	// goroutine.
	OnChange func()
	// OnUI, when set, runs f on the goroutine that owns the editor and
	// returns once it has finished; Get and OnOpen are called through it,
	// since saving and opening run in the background and the editor (a
	// canvas.TextFlow, say) is not safe for concurrent use. Nil calls f
	// directly.
	OnUI func(f func())
	// Go runs background work; nil means a goroutine. Tests set it to
	// run the function inline.
	Go func(func())

	mu                   sync.Mutex
	target, format, name string
	entries              []Entry
	versions             []Version
	trashed              []Entry
	tagFilter            string
	status               string
	failed, busy         bool
}

// NewSession starts a session on the saver's first target, in JSON.
func NewSession(s *Saver, get func() docfile.Document, onOpen func(docfile.Document)) *Session {
	ss := &Session{Saver: s, Get: get, OnOpen: onOpen, format: docfile.JSON.Name()}
	if len(s.Targets) > 0 {
		ss.target = s.Targets[0].Name
	}
	if cs := s.codecs(); len(cs) > 0 {
		ss.format = cs[0].Name()
	}
	return ss
}

func (s *Session) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Session) set(f func()) {
	s.mu.Lock()
	f()
	s.mu.Unlock()
	s.changed()
}

func (s *Session) finish(ok string, err error) {
	s.set(func() {
		s.busy = false
		s.failed = err != nil
		if err != nil {
			s.status = err.Error()
		} else {
			s.status = ok
		}
	})
}

// SetTarget chooses the target and lists what is saved in it.
func (s *Session) SetTarget(ctx context.Context, name string) {
	s.set(func() { s.target, s.entries, s.status, s.failed = name, nil, "", false })
	s.Refresh(ctx)
}

// SetFormat chooses the format; SetName names the document.
func (s *Session) SetFormat(name string) { s.set(func() { s.format = name }) }

// SetName names the document.
func (s *Session) SetName(name string) { s.set(func() { s.name = name }) }

// Name returns the document name.
func (s *Session) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// Refresh lists the documents saved in the current target.
func (s *Session) Refresh(ctx context.Context) {
	s.mu.Lock()
	target := s.target
	s.busy = true
	s.mu.Unlock()
	s.changed()
	list, err := s.Saver.ListTagged(ctx, target)
	var trashed []Entry
	var versions []Version
	if err == nil {
		trashed, err = s.Saver.Trashed(ctx, target)
	}
	if err == nil {
		s.mu.Lock()
		name := s.name
		s.mu.Unlock()
		if validName(name) {
			versions, _ = s.Saver.Versions(ctx, target, name)
		}
	}
	s.mu.Lock()
	// A slow answer for a target the user has since left must not replace
	// the list of the one they are looking at.
	if err == nil && s.target == target {
		s.entries, s.trashed, s.versions = list, trashed, versions
		if s.tagFilter != "" && !slices.ContainsFunc(allTags(list), func(t string) bool { return strings.EqualFold(t, s.tagFilter) }) {
			s.tagFilter = "" // the last document with that tag lost it
		}
	}
	s.mu.Unlock()
	s.finish("", err)
}

func allTags(entries []Entry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		for _, t := range e.Tags {
			if k := strings.ToLower(t); !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// Save writes the current document to the target in the chosen format.
func (s *Session) Save(ctx context.Context) {
	s.mu.Lock()
	target, format, name := s.target, s.format, s.name
	s.busy = true
	s.mu.Unlock()
	s.changed()
	c, err := s.Saver.Codec(format)
	var key string
	if err == nil {
		var doc docfile.Document
		s.ui(func() { doc = s.Get() })
		if doc.Title == "" {
			doc.Title = name
		}
		key, err = s.Saver.Save(ctx, target, name, c, doc)
	}
	if err != nil {
		s.finish("", err)
		return
	}
	s.finish("Saved "+key, nil)
	s.Refresh(ctx)
	s.set(func() { s.status = "Saved " + key })
}

// Open loads a saved document and hands it to OnOpen, taking its name and
// format.
func (s *Session) Open(ctx context.Context, key string) {
	s.mu.Lock()
	target := s.target
	s.busy = true
	s.mu.Unlock()
	s.changed()
	doc, err := s.Saver.Load(ctx, target, key)
	if err != nil {
		s.finish("", err)
		return
	}
	c, name := s.Saver.codecOf(key)
	s.set(func() {
		if c != nil {
			s.format, s.name = c.Name(), name
		}
	})
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(doc) })
	}
	s.finish("Opened "+key, nil)
	s.Refresh(ctx) // the versions are those of the document just opened
	s.set(func() { s.status, s.failed = "Opened "+key, false })
}

// Delete removes a saved document.
func (s *Session) Delete(ctx context.Context, key string) {
	s.mu.Lock()
	target := s.target
	s.busy = true
	s.mu.Unlock()
	s.changed()
	if err := s.Saver.Delete(ctx, target, key); err != nil {
		s.finish("", err)
		return
	}
	s.Refresh(ctx)
	s.set(func() { s.status = "Deleted " + key })
}

// begin marks the session busy and returns the target.
func (s *Session) begin() string {
	s.mu.Lock()
	target := s.target
	s.busy = true
	s.mu.Unlock()
	s.changed()
	return target
}

// after refreshes the lists and then shows msg as the status.
func (s *Session) after(ctx context.Context, msg string) {
	s.Refresh(ctx)
	s.set(func() { s.status, s.failed = msg, false })
}

// SaveVersion snapshots the current document as a version of the one called
// by the name.
func (s *Session) SaveVersion(ctx context.Context) {
	target := s.begin()
	s.mu.Lock()
	format, name := s.format, s.name
	s.mu.Unlock()
	c, err := s.Saver.Codec(format)
	if err == nil {
		var doc docfile.Document
		s.ui(func() { doc = s.Get() })
		if doc.Title == "" {
			doc.Title = name
		}
		_, err = s.Saver.SaveVersion(ctx, target, name, c, doc)
	}
	if err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Saved a version of "+name)
}

// OpenVersion opens a snapshot in the editor. The document keeps its name, so
// saving it afterwards replaces the saved one: go back, then Save.
func (s *Session) OpenVersion(ctx context.Context, key string) {
	target := s.begin()
	doc, err := s.Saver.LoadVersion(ctx, target, key)
	if err != nil {
		s.finish("", err)
		return
	}
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(doc) })
	}
	s.finish("Opened a version", nil)
}

// DeleteVersion removes one snapshot.
func (s *Session) DeleteVersion(ctx context.Context, key string) {
	target := s.begin()
	if err := s.Saver.DeleteVersion(ctx, target, key); err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Deleted a version")
}

// Trash moves a saved document to the trash.
func (s *Session) Trash(ctx context.Context, key string) {
	target := s.begin()
	if err := s.Saver.Trash(ctx, target, key); err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Moved "+key+" to the trash")
}

// Restore takes a document out of the trash.
func (s *Session) Restore(ctx context.Context, trashKey string) {
	target := s.begin()
	key, err := s.Saver.Restore(ctx, target, trashKey)
	if err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Restored "+key)
}

// Purge deletes a trashed document for good.
func (s *Session) Purge(ctx context.Context, trashKey string) {
	target := s.begin()
	if err := s.Saver.Purge(ctx, target, trashKey); err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Deleted it for good")
}

// EmptyTrash deletes everything in the trash.
func (s *Session) EmptyTrash(ctx context.Context) {
	target := s.begin()
	n, err := s.Saver.EmptyTrash(ctx, target)
	if err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, fmt.Sprintf("Emptied the trash (%d)", n))
}

// Duplicate saves a copy of a document.
func (s *Session) Duplicate(ctx context.Context, key string) {
	target := s.begin()
	newKey, err := s.Saver.Duplicate(ctx, target, key, "")
	if err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Copied "+key+" to "+newKey)
}

// SetTags sets a saved document's tags.
func (s *Session) SetTags(ctx context.Context, key string, tags []string) {
	target := s.begin()
	if err := s.Saver.SetTags(ctx, target, key, tags); err != nil {
		s.finish("", err)
		return
	}
	s.after(ctx, "Tagged "+key)
}

// SetTagFilter narrows the list to the documents with the tag ("" for all).
func (s *Session) SetTagFilter(tag string) { s.set(func() { s.tagFilter = tag }) }

func (s *Session) ui(f func()) {
	if s.OnUI != nil {
		s.OnUI(f)
		return
	}
	f()
}

func (s *Session) run(f func(context.Context)) {
	job := func() { f(context.Background()) }
	if s.Go != nil {
		s.Go(job)
		return
	}
	go job()
}

// Props returns the props for components.Saver as the session stands.
func (s *Session) Props() components.SaverProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.SaverProps{
		Target: s.target, Format: s.format, Name: s.name,
		Status: s.status, Failed: s.failed, Busy: s.busy,
		OnTarget:  func(n string) { s.run(func(ctx context.Context) { s.SetTarget(ctx, n) }) },
		OnFormat:  s.SetFormat,
		OnName:    s.SetName,
		OnSave:    func() { s.run(s.Save) },
		OnRefresh: func() { s.run(s.Refresh) },
		OnOpen:    func(k string) { s.run(func(ctx context.Context) { s.Open(ctx, k) }) },
		OnDelete:  func(k string) { s.run(func(ctx context.Context) { s.Delete(ctx, k) }) },

		TagFilter:   s.tagFilter,
		OnTagFilter: s.SetTagFilter,
		OnDuplicate: func(k string) { s.run(func(ctx context.Context) { s.Duplicate(ctx, k) }) },
		OnTrash:     func(k string) { s.run(func(ctx context.Context) { s.Trash(ctx, k) }) },
		OnTags:      func(k string, tags []string) { s.run(func(ctx context.Context) { s.SetTags(ctx, k, tags) }) },

		OnSaveVersion:   func() { s.run(s.SaveVersion) },
		OnOpenVersion:   func(k string) { s.run(func(ctx context.Context) { s.OpenVersion(ctx, k) }) },
		OnDeleteVersion: func(k string) { s.run(func(ctx context.Context) { s.DeleteVersion(ctx, k) }) },
		OnRestore:       func(k string) { s.run(func(ctx context.Context) { s.Restore(ctx, k) }) },
		OnPurge:         func(k string) { s.run(func(ctx context.Context) { s.Purge(ctx, k) }) },
		OnEmptyTrash:    func() { s.run(s.EmptyTrash) },
	}
	for _, t := range s.Saver.Targets {
		label := t.Label
		if label == "" {
			label = t.Name
		}
		p.Targets = append(p.Targets, components.SaverChoice{Name: t.Name, Label: label})
	}
	for _, c := range s.Saver.codecs() {
		p.Formats = append(p.Formats, components.SaverChoice{Name: c.Name(), Label: c.Label()})
	}
	for _, e := range s.entries {
		p.Entries = append(p.Entries, components.SaverEntry{Key: e.Key, Name: e.Name, Format: e.Format.Label(), Size: e.Size, Updated: e.Updated, Tags: e.Tags})
	}
	p.AllTags = allTags(s.entries)
	for _, e := range s.trashed {
		p.Trashed = append(p.Trashed, components.SaverEntry{Key: e.Key, Name: e.Name, Format: e.Format.Label(), Size: e.Size, Updated: e.Updated})
	}
	for _, v := range s.versions {
		p.Versions = append(p.Versions, components.SaverVersion{Key: v.Key, Time: v.Time, Size: v.Size})
	}
	return p
}

func (s *Session) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("saver session: %s as %s", s.target, s.format)
}
