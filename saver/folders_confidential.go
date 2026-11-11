package saver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/elaous/gojiberry/docstore"
)

const ConfidentialFolderName = "Confidential folder"

func folderSecretKey(id string) string { return docstore.FolderSecretPrefix + id }

func FolderSecretKey(id string) string { return folderSecretKey(id) }

type folderSecret struct {
	Name string `json:"name"`
}

func (s *Saver) writeFolderSecret(ctx context.Context, t Target, cat *catalog, id, name string) error {
	data, err := json.Marshal(folderSecret{Name: name})
	if err != nil {
		return err
	}
	if err := t.Adapter.Save(ctx, folderSecretKey(id), data); err != nil {
		return err
	}
	if cat != nil {
		cat.fresh = append(cat.fresh, folderSecretKey(id))
	}
	return nil
}

func readFolderSecret(ctx context.Context, t Target, id string) (string, error) {
	stored, err := t.Adapter.Load(ctx, folderSecretKey(id))
	if err != nil {
		return "", err
	}
	var sec folderSecret
	if err := json.Unmarshal(stored.Data, &sec); err != nil {
		return "", fmt.Errorf("saver: folder %s: %w", id, ErrUnreadable)
	}
	return sec.Name, nil
}

func (s *Saver) folder(ctx context.Context, target, id string) (folderEntry, error) {
	var out folderEntry
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		if _, ok := cat.Docs[id]; ok {
			return ErrFolderID // never act on a document's key record as a folder's
		}
		f, ok := cat.Folders[id]
		if !ok {
			return ErrFolder
		}
		out = f
		return nil
	})
	return out, err
}

// RevealFolder returns the real name of a folder.
func (s *Saver) RevealFolder(ctx context.Context, target, id string) (string, error) {
	f, err := s.folder(ctx, target, id)
	if err != nil {
		return "", err
	}
	if f.Protection != ProtectionE2EE {
		return f.Name, nil
	}
	t, err := s.Target(target)
	if err != nil {
		return "", err
	}
	return readFolderSecret(ctx, t, id)
}

// SetFolderProtection switches a folder to a protection level.
func (s *Saver) SetFolderProtection(ctx context.Context, target, id, to string, recursive bool) error {
	if to != ProtectionServer && to != ProtectionE2EE {
		return ErrProtection
	}
	to = level(to)
	if err := s.setFolderLevel(ctx, target, id, to); err != nil {
		return err
	}
	if !recursive {
		return nil
	}
	var folders, docs []string
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		for fid := range cat.Folders {
			if fid != id && cat.inside(fid, id) {
				folders = append(folders, fid)
			}
		}
		for did, e := range cat.Docs {
			if cat.inside(e.Folder, id) {
				docs = append(docs, did)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(folders)
	sort.Strings(docs)
	var errs []error
	for _, fid := range folders {
		if err := s.setFolderLevel(ctx, target, fid, to); err != nil {
			errs = append(errs, fmt.Errorf("folder %s: %w", fid, err))
		}
	}
	for _, did := range docs {
		if err := s.SetProtection(ctx, target, did, to); err != nil {
			errs = append(errs, fmt.Errorf("document %s: %w", did, err))
		}
	}
	return errors.Join(errs...)
}

func (s *Saver) setFolderLevel(ctx context.Context, target, id, to string) error {
	cur, err := s.folder(ctx, target, id)
	if err != nil {
		return err
	}
	if level(cur.Protection) == to {
		return s.rekey(ctx, target, id, to) // settled: finishing is the hook's idempotent job
	}
	if to == ProtectionE2EE {
		if s.ConfidentialCheck != nil {
			if err := s.ConfidentialCheck(ctx, target, id); err != nil {
				return fmt.Errorf("saver: folder not made confidential: %w", err)
			}
		}
		if err := s.rekey(ctx, target, id, to); err != nil {
			return err
		}
	}
	err = s.update(ctx, target, func(t Target, cat *catalog, later *[]func()) error {
		f, ok := cat.Folders[id]
		if !ok {
			return ErrFolder
		}
		if level(f.Protection) == to {
			return nil
		}
		if to == ProtectionE2EE {
			if err := s.writeFolderSecret(ctx, t, cat, id, f.Name); err != nil {
				return err
			}
			f.Name, f.Protection = ConfidentialFolderName, ProtectionE2EE
		} else {
			name, err := readFolderSecret(ctx, t, id)
			if err != nil {
				return err
			}
			f.Name, f.Protection = name, ""
			*later = append(*later, func() { _ = t.Adapter.Delete(ctx, folderSecretKey(id)) })
		}
		if err := s.writeFolder(ctx, t, id, f); err != nil {
			return err
		}
		cat.Folders[id] = f
		return nil
	})
	if err != nil {
		return err
	}
	if to == ProtectionServer {
		return s.rekey(ctx, target, id, to)
	}
	return nil
}
