package saver

import (
	"context"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"slices"
)

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
		return storageFull(err)
	}
	if e, err := s.Saver.Get(ctx, s.targetName(), id); err == nil && e.Protection == ProtectionE2EE {
		s.remember(id, name, tags)
	}
	return s.after(ctx, msg("saver.revealed", "%s can be read: its name is shown", name))
}

// Clear forgets the document being edited and everything the session shows of it: the saved
// document's ID and name, the tag filter, the selection, the revealed names and tags, the lists
// and the status. The editor's own content is the host's to replace (Clear does not touch what Get
// returns). Call it with Forget-like reasons: when the keys are forgotten and the document on
// screen must go with them.
func (s *Session) Clear() {
	s.set(func() {
		s.id, s.name, s.tagFilter, s.status, s.selected, s.folderDraft = "", "", "", "", nil, ""
		s.revealed, s.revealedFolders, s.versions, s.openDlg = nil, nil, nil, nil
		s.entries, s.trashed, s.folders = s.concealed(s.entries), s.concealed(s.trashed), s.concealedFolders(s.folders)
		s.saveState = ""
	})
}

// Forget drops the real names and tags of the confidential documents and folders that were
// revealed here, so the lists show the placeholders again: call it when the keys are forgotten
// (the store is locked). The saver's own state is not touched; a later Reveal, Open or Save
// shows them again.
func (s *Session) Forget() {
	s.set(func() {
		s.revealed, s.revealedFolders = nil, nil
		s.entries, s.trashed, s.folders = s.concealed(s.entries), s.concealed(s.trashed), s.concealedFolders(s.folders)
	})
}

// concealed puts the placeholder back in the confidential entries of a list (call with the lock
// held).
func (s *Session) concealed(list []Entry) []Entry {
	out := make([]Entry, len(list))
	for i, e := range list {
		out[i] = e
		if e.Protection == ProtectionE2EE {
			out[i].Name, out[i].Tags = ConfidentialName, nil
		}
	}
	return out
}

// SetTagFilter narrows the list to the documents with the tag ("" for all).
func (s *Session) SetTagFilter(tag string) { s.set(func() { s.tagFilter = tag }) }

// docOf is an entry as the components list it; revealed says whether the real name of a
// confidential one is known here (the list shows the placeholder, and offers Reveal, if not).
func docOf(e Entry, revealed bool) components.Doc {
	d := components.Doc{ID: e.ID, Name: e.Name, Size: e.Size, Updated: e.Updated, Trashed: e.Trashed, Tags: e.Tags}
	if e.Format != nil {
		d.Format = e.Format.Label()
	}
	if e.Protection == ProtectionE2EE {
		d.Confidential, d.NameHidden = true, !revealed
	}
	return d
}

// docEntry is docOf with what the session knows (call with the lock held).
func (s *Session) docEntry(e Entry) components.Doc {
	_, ok := s.revealed[e.ID]
	return docOf(e, ok)
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
		Reveal:    s.Reveal, RevealFolder: s.RevealFolder,
	}
	if s.ProtectionAvailable == nil || s.ProtectionAvailable(s.target) {
		p.SetProtection = func(ctx context.Context, id string, confidential bool) error {
			return s.SetProtection(ctx, id, protectionLevel(confidential))
		}
		// A folder's level moves its own name only: what is inside keeps its protection, so each
		// document is switched on its own row.
		p.SetFolderProtection = func(ctx context.Context, id string, confidential bool) error {
			return s.SetFolderProtection(ctx, id, protectionLevel(confidential), false)
		}
	}
	for _, e := range s.entries {
		if e.Folder == s.folder { // the folder being shown
			p.Docs = append(p.Docs, s.docEntry(e))
		}
	}
	for _, f := range s.folders {
		p.AllFolders = append(p.AllFolders, s.docFolder(f))
		if f.Parent == s.folder {
			p.Folders = append(p.Folders, s.docFolder(f))
		}
	}
	for _, f := range FolderPath(s.folders, s.folder) {
		p.Path = append(p.Path, s.docFolder(f))
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

func protectionLevel(confidential bool) string {
	if confidential {
		return ProtectionE2EE
	}
	return ProtectionServer
}

// Trash returns the props for components.Trash: the documents in the trash, to
// restore or delete for good (at once; see Docs for asking first).
func (s *Session) Trash() components.TrashProps {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := components.TrashProps{Pending: s.Pending, Selected: s.selected, OnSelect: func(ids []string) { s.set(func() { s.selected = ids }) }}
	p = p.Use(s)
	for _, e := range s.trashed {
		p.Docs = append(p.Docs, s.docEntry(e))
	}
	return p
}
