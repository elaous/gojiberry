package saver

import (
	"archive/zip"
	"bytes"
	"context"
	"path"
	"sort"
	"strings"
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
	err = s.view(ctx, target, func(t Target, cat *catalog) error {
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
		ids := make([]string, 0, len(cat.Docs))
		for id, e := range cat.Docs {
			if e.Trashed == 0 && (folder == "" || e.Folder == folder || cat.inside(e.Folder, folder)) {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		used := map[string]bool{}
		for _, id := range ids {
			e := cat.Docs[id]
			stored, err := t.Adapter.Load(ctx, e.Key)
			if err != nil {
				return err
			}
			file := safeName(e.Name) + path.Ext(e.Key)
			full := path.Join(strings.TrimPrefix(strings.TrimPrefix(dir(e.Folder), root), "/"), file)
			if used[strings.ToLower(full)] {
				full = path.Join(path.Dir(full), safeName(e.Name)+" ("+id[:8]+")"+path.Ext(e.Key))
			}
			used[strings.ToLower(full)] = true
			w, err := zw.Create(full)
			if err != nil {
				return err
			}
			if _, err := w.Write(stored.Data); err != nil {
				return err
			}
		}
		if err := zw.Close(); err != nil {
			return err
		}
		data = buf.Bytes()
		return nil
	})
	return data, name, err
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
