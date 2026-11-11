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

type PackReason string

const PackNeedsKey PackReason = "needs-key"

type PackIssue struct {
	ID, Name string
	Reason   PackReason
	Err      error
}

// PackOptions are the options of ExportPack.
type PackOptions struct {
	Name    string
	History bool
	// Key signs the pack (Ed25519) when it is not nil.
	Key         ed25519.PrivateKey
	Seal        func(ctx context.Context, docID string, plain []byte) ([]byte, error)
	Blur        bool
	Attachments int
	Warn        func(PackIssue)
}

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
	meta := pack.Meta{ID: docfile.NewID(), Name: name, Created: time.Now().UnixNano(), Producer: "gojiberry"}
	var pw *pack.Writer
	var ordinaryIDs []string
	for _, it := range items {
		if !it.e.confidential() {
			ordinaryIDs = append(ordinaryIDs, it.id)
		}
	}
	if o.Attachments > 0 {
		large, lerr := s.anyLargeImage(ctx, target, ordinaryIDs, o)
		if lerr != nil {
			return 0, lerr
		}
		if large {
			if pw, err = pack.NewWriterV2(w, meta); err != nil {
				return 0, err
			}
		}
	}
	if pw == nil {
		pw, err = pack.NewWriter(w, meta)
	}
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
			if err := packSealed(ctx, pw, o, it.id, it.e, doc); err != nil {
				return n, err
			}
		} else {
			if o.Attachments > 0 && pw.Version2() {
				if doc, err = apartImages(pw, doc, o.Attachments); err != nil {
					return n, fmt.Errorf("saver: %q: %w", it.name, err)
				}
			}
			if err := pw.Add(doc); err != nil {
				return n, fmt.Errorf("saver: %q: %w", it.name, err)
			}
		}
		n++
	}
	t, err := s.Target(target)
	if err != nil {
		return n, err
	}
	for _, f := range folders {
		if err := pw.AddFolder(pack.Folder{ID: f.ID, Name: f.Name, Parent: f.Parent}); err != nil {
			return n, err
		}
		if f.Protection != ProtectionE2EE {
			continue
		}
		if o.Seal == nil {
			warn(PackIssue{ID: f.ID, Name: f.Name, Reason: PackNeedsKey})
			continue
		}
		if err := packSealedFolderName(ctx, pw, t, o, f, warn); err != nil {
			return n, err
		}
	}
	return n, pw.Close(o.Key)
}

func packSealed(ctx context.Context, pw *pack.Writer, o PackOptions, id string, e catalogEntry, doc docfile.Document) error {
	seal := o.Seal
	doc.SetID(id)
	e.withCatalog(&doc)
	plain, err := docfile.Open.Marshal(doc)
	if err != nil {
		return err
	}
	updated := doc.Updated
	if o.Blur {
		plain, updated = blurPlain(plain), blurTime(updated)
	}
	env, err := seal(ctx, id, plain)
	if err != nil {
		return fmt.Errorf("saver: sealing %s: %w", id, err)
	}
	if len(env) == 0 {
		return fmt.Errorf("saver: sealing %s gave nothing", id)
	}
	return pw.AddEncrypted(pack.Entry{ID: id, Name: ConfidentialName, Folder: e.Folder, Updated: updated, Trashed: e.Trashed != 0}, env)
}

func packSealedFolderName(ctx context.Context, pw *pack.Writer, t Target, o PackOptions, f Folder, warn func(PackIssue)) error {
	name, err := readFolderSecret(ctx, t, f.ID)
	if errors.Is(err, docstore.ErrLocked) || errors.Is(err, docstore.ErrNotFound) {
		warn(PackIssue{ID: f.ID, Name: f.Name, Reason: PackNeedsKey, Err: err})
		return nil
	}
	if err != nil {
		return fmt.Errorf("saver: folder %s: %w", f.ID, err)
	}
	var doc docfile.Document
	doc.SetID(f.ID)
	doc.SetName(name)
	doc.SetProtection(ProtectionE2EE)
	plain, err := docfile.Open.Marshal(doc)
	if err != nil {
		return err
	}
	if o.Blur {
		plain = blurPlain(plain)
	}
	env, err := o.Seal(ctx, f.ID, plain)
	if err != nil {
		return fmt.Errorf("saver: sealing folder %s: %w", f.ID, err)
	}
	if len(env) == 0 {
		return fmt.Errorf("saver: sealing folder %s gave nothing", f.ID)
	}
	return pw.AddEncrypted(pack.Entry{ID: f.ID, Name: ConfidentialFolderName}, env)
}

