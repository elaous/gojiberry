package wasmserve

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

const fakeBrotli = "not really brotli, but opaque bytes to the handler"

func precompressedSite(t *testing.T) (fstest.MapFS, []byte, []byte) {
	t.Helper()
	plain := []byte(strings.Repeat("wasm bytes ", 500))
	gz, err := gzipBytes(plain)
	if err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(2000, 0)
	return fstest.MapFS{
		"main.wasm":    {Data: plain, ModTime: mt},
		"main.wasm.gz": {Data: gz, ModTime: mt},
		"main.wasm.br": {Data: []byte(fakeBrotli), ModTime: mt},
	}, plain, gz
}

func TestPrecompressedNegotiationHeaders(t *testing.T) {
	fsys, plain, gz := precompressedSite(t)
	h := Handler(fsys, Options{})
	cases := []struct {
		accept, enc string
		body        int
	}{
		{"gzip", "gzip", len(gz)},
		{"gzip;q=0.5, br", "br", len(fakeBrotli)},
		{"br;q=0, gzip", "gzip", len(gz)},
		{"identity", "", len(plain)},
		{"", "", len(plain)},
	}
	byTag := map[string]string{}
	for _, c := range cases {
		rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": c.accept})
		if rec.Code != 200 || rec.Header().Get("Content-Encoding") != c.enc || rec.Body.Len() != c.body {
			t.Fatalf("accept %q: %d enc %q body %d", c.accept, rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.Len())
		}
		if rec.Header().Get("Content-Type") != "application/wasm" || rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("accept %q: headers %v", c.accept, rec.Header())
		}
		if rec.Header().Get("Content-Length") != strconv.Itoa(c.body) || rec.Header().Get("Etag") == "" {
			t.Fatalf("accept %q: length or etag wrong %v", c.accept, rec.Header())
		}
		tag := rec.Header().Get("Etag")
		if prev, ok := byTag[tag]; ok && prev != c.enc {
			t.Fatalf("encodings %q and %q share ETag %s", prev, c.enc, tag)
		}
		byTag[tag] = c.enc
	}
	if len(byTag) != 3 {
		t.Fatalf("gzip, br and identity are three representations with their own validators, got %v", byTag)
	}
	head := get(h, "HEAD", "/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	if head.Header().Get("Content-Encoding") != "gzip" || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(len(gz)) {
		t.Fatalf("HEAD: %v %d", head.Header(), head.Body.Len())
	}
	rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, plain) {
		t.Fatal("precompressed body does not decode to the file")
	}
}

func TestPrecompressedNotModifiedPerEncoding(t *testing.T) {
	fsys, _, _ := precompressedSite(t)
	h := Handler(fsys, Options{})
	gz := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip"})
	tag := gz.Header().Get("Etag")
	again := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": tag})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 || again.Header().Get("Etag") != tag {
		t.Fatalf("same encoding: %d %v", again.Code, again.Header())
	}
	other := get(h, "GET", "/main.wasm", map[string]string{"If-None-Match": tag})
	if other.Code != 200 || other.Body.Len() == 0 {
		t.Fatalf("identity form with the gzip ETag: %d", other.Code)
	}
	lm := gz.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("no Last-Modified")
	}
	byDate := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip", "If-Modified-Since": lm})
	if byDate.Code != http.StatusNotModified {
		t.Fatalf("If-Modified-Since: %d", byDate.Code)
	}
}

func TestPrecompressedRanges(t *testing.T) {
	fsys, plain, gz := precompressedSite(t)
	h := Handler(fsys, Options{})
	rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=10-29"})
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), gz[10:30]) {
		t.Fatalf("gzip range: %d %d bytes", rec.Code, rec.Body.Len())
	}
	if want := "bytes 10-29/" + strconv.Itoa(len(gz)); rec.Header().Get("Content-Range") != want || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip range headers: %v", rec.Header())
	}
	rec = get(h, "GET", "/main.wasm", map[string]string{"Range": "bytes=-5"})
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), plain[len(plain)-5:]) || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("identity suffix range: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	tag := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("Etag")
	rec = get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-3", "If-Range": tag})
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 4 {
		t.Fatalf("If-Range match: %d", rec.Code)
	}
	rec = get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-3", "If-Range": `"stale"`})
	if rec.Code != 200 || rec.Body.Len() != len(gz) {
		t.Fatalf("If-Range mismatch: %d %d", rec.Code, rec.Body.Len())
	}
	rec = get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=999999-"})
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("out of range: %d", rec.Code)
	}
}

func TestStalePrecompressedSiblingIsNotSent(t *testing.T) {
	fsys, plain, _ := precompressedSite(t)
	fsys["main.wasm"] = &fstest.MapFile{Data: append([]byte("new "), plain...), ModTime: time.Unix(3000, 0)}
	h := Handler(fsys, Options{})
	rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "br, gzip"})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("want our own gzip of the new file, got %q", rec.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.HasPrefix(got, []byte("new wasm")) {
		t.Fatal("served the stale precompressed file")
	}
}
