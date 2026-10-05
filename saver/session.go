package saver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
)

// Session is the state behind the document components: the chosen target and
// format, the document being edited (its ID and name), what is saved in the
// target, and the last message. It is a backend for components.DocList,
// Versions and Trash (Docs, Versions and Trash give each its props, with the
// operations plugged in) and for components.Saver, which composes the three
// with the save form (Props).
//
// Operations run in the background through Pending, which tells the components
// when one is running and shows the error of one that failed; OnChange is called
// after any change so the page can render again.
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
	// Pending tracks the background operations; tests set Pending.Inline to run
	// them inline.
	Pending *components.Pending
	// Asker asks before documents are deleted for good; the Saver form shows its
	// dialog. Set Asker.Title and the labels to change its words.
	Asker *components.Asker
	// Deliver hands a file to the person (in a browser: transfer.Download); nil
	// leaves the Download controls out.
	Deliver func(file, mime string, data []byte)
	// Export turns a document into a file in a format (transfer.Registry.Export
	// fits, with the registry's name for the format); nil makes Download send
	// the document as saved. DownloadFormats lists the formats offered, in the
	// order given: a name and the label shown.
	Export          func(format string, doc docfile.Document) (file string, data []byte, mime string, err error)
	DownloadFormats []components.SaverChoice
	// Link gives the address of a document ("document") or folder ("folder") by
	// ID; nil leaves the Link controls out. Copy puts a link where it can be
	// pasted (the clipboard); without it the link is shown in the status line.
	Link func(kind, id string) string
	Copy func(text string)

	op                   sync.Mutex // one saving, opening or deleting at a time
	mu                   sync.Mutex
	target, format, name string
	id                   string // the saved document being edited, "" for one never saved
	entries              []Entry
	folder               string   // the folder being shown, "" at the top level
	folders              []Folder // every folder
	folderDraft          string
	versions             []Version
	trashed              []Entry
	selected             []string
	tagFilter            string
	usage                Usage
	checked              map[string]bool // targets already checked for leftovers of a crash
	status               string
}

// NewSession starts a session on the saver's first target, in JSON.
func NewSession(s *Saver, get func() docfile.Document, onOpen func(docfile.Document)) *Session {
	ss := &Session{Saver: s, Get: get, OnOpen: onOpen, format: docfile.JSON.Name()}
	ss.Pending = components.NewPending(ss.changed)
	ss.Asker = components.NewAsker(ss.changed)
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

func (s *Session) ui(f func()) {
	if s.OnUI != nil {
		s.OnUI(f)
		return
	}
	f()
}

func (s *Session) targetName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target
}

// SetTarget chooses the target and lists what is saved in it. The document being
// edited is not in the new target, so it is saved there as a new document.
func (s *Session) SetTarget(ctx context.Context, name string) error {
	s.set(func() {
		s.target, s.entries, s.trashed, s.versions, s.status, s.id = name, nil, nil, nil, "", ""
		s.folder, s.folders, s.usage = "", nil, Usage{}
	})
	return s.Refresh(ctx)
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

// ID returns the ID of the saved document being edited, "" if it was never saved.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Detach makes the document being edited a new one: the next Save creates a
// document with a new ID instead of replacing the one that was open. Call it when
// the editor's content is replaced by something that is not a saved document (an
// import, a template).
func (s *Session) Detach() { s.set(func() { s.id = "" }) }

// Refresh lists the documents saved in the current target, those in the trash and
// the versions of the document being edited.
func (s *Session) Refresh(ctx context.Context) error {
	target, id := s.targetName(), s.ID()
	s.mu.Lock()
	first := !s.checked[target]
	if s.checked == nil {
		s.checked = map[string]bool{}
	}
	s.checked[target] = true
	s.mu.Unlock()
	if first {
		// Once per session and target: leftovers of an operation that was cut short
		// (see Saver.Repair) are put right before the lists are read.
		_, _ = s.Saver.Repair(ctx, target)
	}
	usage, usageErr := s.Saver.Usage(ctx, target)
	list, trashed, folders, err := s.Saver.Overview(ctx, target) // one read: a document is in one list
	var versions []Version
	if err == nil && id != "" {
		versions, _ = s.Saver.Versions(ctx, target, id)
	}
	if err != nil {
		return err
	}
	s.set(func() {
		// A slow answer for a target the user has since left must not replace
		// the list of the one they are looking at.
		if s.target != target {
			return
		}
		s.entries, s.trashed, s.versions, s.folders = list, trashed, versions, folders
		if usageErr == nil {
			s.usage = usage
		}
		if s.folder != "" && !slices.ContainsFunc(folders, func(f Folder) bool { return f.ID == s.folder }) {
			s.folder = "" // the folder was deleted from elsewhere
		}
		s.selected = slices.DeleteFunc(s.selected, func(id string) bool {
			return !slices.ContainsFunc(trashed, func(e Entry) bool { return e.ID == id })
		})
		if s.tagFilter != "" && !slices.ContainsFunc(allTags(list), func(t string) bool { return strings.EqualFold(t, s.tagFilter) }) {
			s.tagFilter = "" // the last document with that tag lost it
		}
	})
	return nil
}

func allTags(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Tags...)
	}
	return out
}

