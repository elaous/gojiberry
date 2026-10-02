package saver

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
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

func TestSaveLoadListDeleteEveryTargetAndFormat(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: targets(t)}
	for _, tg := range s.Targets {
		for _, c := range docfile.Formats() {
			key, err := s.Save(ctx, tg.Name, "notes", c, doc())
			if err != nil || key != "notes"+c.Ext() {
				t.Fatalf("%s/%s: %q %v", tg.Name, c.Name(), key, err)
			}
			got, err := s.Load(ctx, tg.Name, key)
			if err != nil || got.Title != "Notes" || got.Updated == 0 || len(got.Content) != 1 {
				t.Fatalf("%s/%s: %+v %v", tg.Name, c.Name(), got, err)
			}
		}
		list, err := s.List(ctx, tg.Name)
		if err != nil || len(list) != 3 {
			t.Fatalf("%s: %v %v", tg.Name, list, err)
		}
		if err := s.Delete(ctx, tg.Name, list[0].Key); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Load(ctx, tg.Name, list[0].Key); !errors.Is(err, docstore.ErrNotFound) {
			t.Fatalf("%s: deleted but %v", tg.Name, err)
		}
	}
}

func TestListSkipsFontsAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	_ = mem.Save(ctx, FontPrefix+"Lato/400-n.json", []byte("x")) // looks like a doc, is a font
	_ = mem.Save(ctx, "readme.txt", []byte("x"))
	if _, err := s.Save(ctx, "m", "a", docfile.JSON, doc()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "m", "b", docfile.ProtoBinary, doc()); err != nil {
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

func TestLoadDetectsFormatWithoutExtension(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	data, _ := docfile.ProtoText.Marshal(doc())
	_ = mem.Save(ctx, "weird", data)
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	got, err := s.Load(ctx, "m", "weird")
	if err != nil || got.Title != "Notes" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}, Codecs: []docfile.Codec{docfile.JSON}}
	for _, bad := range []string{"", "a/b", "..", "acl:x", "collab:x", `a\b`} {
		if _, err := s.Save(ctx, "m", bad, docfile.JSON, doc()); !errors.Is(err, ErrName) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	for _, key := range []string{"acl:doc", "collab:doc"} {
		if _, err := s.Load(ctx, "m", key); !errors.Is(err, ErrName) {
			t.Errorf("load %q: %v", key, err)
		}
		if err := s.Delete(ctx, "m", key); !errors.Is(err, ErrName) {
			t.Errorf("delete %q: %v", key, err)
		}
	}
	if _, err := s.Save(ctx, "nope", "a", docfile.JSON, doc()); err == nil {
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
	if _, err := s.Load(ctx, "m", "missing.json"); !errors.Is(err, docstore.ErrNotFound) {
		t.Error(err)
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
