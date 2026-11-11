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

// Folder holds documents and other folders.
type Folder struct {
	ID, Name, Parent string
	Protection       string
}

// MaxFolderDepth is how deep folders nest.
const MaxFolderDepth = 16

var ErrFolder = errors.New("saver: no such folder, or it cannot go there")

var ErrFolderID = errors.New("saver: a folder cannot have a document's ID")

func (c *catalog) depth(id string) int {
	n := 0
	for id != "" && n <= MaxFolderDepth+1 {
		id = c.Folders[id].Parent
		n++
	}
	return n
}

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
	return t.Adapter.Save(ctx, t.folderPrefix()+id, data)
}

func (s *Saver) CreateFolder(ctx context.Context, target, parent, name string) (Folder, error) {
	if !ValidName(name) {
		return Folder{}, ErrName
	}
	return s.createFolder(ctx, target, docfile.NewID(), parent, name)
}

func (s *Saver) createFolder(ctx context.Context, target, id, parent, name string) (Folder, error) {
	return s.createFolderLevel(ctx, target, id, parent, name, false)
}

// the real name is never written in the clear
func (s *Saver) createFolderLevel(ctx context.Context, target, id, parent, name string, e2ee bool) (Folder, error) {
	if !ValidName(name) {
		return Folder{}, ErrName
	}
	// checked before the Rekey hook, which acts on the document's keys
	if err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		if _, ok := cat.Docs[id]; ok {
			return ErrFolderID
		}
		return nil
	}); err != nil {
		return Folder{}, err
	}
	if e2ee {
		if s.ConfidentialCheck != nil {
			if err := s.ConfidentialCheck(ctx, target, id); err != nil {
				return Folder{}, fmt.Errorf("saver: folder not made confidential: %w", err)
			}
		}
		if err := s.rekey(ctx, target, id, ProtectionE2EE); err != nil {
			return Folder{}, err
		}
	}
	var out Folder
	err := s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		if _, ok := cat.Docs[id]; ok {
			return ErrFolderID
		}
		if _, ok := cat.Folders[parent]; parent != "" && !ok {
			return ErrFolder
		}
		if cat.depth(parent) >= MaxFolderDepth {
			return fmt.Errorf("saver: folders nest at most %d deep", MaxFolderDepth)
		}
		f := folderEntry{Name: name, Parent: parent}
		if e2ee {
			if err := s.writeFolderSecret(ctx, t, cat, id, name); err != nil {
				return err
			}
			f.Name, f.Protection = ConfidentialFolderName, ProtectionE2EE
		}
		if err := s.writeFolder(ctx, t, id, f); err != nil {
			return err
		}
		cat.Folders[id] = f
		out = Folder{ID: id, Name: f.Name, Parent: parent, Protection: level(f.Protection)}
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
		name := f.Name
		if f.Protection == ProtectionE2EE {
			name = ConfidentialFolderName
		}
		out = append(out, Folder{ID: id, Name: name, Parent: f.Parent, Protection: level(f.Protection)})
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
	rekeyed := false
	for attempt := 0; ; attempt++ {
		f, err := s.folder(ctx, target, id)
		if err != nil {
			return err
		}
		if f.Protection == ProtectionE2EE {
			if err := s.rekey(ctx, target, id, ProtectionE2EE); err != nil {
				return err
			}
			rekeyed = true
		}
		err = s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
			if _, ok := cat.Docs[id]; ok {
				return ErrFolderID
			}
			f, ok := cat.Folders[id]
			if !ok {
				return ErrFolder
			}
			if f.Protection == ProtectionE2EE {
				if !rekeyed {
					return errNeedRekey // it became confidential after it was read
				}
				if err := s.writeFolderSecret(ctx, t, nil, id, name); err != nil {
					return err
				}
				return nil // the plain record and the catalog keep the placeholder
			}
			f.Name = name
			if err := s.writeFolder(ctx, t, id, f); err != nil {
				return err
			}
			cat.Folders[id] = f
			return nil
		})
		if errors.Is(err, errNeedRekey) && attempt < 2 {
			continue
		}
		return err
	}
}

func (s *Saver) MoveFolder(ctx context.Context, target, id, parent string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		if _, ok := cat.Docs[id]; ok {
			return ErrFolderID
		}
		f, ok := cat.Folders[id]
		if !ok {
			return ErrFolder
		}
		if _, ok := cat.Folders[parent]; parent != "" && !ok || cat.inside(parent, id) {
			return ErrFolder
		}
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

func (s *Saver) DeleteFolder(ctx context.Context, target, id string) error {
	_, err := s.DeleteFolderReport(ctx, target, id)
	return err
}

func (s *Saver) DeleteFolderReport(ctx context.Context, target, id string) (purged []string, err error) {
	err = s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		purged = nil // update may run this again
		if _, ok := cat.Docs[id]; ok {
			return ErrFolderID
		}
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
		var writes []func() error
		for _, did := range gone {
			e := cat.Docs[did]
			w, err := s.prepareTouch(ctx, t, cat, did, &e, later, func(d *docfile.Document) { d.SetFolder("") })
			if errors.Is(err, ErrUnreadable) && !errors.Is(err, docstore.ErrNotFound) {
				e.Folder = ""
				cat.Docs[did] = e
				continue
			}
			if errors.Is(err, ErrUnreadable) {
				purged = append(purged, e.Name)
				delete(cat.Docs, did)
				key, prevKey := e.Key, e.Prev
				*later = append(*later, func() {
					_ = t.Adapter.Delete(ctx, key)
					if prevKey != "" {
						_ = t.Adapter.Delete(ctx, prevKey)
					}
					s.dropVersions(ctx, t, did)
				})
				continue
			}
			if err != nil {
				return err
			}
			writes = append(writes, w)
			e.Folder = ""
			cat.Docs[did] = e
		}
		for _, w := range writes {
			if err := w(); err != nil {
				return err
			}
		}
		secret := cat.Folders[id].Protection == ProtectionE2EE
		delete(cat.Folders, id)
		*later = append(*later, func() {
			_ = t.Adapter.Delete(ctx, t.folderPrefix()+id)
			if secret {
				_ = t.Adapter.Delete(ctx, folderSecretKey(id))
			}
		})
		return nil
	})
	if err != nil {
		purged = nil
	}
	return purged, err
}

func (s *Saver) Move(ctx context.Context, target, folder string, ids ...string) error {
	return s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		if _, ok := cat.Folders[folder]; folder != "" && !ok {
			return ErrFolder
		}
		for _, id := range ids {
			if _, ok := cat.Docs[id]; !ok {
				return fmt.Errorf("%w: %s", docstore.ErrNotFound, id)
			}
		}
		var writes []func() error
		for _, id := range ids {
			e := cat.Docs[id]
			if e.Folder == folder {
				continue
			}
			w, err := s.prepareTouch(ctx, t, cat, id, &e, later, func(d *docfile.Document) { d.SetFolder(folder) })
			if err != nil {
				return err
			}
			writes = append(writes, w)
			e.Folder = folder
			cat.Docs[id] = e
		}
		for _, w := range writes {
			if err := w(); err != nil {
				return err
			}
		}
		return nil
	})
}

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
