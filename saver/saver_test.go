package saver

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
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

// named is the test document with a name for a document list.
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
			// Three documents with one name: three IDs, three files.
			e, err := s.Save(ctx, tg.Name, c, named("notes"))
			if err != nil || !docfile.ValidID(e.ID) || e.Key != e.ID+c.Ext() || e.Name != "notes" || e.Format != c {
				t.Fatalf("%s/%s: %+v %v", tg.Name, c.Name(), e, err)
			}
			got, err := s.Load(ctx, tg.Name, e.ID)
			if err != nil || got.Title != "Notes" || got.ID() != e.ID || got.Name() != "notes" || got.Updated == 0 || len(got.Content) != 1 {
				t.Fatalf("%s/%s: %+v %v", tg.Name, c.Name(), got, err)
			}
		}
		list, err := s.List(ctx, tg.Name)
		if err != nil || len(list) != 3 {
			t.Fatalf("%s: %v %v", tg.Name, list, err)
		}
		ids := map[string]bool{}
		for _, e := range list {
			ids[e.ID] = true
			if e.Name != "notes" {
				t.Errorf("%s: %+v", tg.Name, e)
			}
		}
		if len(ids) != 3 {
			t.Fatalf("%s: documents with the same name share an ID: %v", tg.Name, list)
		}
		if err := s.Delete(ctx, tg.Name, list[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Load(ctx, tg.Name, list[0].ID); !errors.Is(err, docstore.ErrNotFound) {
			t.Fatalf("%s: deleted but %v", tg.Name, err)
		}
		if list, _ = s.List(ctx, tg.Name); len(list) != 2 {
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
	if err != nil || e2.ID != e.ID || e2.Name != "plan renamed" || e2.Key != e.ID+".pb" {
		t.Fatalf("%+v %v", e2, err)
	}
	if _, err := mem.Load(ctx, e.Key); !errors.Is(err, docstore.ErrNotFound) {
		t.Error("the old format's file should be gone")
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
	// Edits made through the catalog are kept in the documents too.
	if err := s.SetTags(ctx, "m", a.ID, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, "m", a.ID, "alpha renamed"); err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ctx, "m", b.ID); err != nil {
		t.Fatal(err)
	}
	// A saved document from before IDs, under its name, gets an ID and the key that goes with it.
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
	// It can be saved back and versioned like any other.
	if _, err := s.Save(ctx, "m", docfile.JSON, got); err != nil {
		t.Errorf("saving an adopted document: %v", err)
	}
	if _, err := s.SaveVersion(ctx, "m", docfile.JSON, got); err != nil {
		t.Errorf("versioning an adopted document: %v", err)
	}
	// Two legacy files with one stem are two documents.
	_ = mem.Save(ctx, "twin.json", data)
	pb, _ := docfile.ProtoBinary.Marshal(doc())
	_ = mem.Save(ctx, "twin.pb", pb)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx, "m"); len(list) != 4 {
		t.Errorf("same-stem legacy files collided: %v", list)
	}
	// A damaged catalog is rebuilt too.
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
	// Any other name is fine: slashes, dots, the same name twice.
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
		// A new library over the same store loads, not downloads.
		got = nil
		lib2 := &fonts.Library{Blobs: Blobs(tg.Adapter),
			Fetch:   func(context.Context, string) ([]byte, error) { t.Fatalf("%s: fetched again", tg.Name); return nil, nil },
			OnReady: func(r fonts.Ready) { got = append(got, r) }}
		lib2.Add(fonts.Font{Family: "Lato", Variants: []fonts.Variant{{Weight: 400, URL: "https://f/l.woff2"}}})
		if _, err := lib2.Use(ctx, "Lato", 400, false); err != nil || len(got) != 1 || got[0].Downloaded || string(got[0].Data) != "FONTDATA" {
			t.Fatalf("%s: %v %+v", tg.Name, err, got)
		}
		// Saved documents don't list the font.
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
