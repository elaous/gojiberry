package saver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
	"time"

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

func TestRebuildRepairsAFolderWhoseParentIsGone(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	parent, _ := s.CreateFolder(ctx, "m", "", "Parent")
	child, _ := s.CreateFolder(ctx, "m", parent.ID, "Child")
	_ = mem.Delete(ctx, FolderRecordPrefix+parent.ID) // a record that did not survive
	_ = mem.Delete(ctx, CatalogKey)
	fs, err := s.Folders(ctx, "m")
	if err != nil || len(fs) != 1 || fs[0].ID != child.ID || fs[0].Parent != "" {
		t.Fatalf("the child must go to the top level, not hang from a missing parent: %+v %v", fs, err)
	}
}

func TestMoveLeavesDocumentsAlreadyThereAlone(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	f, _ := s.CreateFolder(ctx, "m", "", "F")
	d := named("a")
	d.SetFolder(f.ID)
	e, _ := s.Save(ctx, "m", docfile.JSON, d)
	before, _ := mem.Load(ctx, e.Key)
	if err := s.Move(ctx, "m", f.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := mem.Load(ctx, e.Key)
	if string(before.Data) != string(after.Data) {
		t.Error("a document already in the folder was rewritten")
	}
}

func TestRepairFindsWhatACrashLeftBehind(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}, RepairGrace: -1}
	keep, _ := s.CreateFolder(ctx, "m", "", "Keep")
	e, _ := s.Save(ctx, "m", docfile.JSON, named("a"))
	if fixed, err := s.Repair(ctx, "m"); err != nil || fixed {
		t.Fatalf("a consistent store needs no repair: %v %v", fixed, err)
	}
	// A folder whose catalog write never happened: its record is an orphan.
	orphan, _ := s.CreateFolder(ctx, "m", "", "Orphan")
	c, _ := s.readCatalogForTest(ctx, "m")
	delete(c.Folders, orphan.ID)
	data, _ := json.Marshal(c)
	_ = mem.Save(ctx, CatalogKey, data)
	if fixed, _ := s.Repair(ctx, "m"); !fixed {
		t.Fatal("an orphan record is a repair")
	}
	if fs, _ := s.Folders(ctx, "m"); len(fs) != 2 {
		t.Errorf("the folder should be back: %+v", fs)
	}
	// A listed document whose file is gone is dropped; a file nobody listed is adopted.
	_ = mem.Delete(ctx, e.Key)
	extra := named("late")
	extra.SetID(docfile.NewID())
	raw, _ := docfile.JSON.Marshal(extra)
	_ = mem.Save(ctx, extra.ID()+".json", raw)
	if fixed, _ := s.Repair(ctx, "m"); !fixed {
		t.Fatal("expected a repair")
	}
	list, _ := s.List(ctx, "m")
	if len(list) != 1 || list[0].Name != "late" {
		t.Errorf("%+v", list)
	}
	_ = keep
	if fixed, _ := s.Repair(ctx, "m"); fixed {
		t.Error("repair is idempotent")
	}
}

// A delete takes the document out of the catalog first and removes its file after.
// A Repair that lists the store in between must not take the file for a document
// nobody listed and bring it back.
func TestRepairDoesNotResurrectADocumentBeingDeleted(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}, RepairGrace: 200 * time.Millisecond}
	e, err := s.Save(ctx, "m", docfile.JSON, named("doomed"))
	if err != nil {
		t.Fatal(err)
	}
	// The catalog no longer lists it; its file is still there (the first half of a delete).
	c, _ := s.readCatalogForTest(ctx, "m")
	delete(c.Docs, e.ID)
	data, _ := json.Marshal(c)
	_ = mem.Save(ctx, CatalogKey, data)
	go func() { // the second half, a moment later
		time.Sleep(50 * time.Millisecond)
		_ = mem.Delete(ctx, e.Key)
	}()
	if repaired, err := s.Repair(ctx, "m"); err != nil || repaired {
		t.Fatalf("repaired=%v err=%v: the delete was in flight", repaired, err)
	}
	if live, _, _ := s.Lists(ctx, "m"); len(live) != 0 {
		t.Errorf("the deleted document came back: %+v", live)
	}
}

