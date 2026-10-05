package saver

import (
	"context"
	"errors"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

func TestFoldersHoldDocumentsAndFolders(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		work, err := s.CreateFolder(ctx, tg.Name, "", "Work")
		if err != nil || !docfile.ValidID(work.ID) {
			t.Fatalf("%s: %+v %v", tg.Name, work, err)
		}
		// Folders may share a name, like documents.
		sub1, _ := s.CreateFolder(ctx, tg.Name, work.ID, "2026")
		sub2, _ := s.CreateFolder(ctx, tg.Name, "", "2026")
		if sub1.ID == sub2.ID || sub1.Parent != work.ID || sub2.Parent != "" {
			t.Fatalf("%s: %+v %+v", tg.Name, sub1, sub2)
		}
		// A new document goes where its meta says; one in a missing folder goes to the top.
		d := named("plan")
		d.SetFolder(sub1.ID)
		inSub, err := s.Save(ctx, tg.Name, docfile.JSON, d)
		if err != nil || inSub.Folder != sub1.ID {
			t.Fatalf("%s: %+v %v", tg.Name, inSub, err)
		}
		d2 := named("lost")
		d2.SetFolder(docfile.NewID())
		if e, _ := s.Save(ctx, tg.Name, docfile.JSON, d2); e.Folder != "" {
			t.Errorf("%s: a missing folder: %q", tg.Name, e.Folder)
		}
		// Saving the document again does not move it; Move does, and the document agrees.
		again := named("plan")
		again.SetID(inSub.ID)
		if e, _ := s.Save(ctx, tg.Name, docfile.JSON, again); e.Folder != sub1.ID {
			t.Errorf("%s: saving moved the document to %q", tg.Name, e.Folder)
		}
		if err := s.Move(ctx, tg.Name, work.ID, inSub.ID); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Load(ctx, tg.Name, inSub.ID)
		if got.Folder() != work.ID {
			t.Errorf("%s: the document says %q", tg.Name, got.Folder())
		}
		if err := s.Move(ctx, tg.Name, docfile.NewID(), inSub.ID); !errors.Is(err, ErrFolder) {
			t.Errorf("%s: a move to a missing folder: %v", tg.Name, err)
		}
		if err := s.Move(ctx, tg.Name, "", inSub.ID, docfile.NewID()); !errors.Is(err, docstore.ErrNotFound) {
			t.Errorf("%s: a move of a missing document: %v", tg.Name, err)
		}
		if g, _ := s.Load(ctx, tg.Name, inSub.ID); g.Folder() != work.ID {
			t.Errorf("%s: a failed move changed the document", tg.Name)
		}
		// Folder operations.
		if err := s.RenameFolder(ctx, tg.Name, work.ID, "Job"); err != nil {
			t.Fatal(err)
		}
		if err := s.MoveFolder(ctx, tg.Name, work.ID, sub1.ID); !errors.Is(err, ErrFolder) {
			t.Errorf("%s: a folder into its own subfolder: %v", tg.Name, err)
		}
		if err := s.MoveFolder(ctx, tg.Name, sub1.ID, sub2.ID); err != nil {
			t.Fatal(err)
		}
		fs, _ := s.Folders(ctx, tg.Name)
		path := FolderPath(fs, sub1.ID)
		if len(path) != 2 || path[0].ID != sub2.ID || path[1].ID != sub1.ID {
			t.Errorf("%s: path %+v", tg.Name, path)
		}
		// Deleting needs an empty folder; a trashed document in it goes to the top level.
		if err := s.DeleteFolder(ctx, tg.Name, sub2.ID); err == nil {
			t.Errorf("%s: deleted a folder with a folder in it", tg.Name)
		}
		if err := s.DeleteFolder(ctx, tg.Name, work.ID); err == nil {
			t.Errorf("%s: deleted a folder with a document in it", tg.Name)
		}
		_ = s.Trash(ctx, tg.Name, inSub.ID)
		if err := s.DeleteFolder(ctx, tg.Name, work.ID); err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		_ = s.Restore(ctx, tg.Name, inSub.ID)
		if e, _ := s.Get(ctx, tg.Name, inSub.ID); e.Folder != "" {
			t.Errorf("%s: the restored document is in %q", tg.Name, e.Folder)
		}
		if err := s.DeleteFolder(ctx, tg.Name, sub1.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteFolder(ctx, tg.Name, sub2.ID); err != nil {
			t.Fatal(err)
		}
		if fs, _ = s.Folders(ctx, tg.Name); len(fs) != 0 {
			t.Errorf("%s: %+v", tg.Name, fs)
		}
	}
}

func TestFoldersSurviveARebuildAndHaveALimit(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	empty, _ := s.CreateFolder(ctx, "m", "", "Empty")
	full, _ := s.CreateFolder(ctx, "m", "", "Full")
	d := named("x")
	d.SetFolder(full.ID)
	e, _ := s.Save(ctx, "m", docfile.JSON, d)
	_ = mem.Delete(ctx, CatalogKey)
	fs, err := s.Folders(ctx, "m")
	if err != nil || len(fs) != 2 {
		t.Fatalf("%+v %v", fs, err)
	}
	if got, _ := s.Get(ctx, "m", e.ID); got.Folder != full.ID {
		t.Errorf("the document lost its folder: %q", got.Folder)
	}
	_ = empty
	parent := ""
	var err2 error
	for range MaxFolderDepth + 2 {
		var f Folder
		f, err2 = s.CreateFolder(ctx, "m", parent, "deeper")
		if err2 != nil {
			break
		}
		parent = f.ID
	}
	if err2 == nil {
		t.Error("folders nest without limit")
	}
	for _, bad := range []string{"", "bad\x00"} {
		if _, err := s.CreateFolder(ctx, "m", "", bad); !errors.Is(err, ErrName) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := s.CreateFolder(ctx, "m", docfile.NewID(), "x"); !errors.Is(err, ErrFolder) {
		t.Errorf("a missing parent: %v", err)
	}
}
