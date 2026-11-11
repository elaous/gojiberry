package saver

import (
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docdiff"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"slices"
	"strings"
	"sync"
	"time"
)

type Session struct {
	Saver *Saver
	// Get returns the document to save (the editor's current content).
	Get func() docfile.Document
	// OnOpen receives a document the user opened.
	OnOpen              func(docfile.Document)
	Author              string
	OnChange            func()
	OnUI                func(f func())
	Pending             *components.Pending
	Asker               *components.Asker
	Deliver             func(file, mime string, data []byte)
	Pick                func(accept string, done func(name string, data []byte, err error))
	Export              func(format string, doc docfile.Document) (file string, data []byte, mime string, err error)
	DownloadFormats     []components.SaverChoice
	Link                func(kind, id string) string
	Copy                func(text string)
	PackKeys            func() (seal func(ctx context.Context, docID string, plain []byte) ([]byte, error), open func(ctx context.Context, docID string, env []byte) ([]byte, error))
	ProtectionAvailable func(target string) bool

	op                   sync.Mutex // one saving, opening or deleting at a time
	mu                   sync.Mutex
	target, format, name string
	id                   string // the saved document being edited, "" for one never saved
	entries              []Entry
	folder               string   // the folder being shown, "" at the top level
	folders              []Folder // every folder
	folderDraft          string
	versions             []Version
	diff                 []docdiff.Change
	diffKey              string // the snapshot it was for
	trashed              []Entry
	selected             []string
	tagFilter            string
	usage                Usage
	revealed             map[string]revealedMeta
	revealedFolders      map[string]string // confidential folders opened here, by ID: the real name
	openDlg              *openDialog       // the .open download being asked about
	checked              map[string]bool   // targets already checked for leftovers of a crash
	status               string
	saveState            string // see SaveState
	edits                int
	base                 time.Time
}

type revealedMeta struct {
	name string
	tags []string
}

func NewSession(s *Saver, get func() docfile.Document, onOpen func(docfile.Document)) *Session {
	ss := &Session{Saver: s, Get: get, OnOpen: onOpen, format: docfile.JSON.Name()}
	ss.Pending = components.NewPending(ss.changed)
	ss.Asker = components.NewAsker(ss.changed)
	if ts := s.TargetList(); len(ts) > 0 {
		ss.target = ts[0].Name
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

func (s *Session) Target() string { return s.targetName() }

func (s *Session) targetName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target
}

// What SaveState says.
const (
	SaveSaved   = "saved"   // the editor holds what is saved
	SaveSaving  = "saving"  // a save is running
	SaveUnsaved = "unsaved" // edited since the last save or open
	SaveFailed  = "failed"  // the last save failed
)

func (s *Session) SaveState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveState
}

func (s *Session) unsaved() {
	if s.saveState != "" {
		s.saveState = SaveUnsaved
	}
}

func (s *Session) MarkEdited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits++
	if s.saveState != SaveSaving {
		s.saveState = SaveUnsaved
	}
}

func (s *Session) SetTarget(ctx context.Context, name string) error {
	s.set(func() {
		s.target, s.entries, s.trashed, s.versions, s.status, s.id = name, nil, nil, nil, "", ""
		s.revealed, s.revealedFolders = nil, nil
		s.folder, s.folders, s.usage = "", nil, Usage{}
		if s.saveState != "" {
			s.saveState = SaveUnsaved // the document is not in the new target
		}
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

func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *Session) Detach() { s.set(func() { s.id, s.base = "", time.Time{} }) }

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
		if s.target != target {
			return
		}
		s.entries, s.trashed, s.versions, s.folders = s.reveal(list), s.reveal(trashed), versions, s.revealFolders(folders)
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

func (s *Session) reveal(list []Entry) []Entry {
	out := make([]Entry, len(list))
	seen := map[string]bool{}
	for i, e := range list {
		out[i] = e
		if m, ok := s.revealed[e.ID]; ok && e.Protection == ProtectionE2EE {
			out[i].Name, out[i].Tags = m.name, m.tags
			seen[e.ID] = true
		}
	}
	for id := range s.revealed {
		if !seen[id] && slices.ContainsFunc(list, func(e Entry) bool { return e.ID == id }) {
			delete(s.revealed, id)
		}
	}
	return out
}

func (s *Session) remember(id, name string, tags []string) {
	s.set(func() {
		if s.revealed == nil {
			s.revealed = map[string]revealedMeta{}
		}
		s.revealed[id] = revealedMeta{name, tags}
	})
}

func allTags(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Tags...)
	}
	return out
}

