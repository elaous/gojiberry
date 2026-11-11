package saver

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/fonts"
)

func targets(t *testing.T) []Target {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	sq, err := Build(ctx, "sqlite", "SQLite", docstore.Config{Backend: "sqlite", BackendConfig: []byte(`{"path":"` + filepath.Join(dir, "d.db") + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := Build(ctx, "files", "Files", docstore.Config{Backend: "localfs", BackendConfig: []byte(`{"dir":"` + filepath.Join(dir, "fs") + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(docstore.NewServer(docstore.NewMemoryAdapter(), "memory").Handler())
	t.Cleanup(ts.Close)
	return []Target{
		sq, fs,
		{Name: "memory", Label: "Memory", Adapter: docstore.NewMemoryAdapter()},
		{Name: "server", Label: "Server", Adapter: docstore.NewHTTPAdapter(ts.URL, nil)},
	}
}

func doc() docfile.Document {
	return docfile.FromFlow("Notes", &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "hello", Format: canvas.Format{FontWeight: 700}}}})
}

func named(name string) docfile.Document {
	d := doc()
	d.SetName(name)
	return d
}

func TestSaveLoadListDeleteEveryTargetAndFormat(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: targets(t)}
	for _, tg := range s.Targets {
		for _, c := range docfile.Formats() {
			e, err := s.Save(ctx, tg.Name, c, named("notes"))
			if err != nil || !docfile.ValidID(e.ID) || !strings.HasPrefix(e.Key, e.ID+"~") || !strings.HasSuffix(e.Key, c.Ext()) || e.Name != "notes" || e.Format != c {
				t.Fatalf("%s/%s: %+v %v", tg.Name, c.Name(), e, err)
			}
			got, err := s.Load(ctx, tg.Name, e.ID)
			if err != nil || got.Title != "Notes" || got.ID() != e.ID || got.Name() != "notes" || got.Updated == 0 || len(got.Content) != 1 {
				t.Fatalf("%s/%s: %+v %v", tg.Name, c.Name(), got, err)
			}
		}
		list, err := s.List(ctx, tg.Name)
		if err != nil || len(list) != len(docfile.Formats()) {
			t.Fatalf("%s: %v %v", tg.Name, list, err)
		}
		ids := map[string]bool{}
		for _, e := range list {
			ids[e.ID] = true
			if e.Name != "notes" {
				t.Errorf("%s: %+v", tg.Name, e)
			}
		}
		if len(ids) != len(docfile.Formats()) {
			t.Fatalf("%s: documents with the same name share an ID: %v", tg.Name, list)
		}
		if err := s.Delete(ctx, tg.Name, list[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Load(ctx, tg.Name, list[0].ID); !errors.Is(err, docstore.ErrNotFound) {
			t.Fatalf("%s: deleted but %v", tg.Name, err)
		}
		if list, _ = s.List(ctx, tg.Name); len(list) != len(docfile.Formats())-1 {
			t.Fatalf("%s: %v", tg.Name, list)
		}
	}
}

func TestSavingAgainReplacesTheSameDocumentEvenInAnotherFormat(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, err := s.Save(ctx, "m", docfile.JSON, named("plan"))
	if err != nil {
		t.Fatal(err)
	}
	d := named("plan renamed")
	d.SetID(e.ID)
	e2, err := s.Save(ctx, "m", docfile.ProtoBinary, d)
	if err != nil || e2.ID != e.ID || e2.Name != "plan renamed" || !strings.HasPrefix(e2.Key, e.ID+"~") || !strings.HasSuffix(e2.Key, ".pb") {
		t.Fatalf("%+v %v", e2, err)
	}
	if _, err := mem.Load(ctx, e.Key); err != nil {
		t.Errorf("the old format's file is the previous copy: %v", err)
	}
	d3 := named("plan third")
	d3.SetID(e.ID)
	if _, err := s.Save(ctx, "m", docfile.ProtoBinary, d3); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Load(ctx, e.Key); !errors.Is(err, docstore.ErrNotFound) {
		t.Error("two saves later the old format's file should be gone")
	}
	if list, _ := s.List(ctx, "m"); len(list) != 1 {
		t.Fatalf("%v", list)
	}
}

func TestListSkipsFontsAndForeignKeysAndIsNewestFirst(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	_ = mem.Save(ctx, FontPrefix+"Lato/400-n.json", []byte("x")) // looks like a doc, is a font
	_ = mem.Save(ctx, "readme.txt", []byte("x"))
	if _, err := s.Save(ctx, "m", docfile.JSON, named("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "m", docfile.ProtoBinary, named("b")); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "m")
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	if list[0].Updated.Before(list[1].Updated) {
		t.Fatal("newest first")
	}
	if list[0].Name == "" || list[0].Format == nil {
		t.Fatal(list[0])
	}
}

func TestCatalogIsRebuiltFromTheDocuments(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	a, _ := s.Save(ctx, "m", docfile.JSON, named("alpha"))
	b, _ := s.Save(ctx, "m", docfile.ProtoBinary, named("beta"))
	if err := s.SetTags(ctx, "m", a.ID, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "m", a.ID, "alpha renamed"); err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ctx, "m", b.ID); err != nil {
		t.Fatal(err)
	}
	data, _ := docfile.JSON.Marshal(doc())
	_ = mem.Save(ctx, "legacy.json", data)
	if err := mem.Delete(ctx, CatalogKey); err != nil {
		t.Fatal(err)
	}
	live, trashed, err := s.Lists(ctx, "m") // reading a missing catalog builds it
	if err != nil || len(live) != 2 || len(trashed) != 1 {
		t.Fatalf("%v %v %v", live, trashed, err)
	}
	byName := map[string]Entry{}
	for _, e := range live {
		byName[e.Name] = e
	}
	if byName["alpha renamed"].ID != a.ID || !reflect.DeepEqual(byName["alpha renamed"].Tags, []string{"x"}) || trashed[0].ID != b.ID {
		t.Errorf("a rebuilt catalog lost an edit: %+v %+v", byName, trashed)
	}
	leg := byName["legacy"]
	if !docfile.ValidID(leg.ID) || leg.Key != leg.ID+".json" {
		t.Fatalf("legacy document: %+v", leg)
	}
	if _, err := mem.Load(ctx, "legacy.json"); !errors.Is(err, docstore.ErrNotFound) {
		t.Error("the old key should be gone")
	}
	got, err := s.Load(ctx, "m", leg.ID)
	if err != nil || got.Title != "Notes" {
		t.Errorf("%+v %v", got, err)
	}
	if _, err := s.Save(ctx, "m", docfile.JSON, got); err != nil {
		t.Errorf("saving an adopted document: %v", err)
	}
	if _, err := s.SaveVersion(ctx, "m", docfile.JSON, got); err != nil {
		t.Errorf("versioning an adopted document: %v", err)
	}
	_ = mem.Save(ctx, "twin.json", data)
	pb, _ := docfile.ProtoBinary.Marshal(doc())
	_ = mem.Save(ctx, "twin.pb", pb)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx, "m"); len(list) != 4 {
		t.Errorf("same-stem legacy files collided: %v", list)
	}
	_ = mem.Save(ctx, CatalogKey, []byte("{not json"))
	if list, _ := s.List(ctx, "m"); len(list) != 4 {
		t.Fatalf("after damage: %v", list)
	}
}

func TestFilesWithAnUnknownExtensionAreNotDocuments(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	d := doc()
	d.SetID(docfile.NewID())
	d.SetName("weird")
	data, _ := docfile.ProtoText.Marshal(d)
	_ = mem.Save(ctx, d.ID()+".txt", data) // an extension no codec owns: not a document
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	if _, err := s.Load(ctx, "m", d.ID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a file with an unknown extension is not a saved document: %v", err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}, Codecs: []docfile.Codec{docfile.JSON}}
	for _, bad := range []string{"", "   ", "a\x00b", "tab\there", strings.Repeat("x", MaxNameRunes+1)} {
		if _, err := s.Save(ctx, "m", docfile.JSON, named(bad)); !errors.Is(err, ErrName) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	for _, ok := range []string{"a/b", "..", "acl:x", `a\b`, "plan", "plan"} {
		if _, err := s.Save(ctx, "m", docfile.JSON, named(ok)); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	d := named("x")
	d.SetID("../etc/passwd")
	if _, err := s.Save(ctx, "m", docfile.JSON, d); !errors.Is(err, ErrID) {
		t.Errorf("a bad id: %v", err)
	}
	if _, err := s.Save(ctx, "nope", docfile.JSON, named("a")); err == nil {
		t.Error("unknown target")
	}
	if _, err := s.Load(ctx, "nope", "a"); err == nil {
		t.Error("unknown target")
	}
	if _, err := s.List(ctx, "nope"); err == nil {
		t.Error("unknown target")
	}
	if err := s.Delete(ctx, "nope", "a"); err == nil {
		t.Error("unknown target")
	}
	for _, id := range []string{"acl:doc", "collab:doc", CatalogKey, "missing"} {
		if _, err := s.Load(ctx, "m", id); !errors.Is(err, ErrNotFound) {
			t.Errorf("load %q: %v", id, err)
		}
		if err := s.Delete(ctx, "m", id); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete %q: %v", id, err)
		}
	}
	if _, err := s.Codec("proto"); err == nil {
		t.Error("restricted codecs")
	}
	if c, err := s.Codec("json"); err != nil || c != docfile.JSON {
		t.Error(err)
	}
	if _, err := Build(ctx, "x", "X", docstore.Config{Backend: "nope"}); err == nil {
		t.Error("unknown backend")
	}
}

func TestFontLibraryOverEveryTarget(t *testing.T) {
	ctx := context.Background()
	for _, tg := range targets(t) {
		var got []fonts.Ready
		lib := &fonts.Library{Blobs: Blobs(tg.Adapter),
			Fetch:   func(context.Context, string) ([]byte, error) { return []byte("FONTDATA"), nil },
			OnReady: func(r fonts.Ready) { got = append(got, r) }}
		lib.Add(fonts.Font{Family: "Lato", Variants: []fonts.Variant{{Weight: 400, URL: "https://f/l.woff2"}}})
		if _, err := lib.Use(ctx, "Lato", 400, false); err != nil || len(got) != 1 || !got[0].Downloaded {
			t.Fatalf("%s: %v %+v", tg.Name, err, got)
		}
		got = nil
		lib2 := &fonts.Library{Blobs: Blobs(tg.Adapter),
			Fetch:   func(context.Context, string) ([]byte, error) { t.Fatalf("%s: fetched again", tg.Name); return nil, nil },
			OnReady: func(r fonts.Ready) { got = append(got, r) }}
		lib2.Add(fonts.Font{Family: "Lato", Variants: []fonts.Variant{{Weight: 400, URL: "https://f/l.woff2"}}})
		if _, err := lib2.Use(ctx, "Lato", 400, false); err != nil || len(got) != 1 || got[0].Downloaded || string(got[0].Data) != "FONTDATA" {
			t.Fatalf("%s: %v %+v", tg.Name, err, got)
		}
		s := &Saver{Targets: []Target{tg}}
		if list, _ := s.List(ctx, tg.Name); len(list) != 0 {
			t.Fatalf("%s lists %v", tg.Name, list)
		}
	}
}

type brokenAdapter struct{ docstore.Adapter }

func (brokenAdapter) Load(context.Context, string) (docstore.Document, error) {
	return docstore.Document{}, errors.New("boom")
}

func TestBlobsError(t *testing.T) {
	if _, _, err := Blobs(brokenAdapter{docstore.NewMemoryAdapter()}).Get(context.Background(), "k"); err == nil {
		t.Fatal("a store error is not 'not found'")
	}
}

func TestAddRemoveTargetList(t *testing.T) {
	s := &Saver{Targets: []Target{{Name: "a", Label: "A", Adapter: docstore.NewMemoryAdapter()}}}
	s.AddTarget(Target{Name: "b", Label: "B", Adapter: docstore.NewMemoryAdapter()})
	if got := names(s.TargetList()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("after AddTarget: %v", got)
	}
	s.AddTarget(Target{Name: "a", Label: "A2", Adapter: docstore.NewMemoryAdapter()}) // replaces
	if got := names(s.TargetList()); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("a target of the same name is replaced, not added: %v", got)
	}
	if tg, err := s.Target("a"); err != nil || tg.Label != "A2" {
		t.Fatalf("replaced target: %+v %v", tg, err)
	}
	snap := s.TargetList()
	s.RemoveTarget("a")
	s.RemoveTarget("nope") // nothing happens
	if got := names(s.TargetList()); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("after RemoveTarget: %v", got)
	}
	if len(snap) != 2 || snap[0].Name != "a" {
		t.Fatalf("TargetList is a copy that later changes do not touch: %v", names(snap))
	}
	if _, err := s.Target("a"); err == nil {
		t.Fatal("a removed target is still found")
	}
}

func names(ts []Target) []string {
	var n []string
	for _, t := range ts {
		n = append(n, t.Name)
	}
	return n
}

func TestTargetsConcurrently(t *testing.T) {
	s := &Saver{Targets: []Target{{Name: "base", Adapter: docstore.NewMemoryAdapter()}}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("t%d", i%3)
			for j := 0; j < 200; j++ {
				s.AddTarget(Target{Name: name, Adapter: docstore.NewMemoryAdapter()})
				_ = s.TargetList()
				_, _ = s.Target("base")
				s.RemoveTarget(name)
			}
		}(i)
	}
	wg.Wait()
	if got := names(s.TargetList()); !reflect.DeepEqual(got, []string{"base"}) {
		t.Fatalf("targets left: %v", got)
	}
}