func (s *Session) say(msg string) { s.set(func() { s.status = msg }) }

// msg is a status line in the interface language, with args put into it ("%s saved").
func msg(id, def string, args ...any) string {
	return fmt.Sprintf(components.Translate(id, def), args...)
}

// names says which documents or folders an operation was about: the name, a few
// names, or how many.
func (s *Session) names(ids []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, id := range ids {
		found := id
		for _, e := range s.entries {
			if e.ID == id {
				found = e.Name
			}
		}
		for _, e := range s.trashed {
			if e.ID == id {
				found = e.Name
			}
		}
		for _, f := range s.folders {
			if f.ID == id {
				found = f.Name
			}
		}
		out = append(out, found)
	}
	switch {
	case len(out) == 0:
		return ""
	case len(out) <= 2:
		return strings.Join(out, ", ")
	}
	return msg("saver.nItems", "%d items", len(out))
}

// after refreshes the lists and then shows msg as the status.
func (s *Session) after(ctx context.Context, msg string) error {
	if err := s.Refresh(ctx); err != nil {
		return err
	}
	s.say(msg)
	return nil
}

// current is the document to save, named and with the ID of the one being edited.
func (s *Session) current() (docfile.Document, docfile.Codec, error) {
	doc, c, _, err := s.currentIn()
	return doc, c, err
}

// currentIn is current and the target it is for, read together.
func (s *Session) currentIn() (docfile.Document, docfile.Codec, string, error) {
	s.mu.Lock()
	format, name, id, target, folder := s.format, s.name, s.id, s.target, s.folder
	s.mu.Unlock()
	c, err := s.Saver.Codec(format)
	if err != nil {
		return docfile.Document{}, nil, "", err
	}
	var doc docfile.Document
	s.ui(func() { doc = s.Get() })
	if doc.Title == "" {
		doc.Title = name
	}
	doc.SetName(name)
	if id != "" {
		doc.SetID(id)
	} else {
		doc.SetFolder(folder) // a new document goes in the folder being shown
	}
	return doc, c, target, nil
}

// Save writes the current document to the target in the chosen format: as the
// saved document being edited if there is one, else as a new document.
func (s *Session) Save(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	return s.save(ctx)
}

func (s *Session) save(ctx context.Context) error {
	doc, c, target, err := s.currentIn()
	if err != nil {
		return err
	}
	e, err := s.Saver.Save(ctx, target, c, doc)
	if err != nil {
		return err
	}
	s.set(func() {
		if s.target == target { // not if the user has moved on to another target meanwhile
			s.id = e.ID
		}
	})
	return s.after(ctx, msg("saver.saved", "%s saved", e.Name))
}

// SaveAsNew saves the current document as a new document, with a new ID, leaving
// the one that was open as it was.
func (s *Session) SaveAsNew(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.Detach()
	return s.save(ctx)
}

