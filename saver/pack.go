package saver

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
	"github.com/elaous/gojiberry/docstore"
)

// PackReason says why a document was left out of an export or an import with a warning.
type PackReason string

// PackNeedsKey: an end-to-end (confidential) document and no function to seal (export) or
// open (import) it, or the one given could not. The document is not lost: give the key and
// run it again.
const PackNeedsKey PackReason = "needs-key"

// PackIssue is a document an export or import left out with a warning; these never stop the
// operation.
type PackIssue struct {
	ID, Name string
	Reason   PackReason
	Err      error // why, when there is more to say (the error of a Seal or Open)
}

// PackOptions are the options of ExportPack.
type PackOptions struct {
	// Name is the pack's name; "" uses the folder's name, or "documents" for the whole store.
	Name string
	// History puts each document's versions in the pack (each record is a .open message,
	// which can carry them).
	History bool
	// Key signs the pack (Ed25519) when it is not nil.
	Key ed25519.PrivateKey
	// Seal encrypts an end-to-end (confidential) document for the pack: plain is its .open
	// message (with its history, if History is set), the result an envelope the pack stores
	// as it is, as an encrypted record. The pack's index lists ConfidentialName and no tags
	// for it; the real name and tags travel inside the envelope. With no Seal such a
	// document is left out and reported to Warn as PackNeedsKey, never written in the
	// clear. A Seal that fails stops the export: a pack that quietly lacks the document
	// would pass for a backup.
	Seal func(ctx context.Context, docID string, plain []byte) ([]byte, error)
	// Warn is told of each document left out (PackNeedsKey) when not nil. They are not
	// counted in the number ExportPack returns.
	Warn func(PackIssue)
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
	warn := func(i PackIssue) {
		if o.Warn != nil {
			o.Warn(i)
		}
	}
	pw, err := pack.NewWriter(w, pack.Meta{ID: docfile.NewID(), Name: name, Created: time.Now().UnixNano(), Producer: "gojiberry"})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		secret := it.e.confidential()
		if secret && o.Seal == nil {
			warn(PackIssue{ID: it.id, Name: it.name, Reason: PackNeedsKey})
			continue
		}
		doc, _, err := s.loadWithHistory(ctx, target, it.id, o.History)
		if err != nil {
			if secret && errors.Is(err, docstore.ErrLocked) {
				warn(PackIssue{ID: it.id, Name: it.name, Reason: PackNeedsKey, Err: err}) // no key opens it here
				continue
			}
			return n, fmt.Errorf("saver: %q: %w", it.name, err)
		}
		doc.SetTrashed(it.e.Trashed)
		if secret {
			if err := packSealed(ctx, pw, o.Seal, it.id, it.e, doc); err != nil {
				return n, err
			}
		} else if err := pw.Add(doc); err != nil {
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

// packSealed writes an end-to-end document as an encrypted record. The index shows the
// placeholder name and no tags (the catalog holds none for it either); the folder, the times
// and the trash flag are not secret.
func packSealed(ctx context.Context, pw *pack.Writer, seal func(context.Context, string, []byte) ([]byte, error), id string, e catalogEntry, doc docfile.Document) error {
	doc.SetID(id)
	e.withCatalog(&doc) // marks it end to end, and the real name and tags are the file's
	plain, err := docfile.Open.Marshal(doc)
	if err != nil {
		return err
	}
	env, err := seal(ctx, id, plain)
	if err != nil {
		return fmt.Errorf("saver: sealing %s: %w", id, err)
	}
	if len(env) == 0 {
		return fmt.Errorf("saver: sealing %s gave nothing", id)
	}
	return pw.AddEncrypted(pack.Entry{ID: id, Name: ConfidentialName, Folder: e.Folder, Updated: doc.Updated, Trashed: e.Trashed != 0}, env)
}

// ConflictPolicy says what ImportPack does with a document whose ID is already in the target.
type ConflictPolicy int

const (
	// ConflictSkip leaves the document in the target as it is, and reports it skipped.
	ConflictSkip ConflictPolicy = iota
	// ConflictReplace saves the pack's document over the one in the target, and takes the
	// pack's folder, tags and trash flag too (a trashed document replaced by a live one is
	// restored).
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
	// Open decrypts the envelope of an encrypted record (docID is the record's ID) and
	// returns the .open message inside. With no Open, or when it fails, the record is left
	// out and reported in ImportReport.NeedsKey: a warning, never an error that stops the
	// import. A document that comes in this way is saved end to end again (the saver's
	// ConfidentialCheck, if there is one, must allow it, else it is reported as Failed).
	Open func(ctx context.Context, docID string, env []byte) ([]byte, error)
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
	// Warnings are things that went wrong after a document was saved (it is counted in
	// Documents): its trash flag, its folder or its tags could not be applied.
	Warnings []string
	// NeedsKey are the encrypted records left out for want of a working Open (also in
	// Warnings, in words). The pack is unchanged: import it again with the key.
	NeedsKey []PackIssue
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
	idx := rd.Index()
	existing, err := s.Folders(ctx, target) // read once: the folders of the target before the import
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
		var openErr error
		var open func([]byte) ([]byte, error)
		if e.Encrypted && o.Open != nil {
			open = func(env []byte) ([]byte, error) {
				plain, err := o.Open(ctx, e.ID, env)
				openErr = err
				return plain, err
			}
		}
		doc, err := rd.ReadWith(e.ID, open)
		switch {
		case errors.Is(err, pack.ErrEncrypted), openErr != nil:
			if openErr != nil && ctx.Err() != nil {
				return rep, ctx.Err()
			}
			rep.NeedsKey = append(rep.NeedsKey, PackIssue{ID: e.ID, Name: e.Name, Reason: PackNeedsKey, Err: openErr})
			why := "a key is needed to open it"
			if openErr != nil {
				why += ": " + openErr.Error()
			}
			rep.Warnings = append(rep.Warnings, e.Name+": "+why)
			continue
		case err != nil:
			rep.Failed = append(rep.Failed, e.Name+": "+err.Error())
			continue
		}
		secret := e.Encrypted
		if secret {
			doc.SetProtection(ProtectionE2EE)
		}
		folder := o.Into
		if e.Folder != "" {
			if m, ok := mapped[e.Folder]; ok {
				folder = m
			}
		}
		doc.SetFolder(folder)
		tags := doc.Tags()
		doc.SetTrashed(0) // the catalog and the file agree: Trash below sets it in both
		id := doc.ID()
		replacing := false
		var prev Entry
		if p, err := s.Get(ctx, target, id); err == nil {
			switch o.Conflict {
			case ConflictSkip:
				rep.Skipped = append(rep.Skipped, e.Name)
				continue
			case ConflictCopy:
				doc.SetID(docfile.NewID())
				name := e.Name
				if secret {
					name = doc.Name() // the index shows a placeholder; the real name is inside
				}
				doc.SetName(name + " (imported)")
			case ConflictReplace:
				replacing, prev = true, p
			}
		}
		saved, err := s.Save(ctx, target, c, doc)
		if err != nil {
			rep.Failed = append(rep.Failed, e.Name+": "+err.Error())
			continue
		}
		rep.Documents++
		warn := func(what string, err error) {
			rep.Warnings = append(rep.Warnings, e.Name+": "+what+": "+err.Error())
		}
		if replacing {
			// Save keeps the tags, folder and trash flag of the document it replaces;
			// the pack's are what was asked for.
			// An end-to-end document keeps its tags inside it (Save keeps them); the catalog
			// has none. Over an ordinary document the tags go in the catalog first, and
			// SetProtection then moves them inside with the name.
			if !secret || prev.Protection != ProtectionE2EE {
				if err := s.SetTags(ctx, target, saved.ID, tags); err != nil {
					warn("tags", err)
				}
			}
			if secret && prev.Protection != ProtectionE2EE {
				if err := s.SetProtection(ctx, target, saved.ID, ProtectionE2EE); err != nil {
					warn("protection", err)
				}
			}
			if prev.Folder != folder {
				if err := s.Move(ctx, target, folder, saved.ID); err != nil {
					warn("folder", err)
				}
			}
			if !e.Trashed && !prev.Trashed.IsZero() {
				if err := s.Restore(ctx, target, saved.ID); err != nil {
					warn("restore", err)
				}
			}
		}
		if e.Trashed && (!replacing || prev.Trashed.IsZero()) {
			if err := s.Trash(ctx, target, saved.ID); err != nil {
				warn("trash", err)
			}
		}
	}
	return rep, nil
}
