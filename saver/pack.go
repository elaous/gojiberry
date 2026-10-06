package saver

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
	"github.com/elaous/gojiberry/docstore"
)

// ExportPack and ImportPack move documents and folders in and out of a .opendoc pack
// (docs/opendoc-format.md) by ID: folders, tags, the trash flag and the documents'
// contents are kept. Neither knows about keys. A confidential (end-to-end) document is
// written as an encrypted record, sealed by a function the caller injects, and read back
// through another: the saver and the pack only carry the envelope.

// PackReason says why a document was left out of an export or import.
type PackReason string

const (
	// PackNeedsKey: a confidential document and no function to seal (export) or open
	// (import) it, or the one given could not. The document is not lost: give the key and run
	// it again.
	PackNeedsKey PackReason = "needs-key"
	// PackExists: the target already has a document with this ID, which is left as it is.
	PackExists PackReason = "exists"
	// PackFailed: the record is damaged, or the document could not be saved (Err says why).
	PackFailed PackReason = "failed"
)

// PackIssue is a document an export or import left out. These are warnings: they do not stop
// the operation.
type PackIssue struct {
	ID, Name string
	Reason   PackReason
	Err      error
}

// ExportOptions are the options of ExportPack. The zero value exports every document, of
// a target with no confidential ones, unsigned.
type ExportOptions struct {
	// Folder limits the export to a folder and the folders below it ("" for everything).
	Folder string
	// Name names the pack: by default the folder's name, or "documents".
	Name string
	// Seal encrypts a confidential document for the pack: plain is its .open message, the
	// result an envelope the pack stores as it is (an encrypted record; the pack's index
	// lists ConfidentialName and no tags for it, the real name travels inside the envelope).
	// With no Seal such documents are left out and reported as PackNeedsKey, never written
	// in the clear.
	Seal func(ctx context.Context, docID string, plain []byte) ([]byte, error)
	// Sign signs the pack when not nil.
	Sign ed25519.PrivateKey
}

// ExportReport is what ExportPack did.
type ExportReport struct {
	// Exported counts the documents written.
	Exported int
	// Skipped are the documents left out.
	Skipped []PackIssue
}

// clip cuts s to at most n bytes at a character boundary (the pack's index limits names to
// 256 bytes; the document keeps its whole name).
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func packTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if len(t) <= pack.MaxName && len(out) < pack.MaxTags {
			out = append(out, t)
		}
	}
	return out
}