// Files the last rebuild left out (unreadable ones) are not work for every Repair.
func TestRepairLeavesWhatARebuildSkipped(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}, RepairGrace: -1}
	if _, err := s.Save(ctx, "m", docfile.JSON, named("fine")); err != nil {
		t.Fatal(err)
	}
	_ = mem.Save(ctx, docfile.NewID()+".json", []byte("not a document {"))
	if repaired, err := s.Repair(ctx, "m"); err != nil || !repaired {
		t.Fatalf("the unreadable file is found once: %v %v", repaired, err)
	}
	if repaired, err := s.Repair(ctx, "m"); err != nil || repaired {
		t.Fatalf("and then left alone: %v %v", repaired, err)
	}
	if live, _, _ := s.Lists(ctx, "m"); len(live) != 1 {
		t.Errorf("%+v", live)
	}
}

// A document in the trash whose file is gone or damaged cannot be moved out of its
// folder, and nobody can open it: deleting the folder removes it, and says so.
func TestDeleteFolderRemovesUnreadableTrashedDocuments(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	f, _ := s.CreateFolder(ctx, "m", "", "Old")
	mk := func(name string) Entry {
		d := named(name)
		d.SetFolder(f.ID)
		e, err := s.Save(ctx, "m", docfile.JSON, d)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Trash(ctx, "m", e.ID); err != nil {
			t.Fatal(err)
		}
		return e
	}
	good, missing, damaged := mk("good"), mk("missing"), mk("damaged")
	// Trashing wrote a new file for each: the current one is what the catalog says now.
	current := func(id string) Entry {
		e, err := s.Get(ctx, "m", id)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	// Both copies of these two are gone or wrong, or the previous one would stand in.
	for _, e := range []Entry{missing, damaged} {
		cur := current(e.ID)
		_ = mem.Delete(ctx, e.Key) // the previous copy (the file that was current before the trashing)
		if e.ID == missing.ID {
			_ = mem.Delete(ctx, cur.Key)
		} else {
			_ = mem.Save(ctx, cur.Key, []byte("{ not json"))
		}
	}
	damagedKey := current(damaged.ID).Key

	purged, err := s.DeleteFolderReport(ctx, "m", f.ID)
	if err != nil || len(purged) != 2 {
		t.Fatalf("purged %v, %v", purged, err)
	}
	sort.Strings(purged)
	if purged[0] != "damaged" || purged[1] != "missing" {
		t.Errorf("%v", purged)
	}
	trash, _ := s.Trashed(ctx, "m")
	if len(trash) != 1 || trash[0].ID != good.ID || trash[0].Folder != "" {
		t.Errorf("the readable one goes to the top level and stays in the trash: %+v", trash)
	}
	if _, err := mem.Load(ctx, damagedKey); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the damaged file is deleted: %v", err)
	}
	if folders, _ := s.Folders(ctx, "m"); len(folders) != 0 {
		t.Errorf("the folder is gone: %+v", folders)
	}

	// A store error that is not "the file is missing or damaged" still stops it.
	g, _ := s.CreateFolder(ctx, "m", "", "Flaky")
	d := named("flaky")
	d.SetFolder(g.ID)
	e, _ := s.Save(ctx, "m", docfile.JSON, d)
	_ = s.Trash(ctx, "m", e.ID)
	cur, _ := s.Get(ctx, "m", e.ID)
	s2 := &Saver{Targets: []Target{{Name: "m", Adapter: failingLoads{mem, cur.Key}}}}
	if _, err := s2.DeleteFolderReport(ctx, "m", g.ID); err == nil || errors.Is(err, ErrUnreadable) {
		t.Errorf("a transient failure is not a reason to delete anything: %v", err)
	}
	if _, err := mem.Load(ctx, cur.Key); err != nil {
		t.Errorf("the document was deleted: %v", err)
	}
}

// failingLoads fails loading one key with an error that is not "not found".
type failingLoads struct {
	docstore.Adapter
	key string
}

func (f failingLoads) Load(ctx context.Context, id string) (docstore.Document, error) {
	if id == f.key {
		return docstore.Document{}, errors.New("disk on fire")
	}
	return f.Adapter.Load(ctx, id)
}
