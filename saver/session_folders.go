package saver

import (
	"context"
	"github.com/elaous/gojiberry/components"
	"slices"
	"strings"
)

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

func (s *Session) concealedFolders(list []Folder) []Folder {
	out := make([]Folder, len(list))
	for i, f := range list {
		out[i] = f
		if f.Protection == ProtectionE2EE {
			out[i].Name = ConfidentialFolderName
		}
	}
	return out
}

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
		return storageFull(err)
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

// docFolder is docFolderOf with what the session knows (call with the lock held).
func (s *Session) docFolder(f Folder) components.DocFolder {
	d := docFolderOf(f)
	_, ok := s.revealedFolders[f.ID]
	d.NameHidden = d.Confidential && !ok
	return d
}