func (s *Session) say(msg string) { s.set(func() { s.status = msg }) }

func msg(id, def string, args ...any) string {
	return fmt.Sprintf(components.Translate(id, def), args...)
}

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

func (s *Session) after(ctx context.Context, msg string) error {
	if err := s.Refresh(ctx); err != nil {
		return err
	}
	if note := s.capacityNote(); note != "" {
		msg += " " + note
	}
	s.say(msg)
	return nil
}

func (s *Session) capacityNote() string {
	s.mu.Lock()
	u := s.usage
	s.mu.Unlock()
	if !u.Known || u.Percent < components.CapacityWarnAt {
		return ""
	}
	if u.Percent >= components.CapacityFullAt {
		return msg("saver.storageFull", "Storage is %.0f%% full: download your documents now, then delete old versions and the trash.", u.Percent)
	}
	return msg("saver.storageFilling", "Storage is %.0f%% full: download your documents, or delete old versions and the trash, before it fills.", u.Percent)
}

func (s *Session) current() (docfile.Document, docfile.Codec, error) {
	doc, c, _, err := s.currentIn()
	return doc, c, err
}

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

func (s *Session) Save(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	return s.save(ctx)
}

type lockedError struct {
	text string
	err  error
}

func (e *lockedError) Error() string { return e.text }

func (e *lockedError) Unwrap() error { return e.err }

func storageFull(err error) error {
	if errors.Is(err, docstore.ErrLocked) {
		return &lockedError{msg("saver.locked", "This document is confidential or the store is locked, and no key that is unlocked here opens it. Unlock the store with one of your keys (Save tab), then try again."), err}
	}
	if errors.Is(err, docstore.ErrStorageFull) {
		return fmt.Errorf("%s: %w", msg("saver.notSavedFull", "Not saved: the storage is full. Download the document (Download as) so no work is lost, then delete old versions or the trash, or save somewhere else"), err)
	}
	return err
}

func (s *Session) save(ctx context.Context) (err error) {
	var edits int
	stored := false // the document is saved, whatever happens after (the list refresh)
	s.set(func() { s.saveState, edits = SaveSaving, s.edits })
	defer func() {
		s.set(func() {
			switch {
			case err != nil && !stored:
				s.saveState = SaveFailed
			case s.edits != edits:
				s.saveState = SaveUnsaved // edited while saving
			default:
				s.saveState = SaveSaved
			}
		})
	}()
	doc, c, target, err := s.currentIn()
	if err != nil {
		return err
	}
	elsewhere, keptKey := s.keepElsewhere(ctx, target, c, doc)
	e, err := s.Saver.Save(ctx, target, c, doc)
	if err != nil {
		return storageFull(err)
	}
	stored = true
	s.set(func() {
		if s.target == target {
			s.id, s.base = e.ID, e.Updated
		}
	})
	name := e.Name
	if e.Protection == ProtectionE2EE {
		name = doc.Name()
		_, tags, _ := s.Saver.Reveal(ctx, target, e.ID)
		s.remember(e.ID, name, tags)
	}
	if keptKey != "" {
		s.set(func() { s.diff, s.diffKey = elsewhere, keptKey })
		return s.after(ctx, msg("saver.savedOverNewer", "%s saved. It replaced a newer copy saved elsewhere; that copy is kept as a version and compared with yours in Versions", name))
	}
	return s.after(ctx, msg("saver.saved", "%s saved", name))
}

