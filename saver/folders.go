package saver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// Folder holds documents and other folders. Like a document it has an ID (a UUID)
// and a name that need not be unique; Parent is the ID of the folder it is in, ""
// at the top level. The catalog keeps the folders (and each is also written as a
// record of its own, so a rebuilt catalog finds the empty ones).
type Folder struct {
	ID, Name, Parent string
}

// MaxFolderDepth is how deep folders nest.
const MaxFolderDepth = 16

// ErrFolder is returned for a folder that is not there, or a move that would put a
// folder inside itself.
var ErrFolder = errors.New("saver: no such folder, or it cannot go there")

func (c *catalog) depth(id string) int {
	n := 0
	for id != "" && n <= MaxFolderDepth+1 {
		id = c.Folders[id].Parent
		n++
	}
	return n
}

// inside reports whether folder id is in ancestor (or is it).
func (c *catalog) inside(id, ancestor string) bool {
	for n := 0; id != "" && n <= MaxFolderDepth+1; n++ {
		if id == ancestor {
			return true
		}
		id = c.Folders[id].Parent
	}
	return false
}

func (s *Saver) writeFolder(ctx context.Context, t Target, id string, f folderEntry) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return t.Adapter.Save(ctx, FolderRecordPrefix+id, data)
}

// CreateFolder makes a folder called name in parent ("" for the top level).
func (s *Saver) CreateFolder(ctx context.Context, target, parent, name string) (Folder, error) {
	if !ValidName(name) {
		return Folder{}, ErrName
	}
	var out Folder
	err := s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		if _, ok := cat.Folders[parent]; parent != "" && !ok {
			return ErrFolder
		}
		if cat.depth(parent) >= MaxFolderDepth {
			return fmt.Errorf("saver: folders nest at most %d deep", MaxFolderDepth)
		}
		id := docfile.NewID()
		f := folderEntry{Name: name, Parent: parent}
		if err := s.writeFolder(ctx, t, id, f); err != nil {
			return err
		}
		cat.Folders[id] = f
		out = Folder{ID: id, Name: name, Parent: parent}
		return nil
	})
	return out, err
}

// Folders lists every folder, by name.
func (s *Saver) Folders(ctx context.Context, target string) ([]Folder, error) {
	var out []Folder
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		out = cat.folderList()
		return nil
	})
	return out, err
}

func (c *catalog) folderList() []Folder {
	out := make([]Folder, 0, len(c.Folders))
	for id, f := range c.Folders {
		out = append(out, Folder{ID: id, Name: f.Name, Parent: f.Parent})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RenameFolder gives a folder a new name.
func (s *Saver) RenameFolder(ctx context.Context, target, id, name string) error {
	if !ValidName(name) {
		return ErrName
	}
	return s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		f, ok := cat.Folders[id]
		if !ok {
			return ErrFolder
		}
		f.Name = name
		if err := s.writeFolder(ctx, t, id, f); err != nil {
			return err
		}
		cat.Folders[id] = f
		return nil
	})
}

// MoveFolder puts a folder in another ("" for the top level); not into itself
// or one of its own subfolders.
func (s *Saver) MoveFolder(ctx context.Context, target, id, parent string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		f, ok := cat.Folders[id]
		if !ok {
			return ErrFolder
		}
		if _, ok := cat.Folders[parent]; parent != "" && !ok || cat.inside(parent, id) {
			return ErrFolder
		}
		// The deepest folder under id decides whether it still fits.
		deepest := 0
		for other := range cat.Folders {
			if cat.inside(other, id) {
				deepest = max(deepest, cat.depth(other)-cat.depth(id))
			}
		}
		if cat.depth(parent)+1+deepest > MaxFolderDepth {
			return fmt.Errorf("saver: folders nest at most %d deep", MaxFolderDepth)
		}
		f.Parent = parent
		if err := s.writeFolder(ctx, t, id, f); err != nil {
			return err
		}
		cat.Folders[id] = f
		return nil
	})
}

// DeleteFolder removes a folder that holds no document (not in the trash) and no
// folder. Documents in the trash that were in it go back to the top level.
func (s *Saver) DeleteFolder(ctx context.Context, target, id string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		if _, ok := cat.Folders[id]; !ok {
			return ErrFolder
		}
		for _, f := range cat.Folders {
			if f.Parent == id {
				return errors.New("saver: the folder has folders in it")
			}
		}
		var gone []string
		for did, e := range cat.Docs {
			if e.Folder != id {
				continue
			}
			if e.Trashed == 0 {
				return errors.New("saver: the folder has documents in it")
			}
			gone = append(gone, did)
		}
		for _, did := range gone {
			e := cat.Docs[did]
			if err := s.touch(ctx, t, &e, func(d *docfile.Document) { d.SetFolder("") }); err != nil {
				return err
			}
			e.Folder = ""
			cat.Docs[did] = e
		}
		delete(cat.Folders, id)
		*later = append(*later, func() { _ = t.Adapter.Delete(ctx, FolderRecordPrefix+id) })
		return nil
	})
}

// Move puts documents in a folder ("" for the top level). It changes nothing
// unless every document and the folder are there.
func (s *Saver) Move(ctx context.Context, target, folder string, ids ...string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		if _, ok := cat.Folders[folder]; folder != "" && !ok {
			return ErrFolder
		}
		for _, id := range ids {
			if _, ok := cat.Docs[id]; !ok {
				return fmt.Errorf("%w: %s", docstore.ErrNotFound, id)
			}
		}
		for _, id := range ids {
			e := cat.Docs[id]
			if err := s.touch(ctx, t, &e, func(d *docfile.Document) { d.SetFolder(folder) }); err != nil {
				return err
			}
			e.Folder = folder
			cat.Docs[id] = e
		}
		return nil
	})
}

// FolderPath is the folders from the top level down to id, for a breadcrumb; empty
// for the top level.
func FolderPath(folders []Folder, id string) []Folder {
	byID := map[string]Folder{}
	for _, f := range folders {
		byID[f.ID] = f
	}
	var path []Folder
	for n := 0; id != "" && n <= MaxFolderDepth+1; n++ {
		f, ok := byID[id]
		if !ok {
			break
		}
		path = append([]Folder{f}, path...)
		id = f.Parent
	}
	return path
}
