package userconfig

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/tokens"
)

func TestShareTemplateBetweenTwoPeople(t *testing.T) {
	ctx := context.Background()
	m := docstore.NewMemoryAdapter()
	ana, bo := &Store{A: m, User: "ana@example.com"}, &Store{A: m, User: "bo@example.com"}
	if _, err := ana.Put(ctx, "template.report", []byte(`{"title":"Quarterly","content":[]}`), 0); err != nil {
		t.Fatal(err)
	}
	b, err := ana.Export(ctx, "template.report", "Our usual report layout - Ana")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("ana@example.com")) {
		t.Fatal("a bundle carries no identity")
	}
	info, err := Inspect(b)
	if err != nil || info.Kind != KindTemplate || info.Name != "template.report" || !strings.Contains(info.Note, "Ana") || info.Size == 0 {
		t.Fatalf("%+v %v", info, err)
	}
	if names, _ := bo.List(ctx); len(names) != 0 {
		t.Fatal("inspecting writes nothing, and bo has nothing yet")
	}
	it, err := bo.Import(ctx, b, "")
	if err != nil || it.Name != "template.report" || it.Rev != 1 || string(it.Data) != `{"title":"Quarterly","content":[]}` {
		t.Fatalf("%+v %v", it, err)
	}
	it2, err := bo.Import(ctx, b, "")
	if err != nil || it2.Name != "template.report-2" {
		t.Fatalf("%+v %v", it2, err)
	}
	it3, _ := bo.Import(ctx, b, "")
	if it3.Name != "template.report-3" {
		t.Fatalf("%q", it3.Name)
	}
	if it, err := bo.Import(ctx, b, "template.mine"); err != nil || it.Name != "template.mine" {
		t.Fatalf("%+v %v", it, err)
	}
	for _, bad := range []string{"style.mine", "template.", "template.Bad Name", "mine", "template/x"} {
		if _, err := bo.Import(ctx, b, bad); !errors.Is(err, ErrBadName) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if names, _ := ana.List(ctx); len(names) != 1 {
		t.Fatalf("%v", names)
	}
}