const elsewhereVersion = "Saved elsewhere, before your save"

func (s *Session) keepElsewhere(ctx context.Context, target string, c docfile.Codec, doc docfile.Document) (changes []docdiff.Change, key string) {
	id := doc.ID()
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()
	if id == "" || base.IsZero() {
		return nil, ""
	}
	cur, err := s.Saver.Get(ctx, target, id)
	if err != nil || !cur.Updated.After(base) {
		return nil, ""
	}
	theirs, _, err := s.Saver.LoadChecked(ctx, target, id)
	if err != nil {
		return nil, ""
	}
	changes = docdiff.Compare(theirs, doc)
	if !docdiff.Summarize(changes).Changed() {
		return nil, ""
	}
	v, err := s.Saver.SaveVersionBy(ctx, target, c, theirs, "")
	if err != nil {
		return nil, ""
	}
	_ = s.Saver.RenameVersion(ctx, target, v.Key, elsewhereVersion)
	return changes, v.Key
}

func (s *Session) SaveAsNew(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.Detach()
	return s.save(ctx)
}

func (s *Session) Open(ctx context.Context, id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	target := s.targetName()
	doc, rec, err := s.Saver.LoadChecked(ctx, target, id)
	if err != nil {
		return storageFull(err)
	}
	e, err := s.Saver.Get(ctx, target, id)
	if err != nil {
		return err
	}
	name := e.Name
	if e.Protection == ProtectionE2EE {
		name = doc.Name()
		s.remember(id, name, doc.Tags())
	}
	s.set(func() {
		s.id, s.name, s.folder, s.base = id, name, e.Folder, e.Updated // show where it is
		if e.Format != nil {
			s.format = e.Format.Name()
		}
	})
	if s.OnOpen != nil {
		s.ui(func() { s.OnOpen(doc) })
	}
	s.set(func() { s.saveState = SaveSaved; s.edits++ }) // the editor holds what was saved
	line := msg("saver.opened", "%s opened", name)
	if rec.FromPrevious {
		line = msg("saver.recovered", "%s was damaged: opened the copy saved %s (it is now the saved one)", name, rec.Saved.Format("2006-01-02 15:04"))
	}
	return s.after(ctx, line) // the versions are those of the document just opened
}

func (s *Session) Check(ctx context.Context) error {
	s.op.Lock()
	defer s.op.Unlock()
	rep, err := s.Saver.Verify(ctx, s.targetName())
	if err != nil {
		return err
	}
	line := msg("saver.checkedOK", "Checked %d documents: all fine", rep.Checked)
	if len(rep.Restored) > 0 || len(rep.Lost) > 0 {
		line = msg("saver.checkedProblems", "Checked %d documents: %d put back from an earlier copy (%s), %d damaged with no copy (%s)",
			rep.Checked, len(rep.Restored), strings.Join(rep.Restored, ", "), len(rep.Lost), strings.Join(rep.Lost, ", "))
	}
	if len(rep.BackupsLost) > 0 {
		line += " " + msg("saver.checkedBackups", "(%d had no good previous copy; the next save makes one)", len(rep.BackupsLost))
	}
	if rep.Orphans > 0 {
		line += " " + msg("saver.checkedOrphans", "(%d leftover files removed)", rep.Orphans)
	}
	return s.after(ctx, line)
}

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
			s.unsaved() // what is in the editor is no longer stored anywhere
		}
	})
	return s.after(ctx, msg("saver.deleted", "%s deleted", name))
}

// Props returns the props for components.Saver as the session stands.
func (s *Session) Props() components.SaverProps {
	docs, versions, trash := s.Docs(), s.Versions(), s.Trash()
	trash.OnSelect = nil
	docs.Confirm, trash.Confirm = s.Asker.Ask, s.Asker.Ask
	docs.ConfirmAs = s.Asker.AskWith
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
	for _, t := range s.Saver.TargetList() {
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
