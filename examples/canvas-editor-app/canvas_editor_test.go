package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanvasEditorEndToEnd builds the WASM client, serves this
// directory over a real HTTP server, and runs e2e/verify.js against
// it through a real headless Chromium (via Playwright) -- driving the
// actual canvas-rendered editor through real keyboard/mouse/wheel
// input and asserting on #doc-state, since there's no DOM text to
// read a canvas-rendered document's content back from any other way.
// Skips (doesn't fail) when node, the playwright module, or the
// chromium executable aren't available, the same "skip when infra is
// missing" convention gojiberry/examples/canvas-app's Postgres tests
// use.
func TestCanvasEditorEndToEnd(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found on PATH")
	}

	chromiumPath := os.Getenv("PLAYWRIGHT_CHROMIUM")
	if chromiumPath == "" {
		chromiumPath = "/opt/pw-browsers/chromium"
	}
	if _, err := os.Stat(chromiumPath); err != nil {
		t.Skipf("no chromium executable at %s: %v", chromiumPath, err)
	}

	npmRoot, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		t.Skipf("npm root -g failed: %v", err)
	}
	nodePathEnv := strings.TrimSpace(string(npmRoot))
	if probe := exec.Command(nodePath, "-e", "require('playwright')"); true {
		probe.Env = append(os.Environ(), "NODE_PATH="+nodePathEnv)
		if out, err := probe.CombinedOutput(); err != nil {
			t.Skipf("playwright module not resolvable (NODE_PATH=%s): %v\n%s", nodePathEnv, err, out)
		}
	}

	if err := buildWASM(t); err != nil {
		t.Fatalf("building main.wasm: %v", err)
	}
	if err := ensureWasmExecJS(t); err != nil {
		t.Fatalf("staging wasm_exec.js: %v", err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(".")))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	cmd := exec.Command(nodePath, "e2e/verify.js")
	cmd.Env = append(os.Environ(),
		"NODE_PATH="+nodePathEnv,
		"PORT="+port,
		"PLAYWRIGHT_CHROMIUM="+chromiumPath,
	)
	out, err := cmd.CombinedOutput()
	t.Logf("verify.js output:\n%s", out)
	if err != nil {
		t.Fatalf("e2e/verify.js failed: %v", err)
	}
	if !strings.Contains(string(out), "ALL CHECKS PASSED") {
		t.Fatalf("e2e/verify.js did not report success")
	}
}

// buildWASM cross-compiles this package for GOOS=js GOARCH=wasm into
// main.wasm, the same build a real `make wasm` performs, so the test
// exercises the actual binary a user would run rather than a native
// build of the same source (which wouldn't even compile -- this
// package is js/wasm-only, see the //go:build tag on every file here).
func buildWASM(t *testing.T) error {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", "main.wasm", ".")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("go build output:\n%s", out)
	}
	return err
}

// ensureWasmExecJS copies the Go toolchain's own wasm_exec.js next to
// main.wasm if it isn't already there, the same manual step every
// other wasm example's Makefile/README documents.
func ensureWasmExecJS(t *testing.T) error {
	t.Helper()
	if _, err := os.Stat("wasm_exec.js"); err == nil {
		return nil
	}
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return err
	}
	src := filepath.Join(strings.TrimSpace(string(out)), "lib", "wasm", "wasm_exec.js")
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile("wasm_exec.js", data, 0o644)
}
