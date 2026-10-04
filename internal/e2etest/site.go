//go:build !(js && wasm)

package e2etest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Site is a wasm example built for GOOS=js GOARCH=wasm and served from a temp
// directory with its index.html and wasm_exec.js.
type Site struct {
	URL string
	dir string
	srv *httptest.Server
}

// NewSite builds the package in the current directory (the example's own
// directory, which is what `go test` uses as the working directory) and serves
// it. A page at /d/{doc} is served index.html too, for documents addressed by
// path. It is closed when the test ends.
func NewSite(t *testing.T) *Site {
	t.Helper()
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "main.wasm"), ".")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building main.wasm: %v\n%s", err, out)
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
	files := http.FileServer(http.Dir(dir))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/d/") {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return &Site{URL: srv.URL, dir: dir, srv: srv}
}
