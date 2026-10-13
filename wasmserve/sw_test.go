package wasmserve

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestServiceWorkerIsVersionedByTheContentsOfTheShell(t *testing.T) {
	site := func(wasm string) fstest.MapFS {
		return fstest.MapFS{"index.html": {Data: []byte("<html>")}, "main.wasm": {Data: []byte(wasm)}, "wasm_exec.js": {Data: []byte("js")}, "other.txt": {Data: []byte("not in the shell")}}
	}
	a, err := ServiceWorker(site("one"), nil)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := ServiceWorker(site("one"), nil)
	changed, _ := ServiceWorker(site("two"), nil)
	if string(a) != string(same) {
		t.Error("the same files give the same script")
	}
	if string(a) == string(changed) {
		t.Error("a new program is a new script, so the browser installs it")
	}
	s := string(a)
	for _, want := range []string{`const SHELL = ["index.html","wasm_exec.js","main.wasm"]`, `const CACHE = PREFIX + VERSION`, `cache: "reload"`, `res.arrayBuffer()`, `k.startsWith(PREFIX) && k !== CACHE`} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %s", want)
		}
	}
	if strings.Contains(s, "other.txt") || strings.Contains(s, "__") {
		t.Error("only the shell is listed and every placeholder is filled in")
	}
	// A file for later is listed apart, and is part of the version.
	lazy := fstest.MapFS{"index.html": {Data: []byte("x")}, "files.wasm": {Data: []byte("a")}, "files-worker.js": {Data: []byte("w")}}
	l1, _ := ServiceWorker(lazy, nil)
	lazy["files.wasm"] = &fstest.MapFile{Data: []byte("b")}
	l2, _ := ServiceWorker(lazy, nil)
	if !strings.Contains(string(l1), `const LAZY = ["files-worker.js","files.wasm"]`) || strings.Contains(string(l1), `const SHELL = ["index.html","files`) || string(l1) == string(l2) {
		t.Errorf("lazy files: %s", l1)
	}
	// A missing file is left out; no file at all is an error.
	only, err := ServiceWorker(fstest.MapFS{"index.html": {Data: []byte("x")}}, nil)
	if err != nil || !strings.Contains(string(only), `const SHELL = ["index.html"]`) {
		t.Errorf("a partial shell: %v", err)
	}
	if _, err := ServiceWorker(fstest.MapFS{}, nil); err == nil {
		t.Error("an empty site has nothing to keep")
	}
}

func TestHandlerServesTheServiceWorkerOnlyWhenAsked(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte("<html>")}, "main.wasm": {Data: []byte("w")}}
	rec := get(Handler(fsys, Options{ServiceWorker: true}), "GET", "/sw.js", nil)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || !strings.Contains(rec.Body.String(), "main.wasm") {
		t.Errorf("sw.js: %d %v", rec.Code, rec.Header())
	}
	if rec := get(Handler(fsys, Options{}), "GET", "/sw.js", nil); rec.Code != 404 {
		t.Errorf("without the option sw.js is a 404, got %d", rec.Code)
	}
}
