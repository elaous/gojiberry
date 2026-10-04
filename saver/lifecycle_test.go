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

func TestVersionsKeepHistoryNewestFirstAndPrune(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}, MaxVersions: 3}
		for i := range 5 {
			d := doc()
			d.Title = "v" + string(rune('0'+i))
			if _, err := s.SaveVersion(ctx, tg.Name, "notes", docfile.JSON, d); err != nil {
				t.Fatalf("%s: %v", tg.Name, err)
			}
		}
		vs, err := s.Versions(ctx, tg.Name, "notes")
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
		if vs, _ = s.Versions(ctx, tg.Name, "notes"); len(vs) != 2 {
			t.Errorf("%s: after a delete: %d", tg.Name, len(vs))
		}
		// Versions are not documents, and another document's are separate.
		if list, _ := s.List(ctx, tg.Name); len(list) != 0 {
			t.Errorf("%s: versions leaked into the list: %v", tg.Name, list)
		}
		if other, _ := s.Versions(ctx, tg.Name, "other"); len(other) != 0 {
			t.Errorf("%s: %v", tg.Name, other)
		}
	}
}

func TestReservedKeysAreRefused(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: targets(t)[2:3]} // memory
	if _, err := s.Save(ctx, "memory", "a", docfile.JSON, doc()); err != nil {
		t.Fatal(err)
	}
	v, _ := s.SaveVersion(ctx, "memory", "a", docfile.JSON, doc())
	if _, err := s.Load(ctx, "memory", v.Key); !errors.Is(err, ErrName) {
		t.Errorf("Load of a version key: %v", err)
	}
	if err := s.Delete(ctx, "memory", v.Key); !errors.Is(err, ErrName) {
		t.Errorf("Delete of a version key: %v", err)
	}
	if _, err := s.LoadVersion(ctx, "memory", "a.json"); !errors.Is(err, ErrName) {
		t.Errorf("LoadVersion of a document key: %v", err)
	}
	if _, err := s.Restore(ctx, "memory", "a.json"); !errors.Is(err, ErrName) {
		t.Errorf("Restore of a document key: %v", err)
	}
	if err := s.Trash(ctx, "memory", "trash/x.json"); !errors.Is(err, ErrName) {
		t.Errorf("Trash of a trash key: %v", err)
	}
	if _, err := s.SaveVersion(ctx, "memory", "a/b", docfile.JSON, doc()); !errors.Is(err, ErrName) {
		t.Errorf("a name with a slash: %v", err)
	}
	if _, err := s.LoadVersion(ctx, "memory", "versions/../a.json"); !errors.Is(err, ErrName) {
		t.Errorf("a key that climbs out: %v", err)
	}
}

func TestTrashRestorePurge(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		key, _ := s.Save(ctx, tg.Name, "report", docfile.JSON, doc())
		if _, err := s.SaveVersion(ctx, tg.Name, "report", docfile.JSON, doc()); err != nil {
			t.Fatal(err)
		}
		if err := s.Trash(ctx, tg.Name, key); err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		if _, err := s.Load(ctx, tg.Name, key); !errors.Is(err, docstore.ErrNotFound) {
			t.Fatalf("%s: a trashed document is gone from the store: %v", tg.Name, err)
		}
		if list, _ := s.List(ctx, tg.Name); len(list) != 0 {
			t.Errorf("%s: trash shows in the list: %v", tg.Name, list)
		}
		tr, err := s.Trashed(ctx, tg.Name)
		if err != nil || len(tr) != 1 || tr[0].Name != "report" || tr[0].Updated.IsZero() {
			t.Fatalf("%s: trashed %+v %v", tg.Name, tr, err)
		}
		// Save a new "report" while the old one is in the trash: restore must not overwrite it.
		_, _ = s.Save(ctx, tg.Name, "report", docfile.JSON, doc())
		restored, err := s.Restore(ctx, tg.Name, tr[0].Key)
		if err != nil || restored != "report (restored).json" {
			t.Fatalf("%s: restored as %q %v", tg.Name, restored, err)
		}
		if got, err := s.Load(ctx, tg.Name, restored); err != nil || got.Title != "Notes" {
			t.Errorf("%s: %+v %v", tg.Name, got, err)
		}
		if tr, _ = s.Trashed(ctx, tg.Name); len(tr) != 0 {
			t.Errorf("%s: still in the trash: %v", tg.Name, tr)
		}
		if vs, _ := s.Versions(ctx, tg.Name, "report"); len(vs) != 1 {
			t.Errorf("%s: the history went with the document: %d", tg.Name, len(vs))
		}
		// Purge and EmptyTrash.
		_ = s.Trash(ctx, tg.Name, restored)
		_ = s.Trash(ctx, tg.Name, "report.json")
		tr, _ = s.Trashed(ctx, tg.Name)
		if len(tr) != 2 {
			t.Fatalf("%s: %v", tg.Name, tr)
		}
		if err := s.Purge(ctx, tg.Name, tr[0].Key); err != nil {
			t.Fatal(err)
		}
		if n, err := s.EmptyTrash(ctx, tg.Name); err != nil || n != 1 {
			t.Errorf("%s: emptied %d %v", tg.Name, n, err)
		}
		if err := s.Trash(ctx, tg.Name, "nothing.json"); !errors.Is(err, docstore.ErrNotFound) {
			t.Errorf("%s: trashing what is not there: %v", tg.Name, err)
		}
	}
}

