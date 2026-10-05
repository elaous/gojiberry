package saver

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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
func (s *Saver) Archive(ctx context.Context, target, folder string) (data []byte, name string, err error) {
	name = "documents"
	type item struct{ id, key, name, dir string }
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
				parts = append([]string{safeName(f.Name)}, parts...)
				id = f.Parent
			}
			return path.Join(parts...)
		}
		root := dir(folder)
		for id, e := range cat.Docs {
			if e.Trashed == 0 && (folder == "" || e.Folder == folder || cat.inside(e.Folder, folder)) {
				items = append(items, item{id, e.Key, e.Name, strings.TrimPrefix(strings.TrimPrefix(dir(e.Folder), root), "/")})
			}
		}
		return nil
	})
	if err != nil {
		return nil, name, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := map[string]bool{}
	for _, it := range items {
		stored, err := t.Adapter.Load(ctx, it.key)
		if errors.Is(err, docstore.ErrNotFound) {
			continue // deleted since the catalog was read
		}
		if err != nil {
			return nil, name, err
		}
		ext := path.Ext(it.key)
		full := path.Join(it.dir, safeName(it.name)+ext)
		if used[strings.ToLower(full)] {
			full = path.Join(path.Dir(full), safeName(it.name)+" ("+it.id[:8]+")"+ext)
		}
		used[strings.ToLower(full)] = true
		w, err := zw.Create(full)
		if err != nil {
			return nil, name, err
		}
		if _, err := w.Write(stored.Data); err != nil {
			return nil, name, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, name, err
	}
	return buf.Bytes(), name, nil
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
