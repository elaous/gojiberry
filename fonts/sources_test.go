package fonts

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/elaous/gojiberry/fontserver"
)

// writeFont writes a minimal sfnt file (an offset table, a directory
// entry and a 'name' table) carrying the given family and subfamily.
func writeFont(t *testing.T, dir, rel, family, subfamily string) {
	t.Helper()
	enc := func(s string) []byte {
		var b []byte
		for _, r := range utf16.Encode([]rune(s)) {
			b = append(b, byte(r>>8), byte(r))
		}
		return b
	}
	fam, sub := enc(family), enc(subfamily)
	storage := append(append([]byte{}, fam...), sub...)
	var name bytes.Buffer
	w := func(v any) { _ = binary.Write(&name, binary.BigEndian, v) }
	w(uint16(0))
	w(uint16(2))
	w(uint16(6 + 2*12))
	for _, r := range []struct {
		id       uint16
		len, off int
	}{{1, len(fam), 0}, {2, len(sub), len(fam)}} {
		w(uint16(3))      // Windows
		w(uint16(1))      // Unicode BMP
		w(uint16(0x0409)) // en-US
		w(r.id)
		w(uint16(r.len))
		w(uint16(r.off))
	}
	name.Write(storage)

	var f bytes.Buffer
	fw := func(v any) { _ = binary.Write(&f, binary.BigEndian, v) }
	fw(uint32(0x00010000))
	fw(uint16(1))
	fw(uint16(0))
	fw(uint16(0))
	fw(uint16(0))
	f.WriteString("name")
	fw(uint32(0))
	fw(uint32(12 + 16))
	fw(uint32(name.Len()))
	f.Write(name.Bytes())

	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, f.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalRepository(t *testing.T) {
	dir := t.TempDir()
	writeFont(t, dir, "sans/Regular.ttf", "Test Sans", "Regular")
	writeFont(t, dir, "sans/Bold Italic.ttf", "Test Sans", "Bold Italic")
	writeFont(t, dir, "Serif.otf", "Test Serif", "Regular")
	if err := os.WriteFile(filepath.Join(dir, "junk.ttf"), []byte("not a font"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := Local(dir, "/fonts/")
	fonts, err := repo.Fonts(bg)
	if err != nil || repo.Name() != "Local fonts" {
		t.Fatalf("%v %v", fonts, err)
	}
	if len(fonts) != 2 || fonts[0].Family != "Test Sans" || fonts[1].Family != "Test Serif" || fonts[0].Source != "Local fonts" {
		t.Fatalf("fonts = %+v", fonts)
	}
	sans := fonts[0]
	if len(sans.Variants) != 2 || sans.Variants[0].Weight != 400 || sans.Variants[1].Weight != 700 || !sans.Variants[1].Italic {
		t.Fatalf("variants = %+v", sans.Variants)
	}
	if sans.Variants[1].URL != "/fonts/sans/Bold%20Italic.ttf" {
		t.Fatalf("URL = %q", sans.Variants[1].URL)
	}

	// New files appear after Rescan.
	writeFont(t, dir, "New.ttf", "Fresh", "Regular")
	if again, _ := repo.Fonts(bg); len(again) != 2 {
		t.Fatal("fonts are scanned once")
	}
	if err := repo.Rescan(); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.Fonts(bg); len(again) != 3 {
		t.Fatalf("after Rescan: %d families", len(again))
	}
	// A missing directory is just empty.
	empty, err := Local(filepath.Join(dir, "nope"), "").Fonts(bg)
	if err != nil || len(empty) != 0 {
		t.Fatalf("%v %v", empty, err)
	}
}

func TestServerRepositoryAgainstARealFontserver(t *testing.T) {
	dir := t.TempDir()
	writeFont(t, dir, "A Regular.ttf", "Served Sans", "Regular")
	writeFont(t, dir, "A Bold.ttf", "Served Sans", "Bold")
	srv := httptest.NewServer(fontserver.NewServer(fontserver.Config{FontsDir: dir}).Handler())
	defer srv.Close()

	repo := Server(srv.URL+"/", ServerLocal, nil)
	fonts, err := repo.Fonts(bg)
	if err != nil || repo.Name() != "Server fonts" {
		t.Fatalf("%v %v", fonts, err)
	}
	if len(fonts) != 1 || fonts[0].Family != "Served Sans" || len(fonts[0].Variants) != 2 {
		t.Fatalf("fonts = %+v", fonts)
	}
	v := fonts[0].Variants[1]
	if v.Weight != 700 || v.URL != srv.URL+"/local/files/A%20Bold.ttf" {
		t.Fatalf("variant = %+v", v)
	}
	// The URL really serves the file.
	resp, err := http.Get(v.URL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET %s: %v %v", v.URL, resp, err)
	}
	resp.Body.Close()

	// Google catalog with no API key: enabled=false, no families, no error.
	g, err := Server(srv.URL, ServerGoogle, nil).Fonts(bg)
	if err != nil || len(g) != 0 {
		t.Fatalf("google (no key) = %v, %v", g, err)
	}
}

func TestServerGoogleDecoding(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/google/fonts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"enabled":true,"families":[
			{"family":"Open Sans","category":"sans-serif","variants":["700italic","regular","italic","700","weird"],
			 "files":{"regular":"https://f/r.ttf","700":"https://f/b.ttf","italic":"https://f/i.ttf","700italic":"https://f/bi.ttf"}}]}`))
	})
	mux.HandleFunc("/local/fonts", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	repo := Server(srv.URL, ServerGoogle, srv.Client())
	fonts, err := repo.Fonts(bg)
	if err != nil || repo.Name() != "Google Fonts" || len(fonts) != 1 {
		t.Fatalf("%v %v", fonts, err)
	}
	f := fonts[0]
	if f.Category != "sans-serif" || f.Source != "Google Fonts" || len(f.Variants) != 4 {
		t.Fatalf("font = %+v (the unparseable 'weird' key is skipped)", f)
	}
	want := []Variant{
		{Weight: 400, Style: "regular", URL: "https://f/r.ttf"},
		{Weight: 400, Italic: true, Style: "italic", URL: "https://f/i.ttf"},
		{Weight: 700, Style: "700", URL: "https://f/b.ttf"},
		{Weight: 700, Italic: true, Style: "700italic", URL: "https://f/bi.ttf"},
	}
	for i, v := range f.Variants {
		if v != want[i] {
			t.Errorf("variant %d = %+v, want %+v", i, v, want[i])
		}
	}

	// HTTP errors and bad JSON are reported.
	if _, err := Server(srv.URL, ServerLocal, nil).Fonts(bg); err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("err = %v", err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{")) }))
	defer bad.Close()
	for _, k := range []ServerKind{ServerLocal, ServerGoogle} {
		if _, err := Server(bad.URL, k, nil).Fonts(bg); err == nil {
			t.Fatalf("kind %d: bad JSON must be an error", k)
		}
	}
	if _, err := Server("http://127.0.0.1:1", ServerLocal, nil).Fonts(bg); err == nil {
		t.Fatal("unreachable server")
	}
	if _, err := Server("://bad url", ServerLocal, nil).Fonts(bg); err == nil {
		t.Fatal("bad URL")
	}
}