func TestDuplicate(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		key, _ := s.Save(ctx, tg.Name, "plan", docfile.ProtoBinary, doc())
		a, err := s.Duplicate(ctx, tg.Name, key, "")
		if err != nil || a != "Copy of plan.pb" {
			t.Fatalf("%s: %q %v", tg.Name, a, err)
		}
		b, _ := s.Duplicate(ctx, tg.Name, key, "")
		if b != "Copy of plan (2).pb" {
			t.Errorf("%s: a taken name gets a number: %q", tg.Name, b)
		}
		c, _ := s.Duplicate(ctx, tg.Name, key, "Plan B")
		got, err := s.Load(ctx, tg.Name, c)
		if err != nil || c != "Plan B.pb" || got.Title != "Plan B" || len(got.Content) != 1 {
			t.Errorf("%s: %q %+v %v", tg.Name, c, got, err)
		}
		if _, err := s.Duplicate(ctx, tg.Name, key, "a/b"); !errors.Is(err, ErrName) {
			t.Errorf("%s: %v", tg.Name, err)
		}
		if _, err := s.Duplicate(ctx, tg.Name, "missing.pb", ""); !errors.Is(err, docstore.ErrNotFound) {
			t.Errorf("%s: %v", tg.Name, err)
		}
	}
}

func TestTags(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		a, _ := s.Save(ctx, tg.Name, "a", docfile.JSON, doc())
		b, _ := s.Save(ctx, tg.Name, "b", docfile.ProtoBinary, doc())
		if err := s.SetTags(ctx, tg.Name, a, []string{" rpg ", "draft", "RPG", "a,b", ""}); err != nil {
			t.Fatalf("%s: %v", tg.Name, err)
		}
		if err := s.SetTags(ctx, tg.Name, b, []string{"draft"}); err != nil {
			t.Fatal(err)
		}
		list, err := s.ListTagged(ctx, tg.Name)
		if err != nil || len(list) != 2 {
			t.Fatalf("%s: %v %v", tg.Name, list, err)
		}
		byName := map[string][]string{}
		for _, e := range list {
			byName[e.Name] = e.Tags
		}
		if !reflect.DeepEqual(byName["a"], []string{"rpg", "draft", "a b"}) || !reflect.DeepEqual(byName["b"], []string{"draft"}) {
			t.Errorf("%s: tags %v", tg.Name, byName)
		}
		got, _ := s.Load(ctx, tg.Name, a)
		if got.Title != "Notes" || len(got.Content) != 1 || !got.HasTag("RPG") {
			t.Errorf("%s: the document changed: %+v", tg.Name, got)
		}
		_ = s.SetTags(ctx, tg.Name, a, nil)
		got, _ = s.Load(ctx, tg.Name, a)
		if len(got.Tags()) != 0 {
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

func TestTrashKeepsEarlierCopyWithSameName(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		s := &Saver{Targets: []Target{tg}}
		for range 2 {
			key, _ := s.Save(ctx, tg.Name, "a", docfile.JSON, doc())
			if err := s.Trash(ctx, tg.Name, key); err != nil {
				t.Fatalf("%s: %v", tg.Name, err)
			}
		}
		tr, err := s.Trashed(ctx, tg.Name)
		if err != nil || len(tr) != 2 {
			t.Fatalf("%s: the second trashed copy replaced the first: %+v %v", tg.Name, tr, err)
		}
		for _, bad := range []string{"trash/a/b.json", "trash/acl:x.json"} {
			if _, err := s.Restore(ctx, tg.Name, bad); err == nil {
				t.Errorf("%s: restored %q", tg.Name, bad)
			}
		}
	}
}
