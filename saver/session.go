package saver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
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
	// Pick asks the person for a file (in a browser: transfer.PickFile); with it the document
	// list offers "Import .opendoc".
	Pick func(accept string, done func(name string, data []byte, err error))
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
	revealed             map[string]revealedMeta // confidential documents opened here, by ID: the real name and tags
	revealedFolders      map[string]string       // confidential folders opened here, by ID: the real name
	openDlg              *openDialog             // the .open download being asked about
	checked              map[string]bool         // targets already checked for leftovers of a crash
	status               string
	saveState            string // see SaveState
	edits                int    // MarkEdited calls, to tell whether the document changed during a save
}

// revealedMeta is what a confidential document's catalog entry hides, known here because the
// document was opened (or saved) with its key.
type revealedMeta struct {
	name string
	tags []string
}

// NewSession starts a session on the saver's first target, in JSON.
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

// SaveState tells where saving stands, for a status bar: "" for a document never
// saved or opened and not yet edited, else SaveSaved, SaveSaving, SaveUnsaved or
// SaveFailed.
func (s *Session) SaveState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveState
}

// unsaved marks the editor's content as not stored, if the session has been tracking
// it (call with the lock held).
func (s *Session) unsaved() {
	if s.saveState != "" {
		s.saveState = SaveUnsaved
	}
}

// MarkEdited tells the session the document in the editor changed (call it from
// the editor's change callback). It does not render again: the host is already
// rendering for the edit.
func (s *Session) MarkEdited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits++
	if s.saveState != SaveSaving {
		s.saveState = SaveUnsaved
	}
}

// SetTarget chooses the target and lists what is saved in it. The document being
// edited is not in the new target, so it is saved there as a new document.
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

// reveal puts the real name and tags of the confidential documents opened in this session
// into a list read from the catalog, which lists the placeholder and no tags, so an
// unopened one shows (and filters, and searches by) nothing of what is hidden. An entry that
// is not confidential any more drops out of the memory (call with the lock held).
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

// revealFolders puts the real names of the confidential folders opened in this session into a
// list read from the catalog, which lists the placeholder; one not opened here keeps it. A
// folder that is not confidential any more or is gone drops out of the memory (call with the
// lock held).
func (s *Session) revealFolders(list []Folder) []Folder {
	out := make([]Folder, len(list))
	seen := map[string]bool{}
	for i, f := range list {
		out[i] = f
		if name, ok := s.revealedFolders[f.ID]; ok && f.Protection == ProtectionE2EE {
			out[i].Name = name
			seen[f.ID] = true
		}
	}
	for id := range s.revealedFolders {
		if !seen[id] {
			delete(s.revealedFolders, id)
		}
	}
	return out
}

// rememberFolder keeps the real name of a confidential folder for the list.
func (s *Session) rememberFolder(id, name string) {
	s.set(func() {
		if s.revealedFolders == nil {
			s.revealedFolders = map[string]string{}
		}
		s.revealedFolders[id] = name
	})
}

// remember keeps the real name and tags of a confidential document for the list.
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
	if note := s.capacityNote(); note != "" {
		msg += " " + note
	}
	s.say(msg)
	return nil
}

// capacityNote is a sentence for the status line when the store is filling up, and ""
// when it is not (or its size is not known).
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

// storageFull adds to an error from a write the advice for a full store, when that is
// what it is, so every way of saving says the same.
func storageFull(err error) error {
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
	e, err := s.Saver.Save(ctx, target, c, doc)
	if err != nil {
		return storageFull(err)
	}
	stored = true
	s.set(func() {
		if s.target == target { // not if the user has moved on to another target meanwhile
			s.id = e.ID
		}
	})
	name := e.Name
	if e.Protection == ProtectionE2EE {
		// The catalog lists the placeholder; the person knows what they saved, and the
		// list shows it from now on.
		name = doc.Name()
		_, tags, _ := s.Saver.Reveal(ctx, target, e.ID)
		s.remember(e.ID, name, tags)
	}
	return s.after(ctx, msg("saver.saved", "%s saved", name))
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
	doc, rec, err := s.Saver.LoadChecked(ctx, target, id)
	if err != nil {
		return err
	}
	e, err := s.Saver.Get(ctx, target, id)
	if err != nil {
		return err
	}
	name := e.Name
	if e.Protection == ProtectionE2EE {
		// Opened, so it can be read: the placeholder gives way to the real name, here and in
		// the list.
		name = doc.Name()
		s.remember(id, name, doc.Tags())
	}
	s.set(func() {
		s.id, s.name, s.folder = id, name, e.Folder // show where it is
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
		// Say so: the text is an earlier save, and the person should know.
		line = msg("saver.recovered", "%s was damaged: opened the copy saved %s (it is now the saved one)", name, rec.Saved.Format("2006-01-02 15:04"))
	}
	return s.after(ctx, line) // the versions are those of the document just opened
}

// Check reads every saved document and compares it with its checksum, putting back from
// its previous copy any that is missing or damaged (Saver.Verify), and says what it
// found and did.
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
			s.unsaved() // what is in the editor is no longer stored anywhere
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
		return storageFull(err)
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
			s.unsaved()
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
		return storageFull(err)
	}
	return s.after(ctx, msg("saver.created", "%s created", e.Name))
}

