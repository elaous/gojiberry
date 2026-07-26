// Package main's own benchmark harness driver: task #128's rendering
// benchmarks for domrt's patch/patchChildren. Each Benchmark function
// below spawns e2e/bench.js (a real headless Chromium run, driven the
// same way every other gojiberry example's own e2e/verify.js is) to
// run a fixed, generous number of update cycles of one named scenario
// entirely inside the wasm program itself, timed there with Go's own
// time.Now/time.Since so per-call browser-launch/subprocess overhead
// never pollutes the number -- only the actual patch/patchChildren
// work is measured.
//
// Deliberately not the usual "scale by b.N until benchtime elapses"
// shape: browser-launch overhead (roughly a second) dwarfs any single
// update cycle, so letting Go's own calibration loop drive real
// work size would either measure launch overhead instead of patch
// cost (small b.N) or spawn many redundant browser processes chasing
// a stable measurement it'll never reach that way (large b.N). Instead
// each scenario always runs a fixed benchIterations cycles internally,
// and reports its own ns/op via b.ReportMetric -- which, per
// testing.B's own documented behavior, replaces Go's default
// (wall-time-since-ResetTimer-divided-by-b.N) computation for that
// exact metric name. Run with `go test -bench=. -benchtime=1x .` (or
// `make bench`) so Go's own calibration loop doesn't also try to scale
// b.N -- see this package's own Makefile.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// benchIterations is how many update cycles each scenario runs inside
// the wasm program per Benchmark call -- large enough that per-
// iteration browser/GC noise averages out, small enough that a full
// four-scenario run stays in the tens-of-seconds range.
const benchIterations = 500

func benchmarkScenario(b *testing.B, scenario string) {
	b.Helper()
	nodePath, chromiumPath, nodePathEnv := requirePlaywrightForBench(b)

	if err := buildWASM(b); err != nil {
		b.Fatalf("building main.wasm: %v", err)
	}
	if err := ensureWasmExecJS(b); err != nil {
		b.Fatalf("staging wasm_exec.js: %v", err)
	}

	srv := httptest.NewServer(http.FileServer(http.Dir(".")))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	b.ResetTimer()
	cmd := exec.Command(nodePath, "e2e/bench.js")
	cmd.Env = append(os.Environ(),
		"NODE_PATH="+nodePathEnv,
		"PORT="+port,
		"PLAYWRIGHT_CHROMIUM="+chromiumPath,
		"BENCH_SCENARIO="+scenario,
		"BENCH_N="+strconv.Itoa(benchIterations),
	)
	out, err := cmd.CombinedOutput()
	b.StopTimer()
	if err != nil {
		b.Fatalf("e2e/bench.js failed: %v\n%s", err, out)
	}

	ns, err := parseResultNs(string(out))
	if err != nil {
		b.Fatalf("parsing e2e/bench.js output: %v\n%s", err, out)
	}
	b.ReportMetric(ns/float64(benchIterations), "ns/op")
}

// parseResultNs extracts the float64 nanosecond count off bench.js's
// own "RESULT:<float>" trailer line.
func parseResultNs(output string) (float64, error) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "RESULT:"); ok {
			return strconv.ParseFloat(v, 64)
		}
	}
	return 0, fmt.Errorf("no RESULT: line found")
}

// BenchmarkPatchAttrsOnly measures the cheapest real patch path: same
// keys, same order, every cycle -- only each <li>'s own text content
// changes, so patch never creates, destroys, or moves a single node.
func BenchmarkPatchAttrsOnly(b *testing.B) { benchmarkScenario(b, "attrs-only") }

// BenchmarkPatchKeyedReorder measures patchChildren's key-match lookup
// plus its own DOM-order-fixup pass: the same keys reshuffle into a
// new order every cycle, so every child needs an insertBefore call
// even though none are created or destroyed.
func BenchmarkPatchKeyedReorder(b *testing.B) { benchmarkScenario(b, "keyed-reorder") }

// BenchmarkPatchAppend measures patchChildren's cheapest growth path:
// every prior key stays exactly where it was, one new keyed child
// appends at the end each cycle -- the "add one more item" interaction
// a real app actually hits most often.
func BenchmarkPatchAppend(b *testing.B) { benchmarkScenario(b, "append") }

// BenchmarkPatchFullReplace measures patchChildren's worst case: a
// completely different key set every cycle, so nothing matches --
// every old child is torn down and every new one created fresh.
func BenchmarkPatchFullReplace(b *testing.B) { benchmarkScenario(b, "full-replace") }

// requirePlaywrightForBench mirrors canvas-editor-app's own
// TestCanvasEditorEndToEnd preflight (node on PATH, a real chromium
// executable, the playwright module resolvable) but skips the
// *benchmark*, not a test, when any of it is missing -- b.Skip works
// the same way t.Skip does for a *testing.B.
func requirePlaywrightForBench(b *testing.B) (nodePath, chromiumPath, nodePathEnv string) {
	b.Helper()
	var err error
	nodePath, err = exec.LookPath("node")
	if err != nil {
		b.Skip("node not found on PATH")
	}

	chromiumPath = os.Getenv("PLAYWRIGHT_CHROMIUM")
	if chromiumPath == "" {
		chromiumPath = "/opt/pw-browsers/chromium"
	}
	if _, err := os.Stat(chromiumPath); err != nil {
		b.Skipf("no chromium executable at %s: %v", chromiumPath, err)
	}

	npmRoot, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		b.Skipf("npm root -g failed: %v", err)
	}
	nodePathEnv = strings.TrimSpace(string(npmRoot))
	probe := exec.Command(nodePath, "-e", "require('playwright')")
	probe.Env = append(os.Environ(), "NODE_PATH="+nodePathEnv)
	if out, err := probe.CombinedOutput(); err != nil {
		b.Skipf("playwright module not resolvable (NODE_PATH=%s): %v\n%s", nodePathEnv, err, out)
	}
	return nodePath, chromiumPath, nodePathEnv
}

// buildWASM cross-compiles this package for GOOS=js GOARCH=wasm into
// main.wasm, the same build canvas-editor-app's own buildWASM (and a
// real `make wasm`) performs.
func buildWASM(b *testing.B) error {
	b.Helper()
	cmd := exec.Command("go", "build", "-o", "main.wasm", ".")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.Logf("go build output:\n%s", out)
	}
	return err
}

// ensureWasmExecJS copies the Go toolchain's own wasm_exec.js next to
// main.wasm if it isn't already there.
func ensureWasmExecJS(b *testing.B) error {
	b.Helper()
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
