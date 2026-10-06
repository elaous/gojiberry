package saver

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
)

// PackOptions are the options of ExportPack.
type PackOptions struct {
	// Name is the pack's name; "" uses the folder's name, or "documents" for the whole store.
	Name string
	// History puts each document's versions in the pack (each record is a .open message,
	// which can carry them).
	History bool
	// Key signs the pack (Ed25519) when it is not nil.
	Key ed25519.PrivateKey
}

// ExportPack writes the documents of a folder (and the folders under it), or of the whole
// target when folder is "", as one .opendoc pack (docs/opendoc-format.md) to w: folders by
// ID, with tags and the trash flag, and returns how many documents it wrote. The documents
// are read outside the saver's lock, one by one; a document that cannot be read (damaged,
// with no good copy) stops the export with an error naming it, since a backup that quietly
// lacks a document is worse than none.
func (s *Saver) ExportPack(ctx context.Context, target, folder string, w io.Writer, o PackOptions) (int, error) {
	type item struct {
		id   string
		name string
		e    catalogEntry
	}
	var items []item
	var folders []Folder
	name := o.Name
	err := s.view(ctx, target, func(_ Target, cat *catalog) error {
		if folder != "" {
			if _, ok := cat.Folders[folder]; !ok {
				return ErrFolder
			}
			if name == "" {
				name = cat.Folders[folder].Name
			}
		}
		for id, e := range cat.Docs {
			if folder == "" || cat.inside(e.Folder, folder) {
				items = append(items, item{id, e.Name, e})
			}
		}
		for _, f := range cat.folderList() {
			if folder == "" || cat.inside(f.ID, folder) {
				if f.ID == folder {
					f.Parent = "" // the exported folder is the top of the pack
				}
				folders = append(folders, f)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if name == "" {
		name = "documents"
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	pw, err := pack.NewWriter(w, pack.Meta{ID: docfile.NewID(), Name: name, Created: time.Now().UnixNano(), Producer: "gojiberry"})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		doc, _, err := s.loadWithHistory(ctx, target, it.id, o.History)
		if err != nil {
			return n, fmt.Errorf("saver: %q: %w", it.name, err)
		}
		doc.SetTrashed(it.e.Trashed)
		if err := pw.Add(doc); err != nil {
			return n, fmt.Errorf("saver: %q: %w", it.name, err)
		}
		n++
	}
	for _, f := range folders {
		if err := pw.AddFolder(pack.Folder{ID: f.ID, Name: f.Name, Parent: f.Parent}); err != nil {
			return n, err
		}
	}
	return n, pw.Close(o.Key)
}

// ConflictPolicy says what ImportPack does with a document whose ID is already in the target.
type ConflictPolicy int

const (
	// ConflictSkip leaves the document in the target as it is, and reports it skipped.
	ConflictSkip ConflictPolicy = iota
	// ConflictReplace saves the pack's document over the one in the target.
	ConflictReplace
	// ConflictCopy imports it as a new document, under a new ID.
	ConflictCopy
)

// ImportOptions are the options of ImportPack.
type ImportOptions struct {
	// Into is the folder the pack's top level goes into ("" for the top).
	Into string
	// Format is how the documents are stored; nil is JSON.
	Format docfile.Codec
	// Conflict is what to do with a document that is already there.
	Conflict ConflictPolicy
}

// ImportReport says what ImportPack did.
type ImportReport struct {
	// Documents and Folders are how many were brought in (a folder already there with the
	// same ID is reused, not counted).
	Documents, Folders int
	// Skipped names the documents left alone because they were already there.
	Skipped []string
	// Failed names the documents that could not be read or saved, with why.
	Failed []string
	// Signed and SignatureOK say whether the pack carried a signature and it checked out;
	// Signer is the key. Whether to trust it is for the caller.
	Signed, SignatureOK bool
	Signer              []byte
}

// ImportPack reads a .opendoc pack and saves its documents into target, recreating its
// folders (by ID, under Into), with tags and the trash flag, and each document's history if
// the pack holds it. A document that fails is reported and the others still come in.
func (s *Saver) ImportPack(ctx context.Context, target string, r io.ReaderAt, size int64, o ImportOptions) (ImportReport, error) {
	var rep ImportReport
	rd, err := pack.Open(r, size)
	if err != nil {
		return rep, err
	}
	if sig, ok := rd.Signature(); sig != nil {
		rep.Signed, rep.SignatureOK, rep.Signer = true, ok, sig.PublicKey
	}
	c := o.Format
	if c == nil {
		c = docfile.JSON
	}
	if o.Into != "" {
		if _, err := s.Folders(ctx, target); err != nil {
			return rep, err
		}
	}
	idx := rd.Index()
	existing, err := s.Folders(ctx, target)
	if err != nil {
		return rep, err
	}
	have := map[string]bool{}
	for _, f := range existing {
		have[f.ID] = true
	}
	if o.Into != "" && !have[o.Into] {
		return rep, ErrFolder
	}
	// Parents before children.
	byID := map[string]pack.Folder{}
	for _, f := range idx.Folders {
		byID[f.ID] = f
	}
	var order []pack.Folder
	done := map[string]bool{}
	var visit func(f pack.Folder, depth int)
	visit = func(f pack.Folder, depth int) {
		if done[f.ID] || depth > pack.MaxDepth {
			return
		}
		if p, ok := byID[f.Parent]; ok {
			visit(p, depth+1)
		}
		done[f.ID] = true
		order = append(order, f)
	}
	for _, f := range idx.Folders {
		visit(f, 0)
	}
	mapped := map[string]string{} // pack folder ID -> folder ID in the target
	for _, f := range order {
		parent := o.Into
		if f.Parent != "" {
			if m, ok := mapped[f.Parent]; ok {
				parent = m
			}
		}
		if have[f.ID] {
			mapped[f.ID] = f.ID
			continue
		}
		made, err := s.createFolder(ctx, target, f.ID, parent, f.Name)
		if err != nil {
			rep.Failed = append(rep.Failed, f.Name+": "+err.Error())
			mapped[f.ID] = parent // its documents go to the parent
			continue
		}
		have[made.ID] = true
		mapped[f.ID] = made.ID
		rep.Folders++
	}
	for _, e := range idx.Entries {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		doc, err := rd.Read(e.ID)
		if err != nil {
			rep.Failed = append(rep.Failed, e.Name+": "+err.Error())
			continue
		}
		folder := o.Into
		if e.Folder != "" {
			if m, ok := mapped[e.Folder]; ok {
				folder = m
			}
		}
		doc.SetFolder(folder)
		id := doc.ID()
		if _, err := s.Get(ctx, target, id); err == nil {
			switch o.Conflict {
			case ConflictSkip:
				rep.Skipped = append(rep.Skipped, e.Name)
				continue
			case ConflictCopy:
				doc.SetID(docfile.NewID())
				doc.SetName(e.Name + " (imported)")
			}
		}
		saved, err := s.Save(ctx, target, c, doc)
		if err != nil {
			rep.Failed = append(rep.Failed, e.Name+": "+err.Error())
			continue
		}
		if e.Trashed {
			if err := s.Trash(ctx, target, saved.ID); err != nil {
				rep.Failed = append(rep.Failed, e.Name+": "+err.Error())
				continue
			}
		}
		rep.Documents++
	}
	return rep, nil
}
