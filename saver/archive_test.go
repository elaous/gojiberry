package saver

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

func TestArchiveDrawsFoldersAsDirectories(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		work, _ := s.CreateFolder(ctx, tg.Name, "", "Work")
		sub, _ := s.CreateFolder(ctx, tg.Name, work.ID, "2026/Q1") // a slash in a name is not a directory
		save := func(name, folder string) Entry {
			d := named(name)
			d.SetFolder(folder)
			e, err := s.Save(ctx, tg.Name, docfile.JSON, d)
			if err != nil {
				t.Fatal(err)
			}
			return e
		}
		save("top", "")
		save("plan", work.ID)
		save("plan", work.ID) // same name twice
		save("budget", sub.ID)
		gone := save("gone", work.ID)
		if err := s.Trash(ctx, tg.Name, gone.ID); err != nil {
			t.Fatal(err)
		}

		data, name, err := s.Archive(ctx, tg.Name, work.ID)
		if err != nil || name != "Work" {
			t.Fatalf("%s: %q %v", tg.Name, name, err)
		}
		got := zipNames(t, data)
		if len(got) != 3 || got[0] != "2026-Q1/budget.json" || got[2] != "plan.json" || !strings.HasPrefix(got[1], "plan (") {
			t.Fatalf("%s: %v", tg.Name, got)
		}
		plans := 0
		for _, n := range got {
			if strings.HasPrefix(n, "plan") {
				plans++
			}
			if strings.Contains(n, "gone") || strings.Contains(n, "top") {
				t.Errorf("%s: %q does not belong", tg.Name, n)
			}
		}
		if plans != 2 {
			t.Errorf("%s: two documents named plan: %v", tg.Name, got)
		}
		// The archive holds the document as saved.
		zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		for _, f := range zr.File {
			if strings.HasSuffix(f.Name, "budget.json") {
				rc, _ := f.Open()
				b, _ := io.ReadAll(rc)
				if d, err := docfile.JSON.Unmarshal(b); err != nil || d.Name() != "budget" {
					t.Errorf("%s: %v %q", tg.Name, err, d.Name())
				}
			}
		}

		all, name, _ := s.Archive(ctx, tg.Name, "")
		if name != "documents" || len(zipNames(t, all)) != 4 {
			t.Errorf("%s: whole store: %q %v", tg.Name, name, zipNames(t, all))
		}
		if _, _, err := s.Archive(ctx, tg.Name, docfile.NewID()); err == nil {
			t.Errorf("%s: a folder that is not there", tg.Name)
		}
	}
}

func TestArchiveSkipsALockedConfidentialDocumentAndReportsIt(t *testing.T) {
	ctx := context.Background()
	s, mem, secret := confidentialSaver(t)
	if _, err := s.Save(ctx, "m", docfile.JSON, named("plain")); err != nil {
		t.Fatal(err)
	}
	// Open: it is in the archive under its real name.
	data, _, skipped, err := s.ArchiveReport(ctx, "m", "")
	if err != nil || len(skipped) != 0 || len(zipNames(t, data)) != 2 {
		t.Fatalf("%v %v %v", zipNames(t, data), skipped, err)
	}
	// Locked: the others come, the locked one is skipped and reported.
	mem.locked.Store(true)
	mem.only = secret
	data, _, skipped, err = s.ArchiveReport(ctx, "m", "")
	if err != nil || len(skipped) != 1 || skipped[0].ID != secret || !errors.Is(skipped[0].Err, docstore.ErrLocked) {
		t.Fatalf("%v %v", skipped, err)
	}
	if got := zipNames(t, data); len(got) != 1 || got[0] != "plain.json" {
		t.Errorf("%v", got)
	}
	if _, _, err := s.Archive(ctx, "m", ""); err != nil {
		t.Errorf("Archive: %v", err)
	}
}
