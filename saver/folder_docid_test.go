package saver

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
)

func hooks(s *Saver) (checked, rekeyed *[]string) {
	checked, rekeyed = new([]string), new([]string)
	s.ConfidentialCheck = func(_ context.Context, _, id string) error { *checked = append(*checked, id); return nil }
	s.Rekey = func(_ context.Context, _, id, _ string) error { *rekeyed = append(*rekeyed, id); return nil }
	return
}

func TestSaveRefusesAFoldersID(t *testing.T) {
	ctx := context.Background()
	s, _, top, sec, _ := folderWorld(t)
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	checked, rekeyed := hooks(s)
	for _, id := range []string{top, sec} {
		for _, e2ee := range []bool{false, true} {
			d := docWithText("Evil", "x")
			d.SetID(id)
			if e2ee {
				d.SetProtection(ProtectionE2EE)
			}
			if _, err := s.Save(ctx, "m", docfile.Open, d); !errors.Is(err, ErrFolderID) {
				t.Errorf("Save over folder %s (e2ee %v): %v", id, e2ee, err)
			}
			if _, err := s.save(ctx, "m", docfile.Open, d, true); !errors.Is(err, ErrFolderID) {
				t.Errorf("save e2ee over folder %s: %v", id, err)
			}
		}
		if _, err := s.Get(ctx, "m", id); !errors.Is(err, ErrNotFound) {
			t.Errorf("the folder became a document: %v", err)
		}
	}
	if len(*checked) != 0 || len(*rekeyed) != 0 {
		t.Errorf("the hooks ran: %v %v", *checked, *rekeyed)
	}
	if name, err := s.RevealFolder(ctx, "m", sec); err != nil || name != secretFolder {
		t.Errorf("%q %v", name, err)
	}
	if err := s.RenameFolder(ctx, "m", top, "Still a folder"); err != nil {
		t.Errorf("%v", err)
	}
}

func TestSessionSaveOfAnImportedOpenFileWithAFoldersID(t *testing.T) {
	ctx := context.Background()
	var imported docfile.Document
	s := NewSession(&Saver{Targets: targets(t)}, func() docfile.Document { return imported }, func(docfile.Document) {})
	s.Pending.Inline = true
	target := s.Saver.TargetList()[0].Name
	if err := s.SetTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	f, err := s.Saver.CreateFolder(ctx, target, "", "Work")
	if err != nil {
		t.Fatal(err)
	}
	checked, rekeyed := hooks(s.Saver)
	crafted := docfile.FromFlow("Evil", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "x"}}})
	crafted.SetID(f.ID)
	crafted.SetProtection(ProtectionE2EE)
	data, err := docfile.Open.Marshal(crafted)
	if err != nil {
		t.Fatal(err)
	}
	imported, err = docfile.Open.Unmarshal(data)
	if err != nil || imported.ID() != f.ID {
		t.Fatalf("%q %v", imported.ID(), err)
	}
	s.Detach()
	s.SetName("Evil")
	if err := s.Save(ctx); !errors.Is(err, ErrFolderID) {
		t.Fatalf("%v", err)
	}
	if s.ID() != "" {
		t.Errorf("the session took the folder's ID: %q", s.ID())
	}
	if len(*checked) != 0 || len(*rekeyed) != 0 {
		t.Errorf("the hooks ran: %v %v", *checked, *rekeyed)
	}
	if fs, _ := s.Saver.Folders(ctx, target); len(fs) != 1 || fs[0].Name != "Work" {
		t.Errorf("%+v", fs)
	}
}

func packWith(t *testing.T, folders []pack.Folder, docs ...docfile.Document) ([]byte, int64) {
	t.Helper()
	var buf bytes.Buffer
	pw, err := pack.NewWriter(&buf, pack.Meta{ID: docfile.NewID(), Name: "crafted"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if err := pw.AddFolder(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range docs {
		if err := pw.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := pw.Close(nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), int64(buf.Len())
}

func TestImportPackDocumentWithAnExistingFoldersIDIsRemapped(t *testing.T) {
	ctx := context.Background()
	s, _, top, sec, _ := folderWorld(t)
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	checked, rekeyed := hooks(s)
	for _, id := range []string{top, sec} {
		d := docWithText("Evil "+id[:4], "x")
		d.SetID(id)
		data, n := packWith(t, nil, d)
		for _, conflict := range []ConflictPolicy{ConflictSkip, ConflictReplace, ConflictCopy} {
			rep, err := s.ImportPack(ctx, "m", bytes.NewReader(data), n, ImportOptions{Conflict: conflict})
			if err != nil || rep.Documents != 1 || len(rep.Failed) != 0 {
				t.Fatalf("%v: %+v %v", conflict, rep, err)
			}
		}
		if _, err := s.Get(ctx, "m", id); !errors.Is(err, ErrNotFound) {
			t.Errorf("a document took folder %s: %v", id, err)
		}
	}
	if slices.Contains(*checked, top) || slices.Contains(*checked, sec) || slices.Contains(*rekeyed, top) || slices.Contains(*rekeyed, sec) {
		t.Errorf("the hooks ran on a folder: %v %v", *checked, *rekeyed)
	}
	if name, err := s.RevealFolder(ctx, "m", sec); err != nil || name != secretFolder {
		t.Errorf("%q %v", name, err)
	}
}

func TestImportPackDocumentWithAPackFoldersIDIsRemapped(t *testing.T) {
	ctx := context.Background()
	s := memSaver()
	checked, rekeyed := hooks(s)
	fid := docfile.NewID()
	d := docWithText("Evil", "x")
	d.SetID(fid)
	data, n := packWith(t, []pack.Folder{{ID: fid, Name: "Dir"}}, d)
	rep, err := s.ImportPack(ctx, "m", bytes.NewReader(data), n, ImportOptions{})
	if err != nil || rep.Folders != 1 || rep.Documents != 1 || len(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := s.Get(ctx, "m", fid); err != nil {
		if fs, _ := s.Folders(ctx, "m"); len(fs) != 1 || fs[0].ID != fid {
			t.Errorf("neither kept the ID: %+v", fs)
		}
	} else if fs, _ := s.Folders(ctx, "m"); len(fs) != 1 || fs[0].ID == fid {
		t.Errorf("the folder and the document share an ID: %+v", fs)
	}
	if len(*checked) != 0 || len(*rekeyed) != 0 {
		t.Errorf("the hooks ran: %v %v", *checked, *rekeyed)
	}
}

func TestFolderOperationsRefuseADocumentsID(t *testing.T) {
	ctx := context.Background()
	s, _, top, _, doc := folderWorld(t)
	if err := s.MoveFolder(ctx, "m", doc, top); !errors.Is(err, ErrFolderID) {
		t.Errorf("MoveFolder: %v", err)
	}
	if _, err := s.DeleteFolderReport(ctx, "m", doc); !errors.Is(err, ErrFolderID) {
		t.Errorf("DeleteFolderReport: %v", err)
	}
	if err := s.RenameFolder(ctx, "m", doc, "x"); !errors.Is(err, ErrFolderID) {
		t.Errorf("RenameFolder: %v", err)
	}
}