// ExportPack writes the documents of the target (those in the trash too, marked) and their
// folders to w as a pack. A folder's name longer than the pack's 256 bytes is cut in the
// index. The documents are read as Load reads them (a damaged one is read from its previous
// copy; one with no good copy stops the export, as it does Archive).
func (s *Saver) ExportPack(ctx context.Context, target string, w io.Writer, opt ExportOptions) (ExportReport, error) {
	var rep ExportReport
	type item struct {
		id    string
		entry catalogEntry
	}
	var items []item
	var folders []Folder
	var t Target
	name := "documents"
	err := s.view(ctx, target, func(tg Target, cat *catalog) error {
		t = tg
		if opt.Folder != "" {
			f, ok := cat.Folders[opt.Folder]
			if !ok {
				return ErrFolder
			}
			name = f.Name
		}
		for id, f := range cat.Folders {
			if opt.Folder == "" || cat.inside(id, opt.Folder) {
				if id == opt.Folder {
					f.Parent = "" // the pack's top
				}
				folders = append(folders, Folder{ID: id, Name: f.Name, Parent: f.Parent})
			}
		}
		for id, e := range cat.Docs {
			if opt.Folder == "" || cat.inside(e.Folder, opt.Folder) {
				items = append(items, item{id, e})
			}
		}
		return nil
	})
	if err != nil {
		return rep, err
	}
	if opt.Name != "" {
		name = opt.Name
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	sort.Slice(folders, func(i, j int) bool { return folders[i].ID < folders[j].ID })
	pw, err := pack.NewWriter(w, pack.Meta{ID: docfile.NewID(), Name: clip(name, pack.MaxName), Created: time.Now().UnixNano(), Producer: "gojiberry saver"})
	if err != nil {
		return rep, err
	}
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if it.entry.confidential() && opt.Seal == nil {
			rep.Skipped = append(rep.Skipped, PackIssue{ID: it.id, Name: it.entry.Name, Reason: PackNeedsKey})
			continue
		}
		doc, _, _, err := s.readEntry(ctx, t, it.entry)
		switch {
		case errors.Is(err, docstore.ErrNotFound):
			continue // deleted since the catalog was read
		case errors.Is(err, docstore.ErrLocked):
			rep.Skipped = append(rep.Skipped, PackIssue{ID: it.id, Name: it.entry.Name, Reason: PackNeedsKey, Err: err})
			continue
		case errors.Is(err, ErrUnreadable):
			return rep, fmt.Errorf("saver: %q is damaged and has no good copy: %w", it.entry.Name, err)
		case err != nil:
			return rep, err
		}
		doc.SetID(it.id)
		it.entry.withCatalog(&doc)
		doc.SetTrashed(it.entry.Trashed)
		data, err := docfile.Open.Marshal(doc)
		if err != nil {
			return rep, err
		}
		e := pack.Entry{ID: it.id, Name: clip(it.entry.Name, pack.MaxName), Folder: it.entry.Folder, Tags: packTags(it.entry.Tags), Updated: doc.Updated, Trashed: it.entry.Trashed != 0}
		if it.entry.confidential() {
			env, err := opt.Seal(ctx, it.id, data)
			if err != nil {
				return rep, fmt.Errorf("saver: sealing %s: %w", it.id, err)
			}
			if len(env) == 0 {
				return rep, fmt.Errorf("saver: sealing %s gave nothing", it.id)
			}
			err = pw.AddEncrypted(e, env)
			if err != nil {
				return rep, err
			}
		} else if err := pw.AddRaw(e, data); err != nil {
			return rep, err
		}
		rep.Exported++
	}
	for _, f := range folders {
		if err := pw.AddFolder(pack.Folder{ID: f.ID, Name: clip(f.Name, pack.MaxName), Parent: f.Parent}); err != nil {
			return rep, err
		}
	}
	return rep, pw.Close(opt.Sign)
}

// ImportOptions are the options of ImportPack.
type ImportOptions struct {
	// Open decrypts the envelope of an encrypted record (docID is the record's, the index's
	// ID) and returns the .open message inside. With no Open, or when it fails, the record is
	// left out and reported as PackNeedsKey: a warning, never an error that stops the
	// import.
	Open func(ctx context.Context, docID string, env []byte) ([]byte, error)
	// Signer, when not empty, requires the pack to be signed by this Ed25519 public key
	// (and the signature valid) before anything is imported (ErrPackSignature otherwise).
	Signer ed25519.PublicKey
}

// ErrPackSignature is returned by ImportPack when the pack's signature is not the one
// required.
var ErrPackSignature = errors.New("saver: the pack is not signed by the expected key")

// ImportReport is what ImportPack did.
type ImportReport struct {
	// Imported are the IDs of the documents saved.
	Imported []string
	// Folders counts the folders created (those the target already has are left alone).
	Folders int
	// Skipped are the documents not imported, with the reason.
	Skipped []PackIssue
}