func TestOnlyTemplatesStylesAndThemesAreShared(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	_, _ = s.Put(ctx, "prefs", []byte(`{}`), 0)
	font, _ := EncodeFont(fakeFont("OTTO", 64), licence)
	_, _ = s.Put(ctx, "font.atkinson", font, 0)
	_, _ = s.Put(ctx, "styles", []byte(`{}`), 0) // "styles" is not the "style." kind
	for _, name := range []string{"prefs", "font.atkinson", "styles", "template"} {
		if _, err := s.Export(ctx, name, ""); !errors.Is(err, ErrNotShareable) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := s.Export(ctx, "template.missing", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("%v", err)
	}
	long := strings.Repeat("é", 800)
	_, _ = s.Put(ctx, "style.x", []byte(`{"a":1}`), 0)
	b, err := s.Export(ctx, "style.x", long)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := Inspect(b); len([]rune(info.Note)) != maxNote {
		t.Fatalf("the note is cut to %d characters: %d", maxNote, len([]rune(info.Note)))
	}
}

func TestThemesMustBeReadableToBeImported(t *testing.T) {
	ctx := context.Background()
	ana, _ := newStore()
	good, _ := json.Marshal(tokens.Default())
	if _, err := ana.Put(ctx, "tokens.calm", good, 0); err != nil {
		t.Fatal(err)
	}
	b, err := ana.Export(ctx, "tokens.calm", "")
	if err != nil {
		t.Fatal(err)
	}
	bo := &Store{A: docstore.NewMemoryAdapter(), User: "bo"}
	if _, err := bo.Import(ctx, b, ""); err != nil {
		t.Fatal(err)
	}
	low := tokens.Default()
	low.Colors[tokens.Text] = "#cccccc"
	lowJSON, _ := json.Marshal(low)
	_, _ = ana.Put(ctx, "tokens.faint", lowJSON, 0)
	fb, _ := ana.Export(ctx, "tokens.faint", "")
	if _, err := bo.Import(ctx, fb, ""); !errors.Is(err, ErrBadBundle) || !strings.Contains(err.Error(), "contrast") {
		t.Fatalf("%v", err)
	}
	_, _ = ana.Put(ctx, "tokens.junk", []byte(`[1,2]`), 0)
	jb, _ := ana.Export(ctx, "tokens.junk", "")
	if _, err := bo.Import(ctx, jb, ""); !errors.Is(err, ErrBadBundle) {
		t.Fatalf("%v", err)
	}
}

func TestDamagedAndForeignBundlesAreRefused(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	_, _ = s.Put(ctx, "template.ok", []byte(`{"a":1}`), 0)
	good, _ := s.Export(ctx, "template.ok", "")

	edit := func(f func(m map[string]any)) []byte {
		var m map[string]any
		_ = json.Unmarshal(good, &m)
		f(m)
		out, _ := json.Marshal(m)
		return out
	}
	cases := map[string][]byte{
		"not json":      []byte("<html>"),
		"wrong tag":     edit(func(m map[string]any) { m["tag"] = "other" }),
		"newer version": edit(func(m map[string]any) { m["v"] = 2 }),
		"kind mismatch": edit(func(m map[string]any) { m["kind"] = "style" }),
		"unknown kind":  edit(func(m map[string]any) { m["kind"] = "font"; m["name"] = "font.x" }),
		"bad name":      edit(func(m map[string]any) { m["name"] = "template.../../x" }),
		"changed data":  edit(func(m map[string]any) { m["data"] = "eyJhIjoyfQ==" }), // {"a":2}
		"data not json": edit(func(m map[string]any) {
			m["data"] = "bm90IGpzb24="
			m["sha256"] = "7db0a1fb28cbc1b0a4bd0e8ff0bd6a29f9ae0e0ad4cd4f3a6e2d8f7e3b9a0c11"
		}),
		"too big": append([]byte(`{"tag":"gojiberry-share","pad":"`), append(bytes.Repeat([]byte("x"), maxBundle), []byte(`"}`)...)...),
	}
	for name, b := range cases {
		if _, err := Inspect(b); !errors.Is(err, ErrBadBundle) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := s.Import(ctx, b, ""); !errors.Is(err, ErrBadBundle) {
			t.Errorf("%s (import): %v", name, err)
		}
	}
	for _, b := range cases {
		if _, err := Inspect(b); err != nil && strings.Contains(err.Error(), "<html>") {
			t.Fatal("an error must not repeat the file")
		}
	}
	small := &Store{A: docstore.NewMemoryAdapter(), User: "u", MaxItem: 3}
	if _, err := small.Import(ctx, good, ""); !errors.Is(err, ErrQuota) {
		t.Fatalf("%v", err)
	}
}

func TestImportGivesUpAfterManyCopies(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore()
	_, _ = s.Put(ctx, "style.x", []byte(`{}`), 0)
	b, _ := s.Export(ctx, "style.x", "")
	var err error
	for range 21 {
		_, err = s.Import(ctx, b, "")
	}
	if err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("%v", err)
	}
}

func TestBundleAndOpenBundleRoundTripWithoutAStore(t *testing.T) {
	data := []byte(`{"title":"Quarterly","content":[]}`)
	b, err := Bundle("template.report", data, "from Ana")
	if err != nil {
		t.Fatal(err)
	}
	info, got, err := OpenBundle(b)
	if err != nil || info.Name != "template.report" || info.Kind != KindTemplate || info.Note != "from Ana" || string(got) != string(data) {
		t.Fatalf("OpenBundle = %+v %q %v", info, got, err)
	}
	if _, err := Bundle("font.x", data, ""); err != ErrNotShareable {
		t.Errorf("a font is shared: %v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	raw["data"] = []byte(`{"title":"Changed","content":[]}`)
	changed, _ := json.Marshal(raw)
	if _, _, err := OpenBundle(changed); err == nil || !errors.Is(err, ErrBadBundle) {
		t.Errorf("a changed bundle was read: %v", err)
	}
}
