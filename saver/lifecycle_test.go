package saver

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

func saved(t *testing.T, s *Saver, target, name string, c docfile.Codec) Entry {
	t.Helper()
	e, err := s.Save(context.Background(), target, c, named(name))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestVersionsKeepHistoryNewestFirstAndPrune(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}, MaxVersions: 3}
		e := saved(t, s, tg.Name, "notes", docfile.JSON)
		for i := range 5 {
			d := named("notes")
			d.SetID(e.ID)
			d.Title = "v" + string(rune('0'+i))
			if _, err := s.SaveVersion(ctx, tg.Name, docfile.JSON, d); err != nil {
				t.Fatalf("%s: %v", tg.Name, err)
			}
		}
		vs, err := s.Versions(ctx, tg.Name, e.ID)
		if err != nil || len(vs) != 3 {
			t.Fatalf("%s: %d versions, %v", tg.Name, len(vs), err)
		}
		newest, err := s.LoadVersion(ctx, tg.Name, vs[0].Key)
		if err != nil || newest.Title != "v4" {
			t.Fatalf("%s: newest %q %v", tg.Name, newest.Title, err)
		}
		oldest, _ := s.LoadVersion(ctx, tg.Name, vs[2].Key)
		if oldest.Title != "v2" || !vs[0].Time.After(vs[2].Time) {
			t.Errorf("%s: pruning kept the wrong ones (%q)", tg.Name, oldest.Title)
		}
		if err := s.DeleteVersion(ctx, tg.Name, vs[1].Key); err != nil {
			t.Fatal(err)
		}
		if vs, _ = s.Versions(ctx, tg.Name, e.ID); len(vs) != 2 {
			t.Errorf("%s: after a delete: %d", tg.Name, len(vs))
		}
		// Versions are not documents, and another document's are separate.
		if list, _ := s.List(ctx, tg.Name); len(list) != 1 {
			t.Errorf("%s: versions leaked into the list: %v", tg.Name, list)
		}
		if other, _ := s.Versions(ctx, tg.Name, docfile.NewID()); len(other) != 0 {
			t.Errorf("%s: %v", tg.Name, other)
		}
		// Deleting the document takes its history with it.
		if err := s.Delete(ctx, tg.Name, e.ID); err != nil {
			t.Fatal(err)
		}
		if vs, _ = s.Versions(ctx, tg.Name, e.ID); len(vs) != 0 {
			t.Errorf("%s: history outlived the document: %d", tg.Name, len(vs))
		}
	}
}

func TestReservedKeysAreRefused(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: targets(t)[2:3]} // memory
	e := saved(t, s, "memory", "a", docfile.JSON)
	d := named("a")
	d.SetID(e.ID)
	v, err := s.SaveVersion(ctx, "memory", docfile.JSON, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, "memory", v.Key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load of a version key: %v", err)
	}
	if err := s.Delete(ctx, "memory", v.Key); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of a version key: %v", err)
	}
	if _, err := s.LoadVersion(ctx, "memory", e.Key); !errors.Is(err, ErrName) {
		t.Errorf("LoadVersion of a document key: %v", err)
	}
	if err := s.Trash(ctx, "memory", "versions/x.json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Trash of a version key: %v", err)
	}
	if _, err := s.SaveVersion(ctx, "memory", docfile.JSON, named("no id")); !errors.Is(err, ErrID) {
		t.Errorf("a version of a document with no ID: %v", err)
	}
	if _, err := s.Versions(ctx, "memory", "a/b"); !errors.Is(err, ErrID) {
		t.Errorf("an id with a slash: %v", err)
	}
	for _, key := range []string{"versions/../a.json", "versions/a.json", "versions/x/y/z.json"} {
		if _, err := s.LoadVersion(ctx, "memory", key); !errors.Is(err, ErrName) {
			t.Errorf("%q: %v", key, err)
		}
	}
}