// SetTags sets a saved document's tags.
func (s *Session) SetTags(ctx context.Context, id string, tags []string) error {
	name := s.names([]string{id})
	if err := s.Saver.SetTags(ctx, s.targetName(), id, tags); err != nil {
		return storageFull(err)
	}
	s.set(func() {
		if m, ok := s.revealed[id]; ok { // a confidential document: the new tags are in the document
			var clean docfile.Document
			clean.SetTags(tags)
			s.revealed[id] = revealedMeta{m.name, clean.Tags()}
		}
	})
	return s.after(ctx, msg("saver.tagged", "Tags of %s updated", name))
}

// SetProtection makes a saved document confidential (docfile.ProtectionE2EE: its name and
// tags move into the document, the catalog lists a placeholder) or ProtectionServer again,
// and shows its real name in the list while it is openable here.
func (s *Session) SetProtection(ctx context.Context, id, to string) error {
	s.op.Lock()
	defer s.op.Unlock()
	target := s.targetName()
	name := s.names([]string{id})
	if err := s.Saver.SetProtection(ctx, target, id, to); err != nil {
		return storageFull(err)
	}
	if to == ProtectionE2EE {
		if n, tags, err := s.Saver.Reveal(ctx, target, id); err == nil {
			s.remember(id, n, tags)
		}
		return s.after(ctx, msg("saver.confidential", "%s is now confidential: its name and tags are kept inside the document", name))
	}
	return s.after(ctx, msg("saver.notConfidential", "%s is no longer confidential", name))
}

// Reveal shows the real name and tags of a confidential document in the list, if it can be
// opened here; the document is not opened in the editor.
func (s *Session) Reveal(ctx context.Context, id string) error {
	name, tags, err := s.Saver.Reveal(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	if e, err := s.Saver.Get(ctx, s.targetName(), id); err == nil && e.Protection == ProtectionE2EE {
		s.remember(id, name, tags)
	}
	return s.after(ctx, msg("saver.revealed", "%s can be read: its name is shown", name))
}

// SetTagFilter narrows the list to the documents with the tag ("" for all).
func (s *Session) SetTagFilter(tag string) { s.set(func() { s.tagFilter = tag }) }

// SetFolderProtection makes a folder confidential (its name moves into an encrypted record,
// the catalog lists the placeholder) or ProtectionServer again, and shows its real name in the
// list while it is openable here. With recursive the folders and documents inside get the
// same level; without it they keep theirs.
func (s *Session) SetFolderProtection(ctx context.Context, id, to string, recursive bool) error {
	s.op.Lock()
	defer s.op.Unlock()
	target := s.targetName()
	name := s.names([]string{id})
	if err := s.Saver.SetFolderProtection(ctx, target, id, to, recursive); err != nil {
		return storageFull(err)
	}
	if to == ProtectionE2EE {
		if n, err := s.Saver.RevealFolder(ctx, target, id); err == nil {
			s.rememberFolder(id, n)
			name = n
		}
		return s.after(ctx, msg("saver.folderConfidential", "%s is now a confidential folder: its name is kept inside the envelope", name))
	}
	return s.after(ctx, msg("saver.folderNotConfidential", "%s is no longer a confidential folder", name))
}

// RevealFolder shows the real name of a confidential folder in the list, if it can be opened
// here.
func (s *Session) RevealFolder(ctx context.Context, id string) error {
	name, err := s.Saver.RevealFolder(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	if f, err := s.Saver.folder(ctx, s.targetName(), id); err == nil && f.Protection == ProtectionE2EE {
		s.rememberFolder(id, name)
	}
	return s.after(ctx, msg("saver.revealed", "%s can be read: its name is shown", name))
}

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
		return storageFull(err)
	}
	s.set(func() { s.folderDraft = "" })
	return s.after(ctx, msg("saver.created", "%s created", name))
}

