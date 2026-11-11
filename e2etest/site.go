//go:build !(js && wasm)

package e2etest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type Site struct {
	URL string
	dir string
	srv *httptest.Server

	mu       sync.Mutex
	handlers []siteHandler
}

type siteHandler struct {
	prefix string
	h      http.Handler
}

func (s *Site) Handle(prefix string, h http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers = append(s.handlers, siteHandler{prefix, h})
}

// Dir is the directory the site serves its files from.
func (s *Site) Dir() string { return s.dir }

func (s *Site) UseLocalhost() { s.URL = strings.Replace(s.URL, "127.0.0.1", "localhost", 1) }

func NewSite(t testing.TB, extra ...string) *Site {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "main.wasm"), ".")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building main.wasm: %v\n%s", err, out)
	}
	if st, err := os.Stat("files"); err == nil && st.IsDir() {
		build := exec.Command("go", "build", "-o", filepath.Join(dir, "files.wasm"), "./files")
		build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building files.wasm: %v\n%s", err, out)
		}
		if js, err := os.ReadFile("files-worker.js"); err == nil {
			if err := os.WriteFile(filepath.Join(dir, "files-worker.js"), js, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	execJS, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"wasm_exec.js": execJS, "index.html": index} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if ents, err := os.ReadDir("static"); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join("static", e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, pattern := range extra {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			t.Fatalf("e2etest: no files match %q: %v", pattern, err)
		}
		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.Base(m)), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	files := http.FileServer(http.Dir(dir))
	site := &Site{dir: dir}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.mu.Lock()
		handlers := append([]siteHandler(nil), site.handlers...)
		site.mu.Unlock()
		for _, h := range handlers {
			if strings.HasPrefix(r.URL.Path, h.prefix) {
				h.h.ServeHTTP(w, r)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/d/") || strings.HasPrefix(r.URL.Path, "/f/") {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	site.URL, site.srv = srv.URL, srv
	return site
}

func NewPageSite(t testing.TB, pages map[string]string) *Site {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Site{URL: srv.URL, srv: srv}
}