func TestTrashRestorePurge(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		e := saved(t, s, tg.Name, "report", docfile.JSON)
		d := named("report")
		d.SetID(e.ID)
		if _, err := s.SaveVersion(ctx, tg.Name, docfile.JSON, d); err != nil {
			t.Fatal(err)
		}
		if err := s.Trash(ctx, tg.Name, e.ID); err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		if list, _ := s.List(ctx, tg.Name); len(list) != 0 {
			t.Errorf("%s: trash shows in the list: %v", tg.Name, list)
		}
		tr, err := s.Trashed(ctx, tg.Name)
		if err != nil || len(tr) != 1 || tr[0].ID != e.ID || tr[0].Name != "report" || tr[0].Trashed.IsZero() {
			t.Fatalf("%s: trashed %+v %v", tg.Name, tr, err)
		}
		// A trashed document is still there: nothing was copied or overwritten.
		if got, err := s.Load(ctx, tg.Name, e.ID); err != nil || got.Name() != "report" {
			t.Errorf("%s: %+v %v", tg.Name, got, err)
		}
		// A new document with the same name is a different document.
		other := saved(t, s, tg.Name, "report", docfile.JSON)
		if other.ID == e.ID {
			t.Fatal("same name, same id")
		}
		if err := s.Restore(ctx, tg.Name, e.ID); err != nil {
			t.Fatal(err)
		}
		list, _ := s.List(ctx, tg.Name)
		if len(list) != 2 {
			t.Fatalf("%s: restored %v", tg.Name, list)
		}
		if tr, _ = s.Trashed(ctx, tg.Name); len(tr) != 0 {
			t.Errorf("%s: still in the trash: %v", tg.Name, tr)
		}
		if vs, _ := s.Versions(ctx, tg.Name, e.ID); len(vs) != 1 {
			t.Errorf("%s: the history went with the document: %d", tg.Name, len(vs))
		}
		// Purge only what is in the trash; EmptyTrash.
		if err := s.Purge(ctx, tg.Name, e.ID); err == nil {
			t.Errorf("%s: purged a document that was not in the trash", tg.Name)
		}
		if err := s.Trash(ctx, tg.Name, e.ID, other.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.Purge(ctx, tg.Name, e.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Load(ctx, tg.Name, e.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: purged but %v", tg.Name, err)
		}
		if vs, _ := s.Versions(ctx, tg.Name, e.ID); len(vs) != 0 {
			t.Errorf("%s: purge keeps history: %d", tg.Name, len(vs))
		}
		if n, err := s.EmptyTrash(ctx, tg.Name); err != nil || n != 1 {
			t.Errorf("%s: emptied %d %v", tg.Name, n, err)
		}
		if err := s.Trash(ctx, tg.Name, docfile.NewID()); !errors.Is(err, docstore.ErrNotFound) {
			t.Errorf("%s: trashing what is not there: %v", tg.Name, err)
		}
	}
}

func TestDuplicate(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		e := saved(t, s, tg.Name, "plan", docfile.ProtoBinary)
		a, err := s.Duplicate(ctx, tg.Name, e.ID, "")
		if err != nil || a.Name != "Copy of plan" || a.ID == e.ID || a.Format != docfile.ProtoBinary {
			t.Fatalf("%s: %+v %v", tg.Name, a, err)
		}
		// Names need not be unique: a second copy has the same name and its own ID.
		b, _ := s.Duplicate(ctx, tg.Name, e.ID, "")
		if b.Name != "Copy of plan" || b.ID == a.ID {
			t.Errorf("%s: %+v", tg.Name, b)
		}
		c, _ := s.Duplicate(ctx, tg.Name, e.ID, "Plan B")
		got, err := s.Load(ctx, tg.Name, c.ID)
		if err != nil || c.Name != "Plan B" || got.Name() != "Plan B" || got.ID() != c.ID || len(got.Content) != 1 {
			t.Errorf("%s: %+v %+v %v", tg.Name, c, got, err)
		}
		if _, err := s.Duplicate(ctx, tg.Name, e.ID, "bad\x00name"); !errors.Is(err, ErrName) {
			t.Errorf("%s: %v", tg.Name, err)
		}
		if _, err := s.Duplicate(ctx, tg.Name, docfile.NewID(), ""); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: %v", tg.Name, err)
		}
		if list, _ := s.List(ctx, tg.Name); len(list) != 4 {
			t.Errorf("%s: %d documents", tg.Name, len(list))
		}
	}
}

