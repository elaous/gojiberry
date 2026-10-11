package wasmserve

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func get(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func site() fstest.MapFS {
	big := strings.Repeat("wasm bytes ", 500)
	return fstest.MapFS{
		"index.html":            {Data: []byte("<h1>app</h1>"), ModTime: time.Unix(1000, 0)},
		"main.wasm":             {Data: []byte(big), ModTime: time.Unix(1000, 0)},
		"pre.js":                {Data: []byte("plain js"), ModTime: time.Unix(1000, 0)},
		"pre.js.br":             {Data: []byte("BROTLI-BYTES"), ModTime: time.Unix(1000, 0)},
		"pre.js.gz":             {Data: []byte("GZIP-BYTES"), ModTime: time.Unix(1000, 0)},
		"logo.png":              {Data: []byte("\x89PNG not really"), ModTime: time.Unix(1000, 0)},
		"sub/index.html":        {Data: []byte("sub index"), ModTime: time.Unix(1000, 0)},
		"app.3f9a1c.js":         {Data: []byte("hashed"), ModTime: time.Unix(1000, 0)},
		"style.css":             {Data: []byte("body{}"), ModTime: time.Unix(1000, 0)},
		"data.unknownextension": {Data: []byte("x"), ModTime: time.Unix(1000, 0)},
	}
}

func TestWasmTypeAndOnTheFlyGzip(t *testing.T) {
	h := Handler(site(), Options{})
	rec := get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip, deflate"})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/wasm" || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if !bytes.Equal(plain, site()["main.wasm"].Data) || rec.Body.Len() != 0 {
		t.Fatal("gzip round trip")
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("Etag") == "" {
		t.Fatalf("%v", rec.Header())
	}
	// without gzip in Accept-Encoding: the file as it is
	plainRec := get(h, "GET", "/main.wasm", nil)
	if plainRec.Header().Get("Content-Encoding") != "" || plainRec.Body.Len() != len(site()["main.wasm"].Data) {
		t.Fatalf("identity: %v %d", plainRec.Header(), plainRec.Body.Len())
	}
	if get(h, "GET", "/main.wasm", map[string]string{"Accept-Encoding": "gzip;q=0"}).Header().Get("Content-Encoding") != "" {
		t.Fatal("q=0 means no")
	}
}

func TestPrecompressedSiblingsWinAndBrotliBeatsGzip(t *testing.T) {
	h := Handler(site(), Options{})
	br := get(h, "GET", "/pre.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if br.Header().Get("Content-Encoding") != "br" || br.Body.String() != "BROTLI-BYTES" || !strings.HasPrefix(br.Header().Get("Content-Type"), "text/javascript") && !strings.HasPrefix(br.Header().Get("Content-Type"), "application/javascript") {
		t.Fatalf("%v %q", br.Header(), br.Body)
	}
	gz := get(h, "GET", "/pre.js", map[string]string{"Accept-Encoding": "gzip"})
	if gz.Header().Get("Content-Encoding") != "gzip" || gz.Body.String() != "GZIP-BYTES" {
		t.Fatalf("%v %q", gz.Header(), gz.Body)
	}
	if get(h, "GET", "/pre.js", nil).Body.String() != "plain js" {
		t.Fatal("no encoding accepted: the original")
	}
}

func TestIncompressibleAndRevalidation(t *testing.T) {
	h := Handler(site(), Options{Immutable: func(n string) bool { return strings.Contains(n, ".3f9a1c.") }})
	if get(h, "GET", "/logo.png", map[string]string{"Accept-Encoding": "gzip"}).Header().Get("Content-Encoding") != "" {
		t.Fatal("images are not recompressed")
	}
	first := get(h, "GET", "/index.html", nil)
	again := get(h, "GET", "/index.html", map[string]string{"If-None-Match": first.Header().Get("Etag")})
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("%d", again.Code)
	}
	if get(h, "GET", "/app.3f9a1c.js", nil).Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal("hashed files are immutable")
	}
	if get(h, "GET", "/data.unknownextension", nil).Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal("unknown type")
	}
}

func TestPathsFallbackAndMethods(t *testing.T) {
	h := Handler(site(), Options{Fallback: "index.html"})
	for _, p := range []string{"/", "/index.html", "/d/report", "/f/x/y"} {
		if rec := get(h, "GET", p, nil); rec.Code != 200 || rec.Body.String() != "<h1>app</h1>" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body)
		}
	}
	if rec := get(h, "GET", "/sub/", nil); rec.Body.String() != "sub index" {
		t.Errorf("a directory serves its index: %q", rec.Body)
	}
	if get(h, "GET", "/missing.js", nil).Code != 404 || get(h, "GET", "/missing.wasm", nil).Code != 404 {
		t.Error("a missing file with an extension is a 404, never the app")
	}
	if rec := get(h, "GET", "/../../etc/passwd", nil); rec.Code != 200 && rec.Code != 404 {
		t.Errorf("traversal: %d", rec.Code)
	}
	if get(h, "HEAD", "/main.wasm", nil).Code != 200 || get(h, "POST", "/", nil).Code != http.StatusMethodNotAllowed {
		t.Error("HEAD yes, POST no")
	}
	plain := Handler(site(), Options{})
	if get(plain, "GET", "/d/report", nil).Code != 404 {
		t.Error("without a fallback an unknown path is a 404")
	}
	if get(Handler(fstest.MapFS{}, Options{Fallback: "index.html"}), "GET", "/x", nil).Code != 404 {
		t.Error("a missing fallback is a 404")
	}
}

func TestChangedFileReplacesItsCachedForm(t *testing.T) {
	fsys := fstest.MapFS{"a.txt": {Data: []byte("one"), ModTime: time.Unix(1, 0)}}
	h := Handler(fsys, Options{}).(*handler)
	get(h, "GET", "/a.txt", map[string]string{"Accept-Encoding": "gzip"})
	fsys["a.txt"] = &fstest.MapFile{Data: []byte("two"), ModTime: time.Unix(2, 0)}
	get(h, "GET", "/a.txt", map[string]string{"Accept-Encoding": "gzip"})
	if len(h.cache) != 1 {
		t.Fatalf("%d cached forms, want only the newest", len(h.cache))
	}
	rec := get(h, "GET", "/a.txt", nil)
	if rec.Body.String() != "two" {
		t.Fatalf("%q", rec.Body)
	}
}