// ImportPack saves the documents and folders of a pack (r, of the given size) to the target,
// under the IDs they have in it: a document the target already has is left as it is
// (PackExists), one in the trash is trashed again, tags and folders are kept. A damaged
// record or one that cannot be saved is reported and the rest imported; so is an encrypted
// record without a working Open. Only a pack that cannot be read at all is an error.
//
// A confidential document is saved confidential again (and is subject to
// Saver.ConfidentialCheck): the pack's own marker for it is the record's flag.
func (s *Saver) ImportPack(ctx context.Context, target string, r io.ReaderAt, size int64, opt ImportOptions) (ImportReport, error) {
	var rep ImportReport
	pr, err := pack.Open(r, size)
	if err != nil {
		return rep, err
	}
	if len(opt.Signer) > 0 {
		sig, valid := pr.Signature()
		if sig == nil || !valid || !ed25519.PublicKey(sig.PublicKey).Equal(opt.Signer) {
			return rep, ErrPackSignature
		}
	}
	c, err := s.importCodec()
	if err != nil {
		return rep, err
	}
	idx := pr.Index()
	if rep.Folders, err = s.importFolders(ctx, target, idx.Folders); err != nil {
		return rep, err
	}
	for _, e := range idx.Entries {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		skip := func(reason PackReason, err error) {
			rep.Skipped = append(rep.Skipped, PackIssue{ID: e.ID, Name: e.Name, Reason: reason, Err: err})
		}
		if _, err := s.Get(ctx, target, e.ID); err == nil {
			skip(PackExists, nil)
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return rep, err
		}
		var openErr error
		var open func([]byte) ([]byte, error)
		if e.Encrypted && opt.Open != nil {
			open = func(env []byte) ([]byte, error) {
				plain, err := opt.Open(ctx, e.ID, env)
				openErr = err
				return plain, err
			}
		}
		doc, err := pr.ReadWith(e.ID, open)
		switch {
		case errors.Is(err, pack.ErrEncrypted):
			skip(PackNeedsKey, nil)
			continue
		case openErr != nil:
			if ctx.Err() != nil {
				return rep, ctx.Err()
			}
			skip(PackNeedsKey, openErr)
			continue
		case err != nil:
			skip(PackFailed, err)
			continue
		}
		trashed := e.Trashed || doc.Trashed() != 0
		doc.SetTrashed(0) // the catalog's flag is set by Trash below
		if e.Encrypted {
			doc.SetProtection(ProtectionE2EE)
		}
		if _, err := s.Save(ctx, target, c, doc); err != nil {
			skip(PackFailed, err)
			continue
		}
		if trashed {
			if err := s.Trash(ctx, target, e.ID); err != nil {
				skip(PackFailed, err) // saved, but not trashed: listed as imported too
			}
		}
		rep.Imported = append(rep.Imported, e.ID)
	}
	return rep, nil
}

// importCodec is the format imported documents are saved in: JSON when the saver has it,
// else its first.
func (s *Saver) importCodec() (docfile.Codec, error) {
	if c, err := s.Codec(docfile.JSON.Name()); err == nil {
		return c, nil
	}
	if cs := s.codecs(); len(cs) > 0 {
		return cs[0], nil
	}
	return nil, errors.New("saver: no format to save in")
}

// importFolders creates the pack's folders the target does not have, parents first. A folder
// whose parent is missing or that would nest too deep goes to the top.
func (s *Saver) importFolders(ctx context.Context, target string, folders []pack.Folder) (int, error) {
	created := 0
	err := s.update(ctx, target, func(t Target, cat *catalog, _ *[]func()) error {
		created = 0 // update may run this again
		byID := map[string]pack.Folder{}
		for _, f := range folders {
			byID[f.ID] = f
		}
		var add func(id string, depth int) error
		add = func(id string, depth int) error {
			f, ok := byID[id]
			if !ok || depth > MaxFolderDepth {
				return nil
			}
			if _, have := cat.Folders[id]; have {
				return nil
			}
			parent := f.Parent
			if parent != "" {
				if err := add(parent, depth+1); err != nil {
					return err
				}
				if _, ok := cat.Folders[parent]; !ok || cat.depth(parent) >= MaxFolderDepth {
					parent = ""
				}
			}
			fe := folderEntry{Name: f.Name, Parent: parent}
			if !ValidName(f.Name) {
				fe.Name = f.ID
			}
			if err := s.writeFolder(ctx, t, id, fe); err != nil {
				return err
			}
			cat.Folders[id] = fe
			created++
			return nil
		}
		for _, f := range folders {
			if err := add(f.ID, 0); err != nil {
				return err
			}
		}
		return nil
	})
	return created, err
}