// Open loads a saved document and hands it to OnOpen, taking its ID, name and
// format.
func (s *Session) Open(ctx context.Context, id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	target := s.targetName()
	doc, err := s.Saver.Load(ctx, target, id)
	if err != nil {
		return err
	}
	e, err := s.Saver.Get(ctx, target, id)
	if err != nil {
		return err
	}
	s.set(func() {
		s.id, s.name, s.folder = id, e.Name, e.Folder // show where it is
		if e.Format != nil {
			s.format = e.Format.Name()
		}
	})
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(doc) })
	}
	return s.after(ctx, msg("saver.opened", "%s opened", e.Name)) // the versions are those of the document just opened
}

// Delete removes a saved document for good, with no trash in between.
func (s *Session) Delete(ctx context.Context, id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	name := s.names([]string{id})
	if err := s.Saver.Delete(ctx, s.targetName(), id); err != nil {
		return err
	}
	s.set(func() {
		if s.id == id {
			s.id = ""
		}
	})
	return s.after(ctx, msg("saver.deleted", "%s deleted", name))
}

// SaveVersion snapshots the document being edited. It has to have been saved, to
// have an ID the snapshots are kept under.
func (s *Session) SaveVersion(ctx context.Context) error {
	if s.ID() == "" {
		return errors.New("save the document before saving a version of it")
	}
	doc, c, err := s.current()
	if err != nil {
		return err
	}
	if _, err := s.Saver.SaveVersion(ctx, s.targetName(), c, doc); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.versionSaved", "A version of %s saved", s.Name()))
}

// OpenVersion opens a snapshot in the editor. The document keeps its ID, so saving
// it afterwards replaces the saved one: go back, then Save.
func (s *Session) OpenVersion(ctx context.Context, key string) error {
	doc, err := s.Saver.LoadVersion(ctx, s.targetName(), key)
	if err != nil {
		return err
	}
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(doc) })
	}
	s.say(msg("saver.versionOpened", "Version opened"))
	return nil
}

// DeleteVersion removes one snapshot.
func (s *Session) DeleteVersion(ctx context.Context, key string) error {
	if err := s.Saver.DeleteVersion(ctx, s.targetName(), key); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.versionDeleted", "Version deleted"))
}

// TrashDocs moves saved documents to the trash.
func (s *Session) TrashDocs(ctx context.Context, ids []string) error {
	name := s.names(ids)
	if err := s.Saver.Trash(ctx, s.targetName(), ids...); err != nil {
		return err
	}
	s.set(func() {
		if slices.Contains(ids, s.id) {
			s.id = "" // the open document is in the trash: saving is a new document
		}
	})
	return s.after(ctx, msg("saver.trashed", "%s moved to the trash", name))
}

// Restore takes documents out of the trash (components.TrashBackend).
func (s *Session) Restore(ctx context.Context, ids ...string) error {
	name := s.names(ids)
	if err := s.Saver.Restore(ctx, s.targetName(), ids...); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.restored", "%s restored", name))
}

// Purge deletes documents in the trash for good (components.TrashBackend).
func (s *Session) Purge(ctx context.Context, ids ...string) error {
	name := s.names(ids)
	if err := s.Saver.Purge(ctx, s.targetName(), ids...); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.purged", "%s deleted for good", name))
}

// Duplicate saves a copy of a document, as a new document with its own ID.
func (s *Session) Duplicate(ctx context.Context, id string) error {
	e, err := s.Saver.Duplicate(ctx, s.targetName(), id, "")
	if err != nil {
		return err
	}
	return s.after(ctx, msg("saver.created", "%s created", e.Name))
}

// SetTags sets a saved document's tags.
func (s *Session) SetTags(ctx context.Context, id string, tags []string) error {
	name := s.names([]string{id})
	if err := s.Saver.SetTags(ctx, s.targetName(), id, tags); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.tagged", "Tags of %s updated", name))
}

// SetTagFilter narrows the list to the documents with the tag ("" for all).
func (s *Session) SetTagFilter(tag string) { s.set(func() { s.tagFilter = tag }) }

// OpenFolder shows a folder ("" for the top level); a new document is saved in it.
func (s *Session) OpenFolder(ctx context.Context, id string) error {
	s.mu.Lock()
	known := id == "" || slices.ContainsFunc(s.folders, func(f Folder) bool { return f.ID == id })
	s.mu.Unlock()
	if !known {
		return ErrFolder
	}
	s.set(func() { s.folder, s.folderDraft = id, "" })
	return nil
}

