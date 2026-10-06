package saver

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/docstore"
)

// Archive returns a ZIP of the documents in a folder and, below it, its
// subfolders ("" for every document of the target). Folders become directories
// (they are virtual in the store; the archive is where they are drawn), each
// document is stored as saved, named after its name with the extension of its
// format, and documents in the trash are left out. Names that would repeat in a
// directory get their ID's first eight characters. The second result is a name
// for the archive: the folder's, or "documents" for the top level.
//
// A confidential document is in the archive as its open file, plaintext, under its real name
// (the user asked for it). One that no key opens here is skipped, not an error; Archive does
// not say which, ArchiveReport does.
func (s *Saver) Archive(ctx context.Context, target, folder string) (data []byte, name string, err error) {
	data, name, _, err = s.ArchiveReport(ctx, target, folder)
	return data, name, err
}

// ArchiveSkip is a document left out of an archive: a confidential one that is locked here.
type ArchiveSkip struct {
	ID, Name string
	Err      error
}

// ArchiveReport is Archive that also returns the documents it left out because they are
// confidential and locked (no key here opens them: a warning to show, not a reason to give up
// the whole archive). The name is the placeholder, the real one being inside.
func (s *Saver) ArchiveReport(ctx context.Context, target, folder string) (data []byte, name string, skipped []ArchiveSkip, err error) {
	name = "documents"
	type item struct {
		id, name, dir string
		entry         catalogEntry
	}
	var items []item
	var t Target
	// Read the catalog under the saver's lock, then let go of it: loading every document
	// of a big folder must not hold up the other operations.
	err = s.view(ctx, target, func(tg Target, cat *catalog) error {
		t = tg
		if folder != "" {
			f, ok := cat.Folders[folder]
			if !ok {
				return ErrFolder
			}
			name = f.Name
		}
		// dir is the path of a folder inside the archive, built from the names.
		dir := func(id string) string {
			var parts []string
			for n := 0; id != "" && n <= MaxFolderDepth; n++ {
				f, ok := cat.Folders[id]
				if !ok {
					break
				}
				part := safeName(f.Name)
				if f.Protection == ProtectionE2EE {
					// Every confidential folder is called the placeholder: the ID tells them apart.
					part += " (" + id[:min(8, len(id))] + ")"
				}
				parts = append([]string{part}, parts...)
				id = f.Parent
			}
			return path.Join(parts...)
		}
		root := dir(folder)
		for id, e := range cat.Docs {
			if e.Trashed == 0 && (folder == "" || e.Folder == folder || cat.inside(e.Folder, folder)) {
				items = append(items, item{id: id, name: e.Name, dir: strings.TrimPrefix(strings.TrimPrefix(dir(e.Folder), root), "/"), entry: e})
			}
		}
		return nil
	})
	if err != nil {
		return nil, name, nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := map[string]bool{}
	for _, it := range items {
		doc, data, _, err := s.readEntry(ctx, t, it.entry)
		if errors.Is(err, docstore.ErrNotFound) {
			continue // deleted since the catalog was read
		}
		if it.entry.confidential() && errors.Is(err, docstore.ErrLocked) {
			skipped = append(skipped, ArchiveSkip{ID: it.id, Name: it.name, Err: err})
			continue // no key opens it here: left out, and reported
		}
		if errors.Is(err, ErrUnreadable) {
			// An archive is a backup: one that quietly lacks a document is worse than
			// one that says so.
			return nil, name, nil, fmt.Errorf("saver: %q is damaged and has no good copy: %w", it.name, err)
		}
		if err != nil {
			return nil, name, nil, err
		}
		ext := path.Ext(it.entry.Key)
		if it.entry.confidential() {
			it.name = doc.Name() // the file is open here: it is named after what it is
		}
		full := path.Join(it.dir, safeName(it.name)+ext)
		if used[strings.ToLower(full)] {
			full = path.Join(path.Dir(full), safeName(it.name)+" ("+it.id[:8]+")"+ext)
		}
		used[strings.ToLower(full)] = true
		w, err := zw.Create(full)
		if err != nil {
			return nil, name, nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, name, nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, name, nil, err
	}
	return buf.Bytes(), name, skipped, nil
}

// safeName is a name as one path element: no separators, nothing hidden.
func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 0x20 {
			return '-'
		}
		return r
	}, n)
	n = strings.Trim(strings.TrimSpace(n), ".")
	if n == "" {
		return "untitled"
	}
	return n
}