func TestRenameAndTags(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		a := saved(t, s, tg.Name, "a", docfile.JSON)
		b := saved(t, s, tg.Name, "b", docfile.ProtoBinary)
		if err := s.SetTags(ctx, tg.Name, a.ID, []string{" rpg ", "draft", "RPG", "a,b", ""}); err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		if err := s.SetTags(ctx, tg.Name, b.ID, []string{"draft"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Rename(ctx, tg.Name, b.ID, "bee"); err != nil {
			t.Fatal(err)
		}
		if err := s.Rename(ctx, tg.Name, b.ID, " "); !errors.Is(err, ErrName) {
			t.Errorf("%s: %v", tg.Name, err)
		}
		list, err := s.List(ctx, tg.Name)
		if err != nil || len(list) != 2 {
			t.Fatalf("%s: %v %v", tg.Name, list, err)
		}
		byName := map[string][]string{}
		for _, e := range list {
			byName[e.Name] = e.Tags
		}
		if !reflect.DeepEqual(byName["a"], []string{"rpg", "draft", "a b"}) || !reflect.DeepEqual(byName["bee"], []string{"draft"}) {
			t.Errorf("%s: tags %v", tg.Name, byName)
		}
		got, _ := s.Load(ctx, tg.Name, a.ID)
		if got.Title != "Notes" || len(got.Content) != 1 || !got.HasTag("RPG") {
			t.Errorf("%s: the document changed: %+v", tg.Name, got)
		}
		// Saving the document again, with no tags of its own, keeps the catalog's.
		d := named("a")
		d.SetID(a.ID)
		e, _ := s.Save(ctx, tg.Name, docfile.JSON, d)
		if !reflect.DeepEqual(e.Tags, []string{"rpg", "draft", "a b"}) {
			t.Errorf("%s: saving dropped the tags: %v", tg.Name, e.Tags)
		}
		_ = s.SetTags(ctx, tg.Name, a.ID, nil)
		if got, _ = s.Load(ctx, tg.Name, a.ID); len(got.Tags()) != 0 {
			t.Errorf("%s: %v", tg.Name, got.Tags())
		}
	}
	d := docfile.Document{}
	d.SetTags([]string{"x"})
	d.SetTags(nil)
	if _, ok := d.Get(docfile.TagsKey); ok || strings.Contains(d.String(), "x") {
		t.Error("clearing the tags removes the entry")
	}
}

func TestPurgeChangesNothingWhenOneIDIsWrong(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	b := saved(t, s, "m", "b", docfile.JSON)
	c := saved(t, s, "m", "c", docfile.JSON)
	if err := s.Trash(ctx, "m", b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx, "m", b.ID, c.ID); err == nil {
		t.Fatal("c is not in the trash")
	}
	if got, err := s.Load(ctx, "m", b.ID); err != nil || got.Name() != "b" {
		t.Errorf("a failed purge left b broken: %+v %v", got, err)
	}
	if tr, _ := s.Trashed(ctx, "m"); len(tr) != 1 {
		t.Errorf("trash %v", tr)
	}
}

func TestSavingDoesNotBringBackRemovedTags(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	e := saved(t, s, "m", "a", docfile.JSON)
	_ = s.SetTags(ctx, "m", e.ID, []string{"t"})
	stale, _ := s.Load(ctx, "m", e.ID) // an editor holding the document with its tag
	_ = s.SetTags(ctx, "m", e.ID, nil) // the tag is removed in the list
	again, err := s.Save(ctx, "m", docfile.JSON, stale)
	if err != nil || len(again.Tags) != 0 {
		t.Fatalf("%+v %v", again, err)
	}
	// And a trashed document stays trashed when it is saved.
	_ = s.Trash(ctx, "m", e.ID)
	if _, err := s.Save(ctx, "m", docfile.JSON, stale); err != nil {
		t.Fatal(err)
	}
	if tr, _ := s.Trashed(ctx, "m"); len(tr) != 1 {
		t.Errorf("trash %v", tr)
	}
}

func revision(title string, saved int64) docfile.Revision {
	d := named("notes")
	d.Title = title
	return docfile.Revision{Saved: saved, Doc: &d}
}

func versionTitles(t *testing.T, s *Saver, target, id string) []string {
	t.Helper()
	vs, err := s.Versions(context.Background(), target, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range vs { // newest first
		d, err := s.LoadVersion(context.Background(), target, v.Key)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, d.Title)
	}
	return out
}

func TestImportKeepsTheNewestMaxVersions(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}, MaxVersions: 3}
		d := named("notes")
		d.SetID(docfile.NewID())
		for i := range 5 { // oldest first
			d.History = append(d.History, revision("r"+string(rune('0'+i)), int64(1000+i)*1e9))
		}
		e, err := s.Save(ctx, tg.Name, docfile.JSON, d)
		if err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		if got := strings.Join(versionTitles(t, s, tg.Name, e.ID), " "); got != "r4 r3 r2" {
			t.Errorf("%s: versions %q, want the newest three", tg.Name, got)
		}
		loaded, err := s.Load(ctx, tg.Name, e.ID)
		if err != nil || len(loaded.History) != 0 {
			t.Errorf("%s: the stored document holds no history: %v %v", tg.Name, len(loaded.History), err)
		}
	}
}

func TestImportIntoAnExistingIDAddsToTheVersions(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}, MaxVersions: 10}
		e := saved(t, s, tg.Name, "notes", docfile.JSON)
		old := named("notes")
		old.SetID(e.ID)
		old.Title = "old"
		if _, err := s.SaveVersion(ctx, tg.Name, docfile.JSON, old); err != nil {
			t.Fatal(err)
		}
		in := named("notes")
		in.SetID(e.ID)
		in.Title = "imported"
		in.History = []docfile.Revision{revision("h1", 1e18), revision("h2", 1.5e18)}
		if _, err := s.Save(ctx, tg.Name, docfile.JSON, in); err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		cur, err := s.Load(ctx, tg.Name, e.ID)
		if err != nil || cur.Title != "imported" || len(cur.History) != 0 {
			t.Fatalf("%s: current %q %v", tg.Name, cur.Title, err)
		}
		// The version it had stays and the imported ones are added; the one saved just now
		// is the newest, the imported ones carry their own (older) times.
		if got := strings.Join(versionTitles(t, s, tg.Name, e.ID), " "); got != "old h2 h1" {
			t.Errorf("%s: versions %q", tg.Name, got)
		}
	}
}

func TestImportSkipsARevisionWithNoDocument(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		d := named("notes")
		d.SetID(docfile.NewID())
		d.History = []docfile.Revision{revision("keep1", 1e18), {Saved: 1.5e18}, revision("keep2", 2e18)}
		e, err := s.Save(ctx, tg.Name, docfile.JSON, d)
		if err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		if got := strings.Join(versionTitles(t, s, tg.Name, e.ID), " "); got != "keep2 keep1" {
			t.Errorf("%s: versions %q", tg.Name, got)
		}
	}
}
