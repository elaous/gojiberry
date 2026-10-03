//go:build !(js && wasm)

// Package e2etest runs a browser end-to-end test for a wasm example:
// it builds the example for GOOS=js GOARCH=wasm, serves it from a temp
// directory, and runs a Playwright (Node) script against a real
// headless Chromium. It skips, rather than fails, when node, the
// playwright module or a chromium executable is missing.
package e2etest

import (
	_ "embed"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed lib.js
var libJS []byte

// Run builds the package in the current directory (the example's own
// directory, which is what `go test` uses as the working directory),
// serves it with index.html and wasm_exec.js, and runs script (a path
// relative to the example directory) with PORT, PLAYWRIGHT_CHROMIUM and
// E2E_LIB in its environment. The script must print "ALL CHECKS PASSED".
func Run(t *testing.T, script string) { t.Helper(); RunEnv(t, script) }

// RunEnv is Run with extra environment variables ("NAME=value") for the
// script: a test that starts a server of its own tells the script where it
// is that way.
func RunEnv(t *testing.T, script string, extraEnv ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH")
	}
	chromium := os.Getenv("PLAYWRIGHT_CHROMIUM")
	if chromium == "" {
		chromium = "/opt/pw-browsers/chromium"
	}
	if _, err := os.Stat(chromium); err != nil {
		t.Skipf("no chromium executable at %s: %v", chromium, err)
	}
	npmRoot, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		t.Skipf("npm root -g failed: %v", err)
	}
	nodePath := strings.TrimSpace(string(npmRoot))
	probe := exec.Command(node, "-e", "require('playwright')")
	probe.Env = append(os.Environ(), "NODE_PATH="+nodePath)
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("playwright module not resolvable (NODE_PATH=%s): %v\n%s", nodePath, err, out)
	}

	site := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(site, "main.wasm"), ".")
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
	for name, data := range map[string][]byte{"wasm_exec.js": execJS, "index.html": index, "lib.js": libJS} {
		if err := os.WriteFile(filepath.Join(site, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(site)))
	defer srv.Close()
	abs, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, abs)
	cmd.Env = append(os.Environ(),
		"NODE_PATH="+nodePath,
		"PORT="+srv.URL[strings.LastIndex(srv.URL, ":")+1:],
		"PLAYWRIGHT_CHROMIUM="+chromium,
		"E2E_LIB="+filepath.Join(site, "lib.js"),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	t.Logf("%s output:\n%s", script, out)
	if err != nil {
		t.Fatalf("%s failed: %v", script, err)
	}
	if !strings.Contains(string(out), "ALL CHECKS PASSED") {
		t.Fatalf("%s did not report success", script)
	}
}
