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

func (s *Saver) Archive(ctx context.Context, target, folder string) (data []byte, name string, err error) {
	data, name, _, err = s.ArchiveReport(ctx, target, folder)
	return data, name, err
}

type ArchiveSkip struct {
	ID, Name string
	Err      error
}

func (s *Saver) ArchiveReport(ctx context.Context, target, folder string) (data []byte, name string, skipped []ArchiveSkip, err error) {
	name = "documents"
	type item struct {
		id, name, dir string
		entry         catalogEntry
	}
	var items []item
	var t Target
	err = s.view(ctx, target, func(tg Target, cat *catalog) error {
		t = tg
		if folder != "" {
			f, ok := cat.Folders[folder]
			if !ok {
				return ErrFolder
			}
			name = f.Name
		}
		dir := func(id string) string {
			var parts []string
			for n := 0; id != "" && n <= MaxFolderDepth; n++ {
				f, ok := cat.Folders[id]
				if !ok {
					break
				}
				part := safeName(f.Name)
				if f.Protection == ProtectionE2EE {
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
			return nil, name, nil, fmt.Errorf("saver: %q is damaged and has no good copy: %w", it.name, err)
		}
		if err != nil {
			return nil, name, nil, err
		}
		ext := path.Ext(it.entry.Key)
		if it.entry.confidential() {
			it.name = doc.Name()
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