func isFolderName(e pack.Entry, folders map[string]pack.Folder) bool {
	_, ok := folders[e.ID]
	return ok && e.Encrypted && e.Name == ConfidentialFolderName
}

type ConflictPolicy int

const (
	ConflictSkip ConflictPolicy = iota
	ConflictReplace
	ConflictCopy
)

// ImportOptions are the options of ImportPack.
type ImportOptions struct {
	Into string
	// Format is how the documents are stored; nil is JSON.
	Format   docfile.Codec
	Conflict ConflictPolicy
	Open     func(ctx context.Context, docID string, env []byte) ([]byte, error)
}

// ImportReport says what ImportPack did.
type ImportReport struct {
	Documents, Folders  int
	Skipped             []string
	Failed              []string
	Warnings            []string
	NeedsKey            []PackIssue
	Signed, SignatureOK bool
	Signer              []byte
}

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
	byID := map[string]pack.Folder{}
	for _, f := range idx.Folders {
		byID[f.ID] = f
	}
	// sealed folder names open first: one that fails is made under the placeholder
	realName := map[string]string{}
	sealed := map[string]bool{} // folders whose name was sealed but could not be read
	for _, e := range idx.Entries {
		if !isFolderName(e, byID) {
			continue
		}
		var openErr error
		var open func([]byte) ([]byte, error)
		if o.Open != nil {
			open = func(env []byte) ([]byte, error) {
				plain, err := o.Open(ctx, e.ID, env)
				openErr = err
				return plain, err
			}
		}
		doc, err := rd.ReadWith(e.ID, open)
		if err != nil {
			if openErr != nil && ctx.Err() != nil {
				return rep, ctx.Err()
			}
			rep.NeedsKey = append(rep.NeedsKey, PackIssue{ID: e.ID, Name: e.Name, Reason: PackNeedsKey, Err: err})
			rep.Warnings = append(rep.Warnings, e.Name+": a key is needed to read the folder's name; the folder is made confidential under the placeholder name (rename it to set one)")
			sealed[e.ID] = true
			continue
		}
		if ValidName(doc.Name()) {
			realName[e.ID] = doc.Name()
		} else {
			sealed[e.ID] = true
			rep.Warnings = append(rep.Warnings, e.Name+": the folder's name is not valid; the folder is made confidential under the placeholder name")
		}
	}
	// a folder and a document must never share an ID
	docIDs := map[string]bool{}
	err = s.view(ctx, target, func(_ Target, cat *catalog) error {
		for id := range cat.Docs {
			docIDs[id] = true
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	packDocs := map[string]bool{}
	for _, e := range idx.Entries {
		if !isFolderName(e, byID) {
			packDocs[e.ID] = true
		}
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
		newID := f.ID
		if docIDs[f.ID] || packDocs[f.ID] {
			newID = docfile.NewID()
			rep.Warnings = append(rep.Warnings, f.Name+": the folder's ID is a document's; it was given a new one")
		} else if have[f.ID] {
			mapped[f.ID] = f.ID
			continue
		}
		var made Folder
		var err error
		if name, ok := realName[f.ID]; ok {
			made, err = s.createFolderLevel(ctx, target, newID, parent, name, true)
		} else if sealed[f.ID] || f.Name == ConfidentialFolderName {
			// hidden name: never create a plain folder of that name
			made, err = s.createFolderLevel(ctx, target, newID, parent, ConfidentialFolderName, true)
		} else {
			made, err = s.createFolder(ctx, target, newID, parent, f.Name)
		}
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
		if isFolderName(e, byID) {
			continue // a folder's sealed name, handled with the folders
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
		if rd.Version() >= 2 {
			var missing int
			if doc, missing = resolveImages(rd, doc); missing > 0 {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: %d pictures the pack should hold are missing", e.Name, missing))
			}
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
		doc.SetTrashed(0)
		// catalog and file agree: Trash below sets it in both
		if have[doc.ID()] {
			doc.SetID(docfile.NewID()) // a folder's ID: never share it (see above)
		}
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
		// end to end in one save, never ordinary first
		saved, err := s.save(ctx, target, c, doc, secret)
		if err != nil {
			rep.Failed = append(rep.Failed, e.Name+": "+err.Error())
			continue
		}
		rep.Documents++
		warn := func(what string, err error) {
			rep.Warnings = append(rep.Warnings, e.Name+": "+what+": "+err.Error())
		}
		if replacing {
			if !secret {
				if err := s.SetTags(ctx, target, saved.ID, tags); err != nil {
					warn("tags", err)
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
