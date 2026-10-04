//go:build !(js && wasm)

// Package main's own benchmark harness driver: task #128's rendering
// benchmarks for domrt's patch/patchChildren. Each Benchmark function
// below drives a real headless Chromium (internal/e2etest, chromedp) to
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
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// benchIterations is how many update cycles each scenario runs inside
// the wasm program per Benchmark call -- large enough that per-
// iteration browser/GC noise averages out, small enough that a full
// four-scenario run stays in the tens-of-seconds range.
const benchIterations = 500

func benchmarkScenario(b *testing.B, scenario string) {
	b.Helper()
	site := e2etest.NewSite(b)
	d := site.OpenURL(b, "/index.html")
	d.WaitFor("the harness", "typeof window.runBench === 'function'")

	b.ResetTimer()
	var result any
	d.Eval(fmt.Sprintf("window.runBench(%q, %d)", scenario, benchIterations), &result)
	b.StopTimer()
	// A missing favicon (this harness serves none) is expected degradation, not a
	// real failure.
	for _, e := range d.Errors() {
		if !strings.Contains(e, "Failed to load resource") {
			b.Fatalf("page errors: %v", d.Errors())
		}
	}
	ns, isNum := result.(float64)
	if !isNum {
		b.Fatalf("runBench error: %v", result)
	}
	b.ReportMetric(ns/float64(benchIterations), "ns/op")
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