// NewFolder makes a folder in the one being shown.
func (s *Session) NewFolder(ctx context.Context, name string) error {
	s.mu.Lock()
	parent := s.folder
	s.mu.Unlock()
	if _, err := s.Saver.CreateFolder(ctx, s.targetName(), parent, name); err != nil {
		return err
	}
	s.set(func() { s.folderDraft = "" })
	return s.after(ctx, msg("saver.created", "%s created", name))
}

// RenameFolder renames a folder.
func (s *Session) RenameFolder(ctx context.Context, id, name string) error {
	if err := s.Saver.RenameFolder(ctx, s.targetName(), id, name); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.renamed", "%s renamed", name))
}

// DeleteFolder removes an empty folder; if it is the one being shown, the one
// above it is shown instead.
func (s *Session) DeleteFolder(ctx context.Context, id string) error {
	name := s.names([]string{id})
	if err := s.Saver.DeleteFolder(ctx, s.targetName(), id); err != nil {
		return err
	}
	s.mu.Lock()
	if s.folder == id {
		s.folder = ""
		for _, f := range s.folders {
			if f.ID == id {
				s.folder = f.Parent
			}
		}
	}
	s.mu.Unlock()
	return s.after(ctx, msg("saver.deleted", "%s deleted", name))
}

// MoveDocs puts documents in a folder ("" for the top level).
func (s *Session) MoveDocs(ctx context.Context, ids []string, folder string) error {
	name := s.names(ids)
	if err := s.Saver.Move(ctx, s.targetName(), folder, ids...); err != nil {
		return err
	}
	return s.after(ctx, msg("saver.moved", "%s moved", name))
}

// SetFolderDraft keeps what is typed in the new-folder box.
func (s *Session) SetFolderDraft(name string) { s.set(func() { s.folderDraft = name }) }

func docOf(e Entry) components.Doc {
	d := components.Doc{ID: e.ID, Name: e.Name, Size: e.Size, Updated: e.Updated, Trashed: e.Trashed, Tags: e.Tags}
	if e.Format != nil {
		d.Format = e.Format.Label()
	}
	return d
}

// Docs returns the props for components.DocList: the saved documents with the
// session's operations plugged in. Deleting for good happens at once; to ask first
// set Confirm to Asker.Ask and draw Asker.Dialog() somewhere (Props does both).
func (s *Session) Docs() components.DocListProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.DocListProps{
		Current: s.id, TagFilter: s.tagFilter, OnTagFilter: s.SetTagFilter, Pending: s.Pending,
		Open:      s.Open,
		Duplicate: s.Duplicate,
		Trash:     s.TrashDocs,
		Delete:    s.Delete,
		Tag:       s.SetTags,
		Refresh:   func() { s.Pending.Run(s.Refresh) },
	}
	for _, e := range s.entries {
		if e.Folder == s.folder { // the folder being shown
			p.Docs = append(p.Docs, docOf(e))
		}
	}
	for _, f := range s.folders {
		p.AllFolders = append(p.AllFolders, components.DocFolder{ID: f.ID, Name: f.Name, Parent: f.Parent})
		if f.Parent == s.folder {
			p.Folders = append(p.Folders, components.DocFolder{ID: f.ID, Name: f.Name, Parent: f.Parent})
		}
	}
	for _, f := range FolderPath(s.folders, s.folder) {
		p.Path = append(p.Path, components.DocFolder{ID: f.ID, Name: f.Name, Parent: f.Parent})
	}
	p.OpenFolder, p.NewFolder, p.RenameFolder, p.DeleteFolder, p.Move = s.OpenFolder, s.NewFolder, s.RenameFolder, s.DeleteFolder, s.MoveDocs
	p.FolderDraft, p.OnFolderDraft = s.folderDraft, s.SetFolderDraft
	if s.Deliver != nil {
		p.Download, p.DownloadFolder, p.DownloadFormats = s.Download, s.DownloadFolder, s.downloadChoices()
	}
	if s.Link != nil {
		p.Link = func(id string) string { return s.Link("document", id) }
		p.FolderLink = func(id string) string { return s.Link("folder", id) }
		p.CopyLink = s.CopyLink
	}
	return p
}