// RenameFolder renames a folder. The new name of a confidential folder goes into its
// encrypted record, and is shown here since the person just typed it.
func (s *Session) RenameFolder(ctx context.Context, id, name string) error {
	if err := s.Saver.RenameFolder(ctx, s.targetName(), id, name); err != nil {
		return storageFull(err)
	}
	s.set(func() {
		if _, ok := s.revealedFolders[id]; ok {
			s.revealedFolders[id] = name
		}
	})
	return s.after(ctx, msg("saver.renamed", "%s renamed", name))
}

// DeleteFolder removes an empty folder; if it is the one being shown, the one
// above it is shown instead.
func (s *Session) DeleteFolder(ctx context.Context, id string) error {
	name := s.names([]string{id})
	purged, err := s.Saver.DeleteFolderReport(ctx, s.targetName(), id)
	if err != nil {
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
	line := msg("saver.deleted", "%s deleted", name)
	if len(purged) > 0 {
		// Say what else went, so nothing disappears unexplained.
		shown := purged
		if len(shown) > 5 {
			shown = append(append([]string(nil), purged[:5]...), "…")
		}
		line += " " + msg("saver.purgedUnreadable", "(%d documents in the trash whose files were gone were removed: %s)", len(purged), strings.Join(shown, ", "))
	}
	return s.after(ctx, line)
}

// MoveDocs puts documents in a folder ("" for the top level).
func (s *Session) MoveDocs(ctx context.Context, ids []string, folder string) error {
	name := s.names(ids)
	if err := s.Saver.Move(ctx, s.targetName(), folder, ids...); err != nil {
		return storageFull(err)
	}
	return s.after(ctx, msg("saver.moved", "%s moved", name))
}

// SetFolderDraft keeps what is typed in the new-folder box.
func (s *Session) SetFolderDraft(name string) { s.set(func() { s.folderDraft = name }) }

func docFolderOf(f Folder) components.DocFolder {
	return components.DocFolder{ID: f.ID, Name: f.Name, Parent: f.Parent, Confidential: f.Protection == ProtectionE2EE}
}

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
		Check:     s.Check,
	}
	for _, e := range s.entries {
		if e.Folder == s.folder { // the folder being shown
			p.Docs = append(p.Docs, docOf(e))
		}
	}
	for _, f := range s.folders {
		p.AllFolders = append(p.AllFolders, docFolderOf(f))
		if f.Parent == s.folder {
			p.Folders = append(p.Folders, docFolderOf(f))
		}
	}
	for _, f := range FolderPath(s.folders, s.folder) {
		p.Path = append(p.Path, docFolderOf(f))
	}
	p.OpenFolder, p.NewFolder, p.RenameFolder, p.DeleteFolder, p.Move = s.OpenFolder, s.NewFolder, s.RenameFolder, s.DeleteFolder, s.MoveDocs
	p.FolderDraft, p.OnFolderDraft = s.folderDraft, s.SetFolderDraft
	if d := s.openDlg; d != nil {
		p.OpenDownload = &components.OpenDownload{
			Name: d.name, Current: d.sizes.Current, History: d.sizes.History, Versions: d.sizes.Versions, Drop: d.drop,
			OnDrop: func(drop bool) {
				s.set(func() {
					if s.openDlg != nil {
						s.openDlg.drop = drop
					}
				})
			},
			OnConfirm: func() { s.Pending.Run(s.DownloadOpen) },
			OnCancel:  s.CancelOpenDownload,
		}
	}
	if s.Pick != nil {
		p.ImportPack = s.pickPack
	}
	if s.Deliver != nil {
		p.DownloadPack = s.DownloadPack
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
	if format == docfile.Open.Name() {
		// Ask first whether to keep the history in the file, and say how big each part is.
		sizes, err := s.Saver.OpenSizes(ctx, s.targetName(), id)
		if err != nil {
			return err
		}
		s.set(func() { s.openDlg = &openDialog{id: id, name: doc.Name(), sizes: sizes} })
		return nil
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

// openDialog is the question asked before an .open download.
type openDialog struct {
	id, name string
	sizes    OpenSizes
	drop     bool
}

// DownloadOpen delivers the .open file the dialog asked about, with or without the history
// (as the person chose), and closes the dialog.
func (s *Session) DownloadOpen(ctx context.Context) error {
	s.mu.Lock()
	dlg := s.openDlg
	s.mu.Unlock()
	if dlg == nil {
		return nil
	}
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	data, err := s.Saver.ExportOpen(ctx, s.targetName(), dlg.id, !dlg.drop)
	if err != nil {
		return err
	}
	s.Deliver(safeName(dlg.name)+docfile.Open.Ext(), docfile.Open.MIME(), data)
	s.set(func() { s.openDlg = nil })
	s.say(msg("saver.downloaded", "%s downloaded", dlg.name))
	return nil
}

// DownloadPack hands over a folder ("" for all the documents) as one .opendoc pack: folders,
// tags, the trash flag and each document's history.
func (s *Session) DownloadPack(ctx context.Context, folder string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	var buf bytes.Buffer
	name := s.folderName(folder)
	// The session holds no keys: confidential documents are left out, and the person is told.
	left := 0
	n, err := s.Saver.ExportPack(ctx, s.targetName(), folder, &buf, PackOptions{Name: name, History: true, Warn: func(PackIssue) { left++ }})
	if err != nil {
		return err
	}
	s.Deliver(safeName(name)+".opendoc", "application/vnd.opendocs.pack", buf.Bytes())
	line := msg("saver.packDownloaded", "%s downloaded as a pack of %d documents", name, n)
	if left > 0 {
		line += " " + msg("saver.packLeftOut", "(%d confidential documents left out: they need a key)", left)
	}
	s.say(line)
	return nil
}

func (s *Session) folderName(id string) string {
	if id == "" {
		return "documents"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.folders {
		if f.ID == id {
			if f.Protection == ProtectionE2EE {
				return ConfidentialFolderName // the pack's name is in the clear: not the real one
			}
			return f.Name
		}
	}
	return "documents"
}

// pickPack asks for a .opendoc file and imports it.
func (s *Session) pickPack() {
	s.Pick(".opendoc", func(name string, data []byte, err error) {
		s.Pending.Run(func(ctx context.Context) error {
			if err != nil {
				return err
			}
			return s.ImportPackData(ctx, name, data)
		})
	})
}

// ImportPackData imports the .opendoc pack in data into the folder being shown, keeping what is
// already there (a document with the same ID is always skipped: the UI offers no other policy), and says what came in, what was
// skipped and what failed, and whether the pack was signed.
func (s *Session) ImportPackData(ctx context.Context, name string, data []byte) error {
	s.op.Lock()
	defer s.op.Unlock()
	target, folder := s.Where()
	c, err := s.Saver.Codec(s.formatName())
	if err != nil {
		return err
	}
	rep, err := s.Saver.ImportPack(ctx, target, bytes.NewReader(data), int64(len(data)), ImportOptions{Into: folder, Format: c})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	line := msg("saver.packImported", "%s: %d documents and %d folders imported", name, rep.Documents, rep.Folders)
	if len(rep.Skipped) > 0 {
		line += " " + msg("saver.packSkipped", "(%d already here, left as they are)", len(rep.Skipped))
	}
	if len(rep.Warnings) > 0 {
		line += " " + msg("saver.packWarnings", "(%d warnings: %s)", len(rep.Warnings), strings.Join(rep.Warnings[:min(3, len(rep.Warnings))], "; "))
	}
	if len(rep.Failed) > 0 {
		line += " " + msg("saver.packFailed", "(%d failed: %s)", len(rep.Failed), strings.Join(rep.Failed[:min(3, len(rep.Failed))], "; "))
	}
	if rep.Signed {
		if rep.SignatureOK {
			line += " " + msg("saver.packSigned", "(signed by key %x)", rep.Signer[:min(8, len(rep.Signer))])
		} else {
			line += " " + msg("saver.packBadSignature", "(WARNING: the signature does not match the pack)")
		}
	}
	return s.after(ctx, line)
}

func (s *Session) formatName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.format
}

// CancelOpenDownload closes the dialog without downloading.
func (s *Session) CancelOpenDownload() { s.set(func() { s.openDlg = nil }) }

// DownloadFolder hands over a ZIP of a folder's documents ("" for all of them).
func (s *Session) DownloadFolder(ctx context.Context, id string) error {
	if s.Deliver == nil {
		return errors.New("saver: nowhere to deliver files")
	}
	data, name, skipped, err := s.Saver.ArchiveReport(ctx, s.targetName(), id)
	if err != nil {
		return err
	}
	s.Deliver(safeName(name)+".zip", "application/zip", data)
	if len(skipped) > 0 {
		s.say(msg("saver.downloadedSkipped", "%s downloaded without %d confidential document(s) that are locked here", name, len(skipped)))
		return nil
	}
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

// Where is the target and folder a new document would be saved in.
func (s *Session) Where() (target, folder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target, s.folder
}
