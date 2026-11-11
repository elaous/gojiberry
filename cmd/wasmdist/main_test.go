package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func site(t *testing.T) (dir string, wasm []byte) {
	t.Helper()
	dir = t.TempDir()
	wasm = []byte(strings.Repeat("wasm bytes ", 3000))
	for name, data := range map[string][]byte{
		"main.wasm":     wasm,
		"index.html":    []byte("<!doctype html>" + strings.Repeat("<p>app</p>", 200)),
		"wasm_exec.js":  []byte(strings.Repeat("// glue\n", 300)),
		"favicon.ico.x": []byte("tiny"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, wasm
}

func TestRunWritesGzipSiblingsAndReportsThem(t *testing.T) {
	dir, wasm := site(t)
	var out bytes.Buffer
	if err := run([]string{"-brotli", "off", dir}, &out); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"main.wasm", "index.html", "wasm_exec.js"} {
		if !strings.Contains(out.String(), n) {
			t.Errorf("the report does not mention %s:\n%s", n, out.String())
		}
		if _, err := os.Stat(filepath.Join(dir, n+".gz")); err != nil {
			t.Errorf("no %s.gz: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "favicon.ico.x.gz")); err == nil {
		t.Error("a tiny file of an unknown type was compressed")
	}
	if _, err := os.Stat(filepath.Join(dir, "main.wasm.br")); err == nil {
		t.Error("-brotli off wrote a .br")
	}
	f, err := os.Open(filepath.Join(dir, "main.wasm.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if plain, _ := io.ReadAll(zr); !bytes.Equal(plain, wasm) {
		t.Error("main.wasm.gz is not main.wasm")
	}
}

func TestRunNeedsOneDirectory(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no argument: %v", err)
	}
	if err := run([]string{filepath.Join(t.TempDir(), "missing")}, &out); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("missing directory: %v", err)
	}
	if err := run([]string{"-brotli", filepath.Join(t.TempDir(), "no-such-tool"), t.TempDir()}, &out); err != nil {
		t.Errorf("an empty directory needs no tool: %v", err)
	}
}

func TestServedDirectoryAfterCompressing(t *testing.T) {
	dir, _ := site(t)
	if err := run([]string{"-brotli", "off", dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	h := handlerFor(dir)
	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := get("/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	onDisk, _ := os.ReadFile(filepath.Join(dir, "main.wasm.gz"))
	if rec.Header().Get("Content-Type") != "application/wasm" || rec.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(rec.Body.Bytes(), onDisk) {
		t.Fatalf("main.wasm: %v", rec.Header())
	}
	again := get("/main.wasm", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": rec.Header().Get("Etag")})
	if again.Code != http.StatusNotModified {
		t.Errorf("a repeat visit costs %d, want 304", again.Code)
	}
	if page := get("/d/report", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "<p>app</p>") {
		t.Errorf("an app path should be answered with index.html: %d", page.Code)
	}
	if get("/missing.js", nil).Code != http.StatusNotFound {
		t.Error("a missing script must be a 404, not the page")
	}
}