// Versions returns the props for components.Versions: the snapshots of the
// document being edited.
func (s *Session) Versions() components.VersionsProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.VersionsProps{Pending: s.Pending, Save: s.SaveVersion, Open: s.OpenVersion, Delete: s.DeleteVersion}
	if s.id != "" {
		p.Name = s.name
	}
	for _, v := range s.versions {
		p.Versions = append(p.Versions, components.DocVersion{Key: v.Key, Time: v.Time, Size: v.Size})
	}
	return p
}

// Trash returns the props for components.Trash: the documents in the trash, to
// restore or delete for good (at once; see Docs for asking first).
func (s *Session) Trash() components.TrashProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.TrashProps{Pending: s.Pending, Selected: s.selected, OnSelect: func(ids []string) { s.set(func() { s.selected = ids }) }}
	p = p.Use(s)
	for _, e := range s.trashed {
		p.Docs = append(p.Docs, docOf(e))
	}
	return p
}

// Props returns the props for components.Saver as the session stands.
func (s *Session) Props() components.SaverProps {
	docs, versions, trash := s.Docs(), s.Versions(), s.Trash()
	trash.OnSelect = nil // the form's trash is the simple one: a row, its buttons
	// The form draws the question's dialog, so here deleting for good asks first.
	docs.Confirm, trash.Confirm = s.Asker.Ask, s.Asker.Ask
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.SaverProps{
		Target: s.target, Format: s.format, Name: s.name,
		Status: s.status, Busy: s.Pending.Busy(),
		OnTarget:    func(n string) { s.Pending.Run(func(ctx context.Context) error { return s.SetTarget(ctx, n) }) },
		OnFormat:    s.SetFormat,
		OnName:      s.SetName,
		OnSave:      func() { s.Pending.Run(s.Save) },
		OnSaveAsNew: func() { s.Pending.Run(s.SaveAsNew) },
		Docs:        docs, Versions: versions, Trash: trash, Asker: s.Asker,
	}
	if err := s.Pending.Err(); err != nil {
		p.Status, p.Failed = err.Error(), true
	}
	p.Capacity = &components.CapacityProps{Used: s.usage.Used, Total: s.usage.Total, ShowCounts: true, Documents: s.usage.Documents, Trashed: s.usage.Trashed}
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
	return p
}

func (s *Session) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("saver session: %s as %s", s.target, s.format)
}

// Download hands a saved document over as a file: in the format, or as saved when
// format is "" or the session has no Export.
func (s *Session) Download(ctx context.Context, id, format string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	doc, err := s.Saver.Load(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	if format == "" || s.Export == nil {
		e, err := s.Saver.Get(ctx, s.targetName(), id)
		if err != nil {
			return err
		}
		c := e.Format
		if c == nil {
			c = docfile.JSON
		}
		data, err := c.Marshal(doc)
		if err != nil {
			return err
		}
		s.Deliver(safeName(doc.Name())+c.Ext(), "application/octet-stream", data)
	} else {
		file, data, mime, err := s.Export(format, doc)
		if err != nil {
			return err
		}
		s.Deliver(file, mime, data)
	}
	s.say(msg("saver.downloaded", "%s downloaded", doc.Name()))
	return nil
}

// DownloadFolder hands over a ZIP of a folder's documents ("" for all of them).
func (s *Session) DownloadFolder(ctx context.Context, id string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	data, name, err := s.Saver.Archive(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	s.Deliver(safeName(name)+".zip", "application/zip", data)
	s.say(msg("saver.downloaded", "%s downloaded", name))
	return nil
}

// CopyLink copies the link to a document or folder and says so.
func (s *Session) CopyLink(url string) {
	if s.Copy != nil {
		s.Copy(url)
		s.say(msg("saver.linkCopied", "Link copied: %s", url))
		return
	}
	s.say(url)
}

func (s *Session) downloadChoices() []components.DocChoice {
	var out []components.DocChoice
	if s.Export != nil {
		for _, c := range s.DownloadFormats {
			out = append(out, components.DocChoice(c))
		}
	}
	return out
}
