package saver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/elaous/gojiberry/docstore"
)

// Confidential folders (docs/encryption-and-passkeys.md, C.2). A folder is server level (the
// default: the catalog and its record list its real name) or confidential (ProtectionE2EE:
// they list ConfidentialFolderName, and the real name is kept in an encrypted record).
//
// What is hidden and what is not. Only the NAME. Where the folder sits in the tree (its
// parent), which documents are in it, and their sizes and times stay visible, as C.2 says
// ("the folder tree's shape stays visible").
//
// Where the name lives. In the record folderSecretKey(id) ("folder-secret/<ID>", JSON
// {"name": ...}), a stored item that is NOT under "catalog:", so an encrypting adapter does
// not skip it (docstore/encrypt.DefaultSkip) and wraps it like a document file. A folder's ID
// is a UUID like a document's, so encrypt.DocID maps the record to the folder, and the same
// signed keys:<ID> record decides who it is encrypted to (ConfidentialKeyring: people's keys
// only at the e2ee level). The folder's plain record ("catalog:folder/<ID>") stays readable
// and holds the placeholder, the parent and the level, which is what lets a rebuild with no
// key list the folder under its place in the tree.
//
// Documents in a confidential folder are NOT made confidential by it: a document's level is
// its own (SetProtection). SetFolderProtection with recursive set applies the level to the
// documents and folders inside as well.

// ConfidentialFolderName is what the catalog lists as the name of a confidential folder.
const ConfidentialFolderName = "Confidential folder"

// folderSecretKey is where a confidential folder keeps its real name.
func folderSecretKey(id string) string { return docstore.FolderSecretPrefix + id }

// FolderSecretKey is the stored key of a confidential folder's real name (for hosts that
// configure which items an encrypting adapter wraps, and for tests).
func FolderSecretKey(id string) string { return folderSecretKey(id) }

type folderSecret struct {
	Name string `json:"name"`
}

// writeFolderSecret stores the real name. With a catalog it is a file this attempt of an
// update wrote under a new key: if the update fails it is removed (cat.fresh); with nil it
// replaces the record of a folder that is already confidential and is kept either way.
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

// readFolderSecret returns the real name; an error wrapping docstore.ErrLocked when no key
// here opens the record.
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

// folder returns a folder's entry as stored (the placeholder name for a confidential one).
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

// RevealFolder returns the real name of a folder. For a server folder it is the catalog's;
// for a confidential one it is read out of its encrypted record, which therefore has to be
// openable here (an error wrapping docstore.ErrLocked when no key opens it): until then a
// client shows ConfidentialFolderName.
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

// SetFolderProtection switches a folder to a protection level. To ProtectionE2EE its real
// name moves into the encrypted record and the catalog (and the folder's plain record) list
// ConfidentialFolderName; back to ProtectionServer it moves out again. The record has to be
// openable here when going back. Before enabling, ConfidentialCheck, if set, must agree
// (nothing changes on a refusal).
//
// The order is SetProtection's: to e2ee the Rekey hook runs first (so the name is never in
// a record wrapped for the server key) and only then does the name move; to server the name
// moves first and the hook runs last. Calling it again with the level the folder already
// has runs the hook again, which finishes a re-key that was cut short.
//
// A folder's record is protected like a document's: docstore.ErrDowngrade from the adapter
// means the folder's signed keys record was deleted or replaced by an older one, and the folder
// is neither switched, renamed nor revealed until the owner recovers it
// (encrypt.Sharing.Recover).
//
// recursive applies the level also to every folder below it and to every document in the
// folder or below it (the trash included), each through SetFolderProtection or SetProtection.
// Without it the documents keep the level they had: a confidential folder does not make its
// documents confidential. A document or folder that cannot be switched (no key opens it) does
// not stop the others: the errors are returned together, run it again to finish.
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
