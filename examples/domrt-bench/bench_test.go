//go:build !(js && wasm)

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

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

func BenchmarkPatchAttrsOnly(b *testing.B) { benchmarkScenario(b, "attrs-only") }

func BenchmarkPatchKeyedReorder(b *testing.B) { benchmarkScenario(b, "keyed-reorder") }

func BenchmarkPatchAppend(b *testing.B) { benchmarkScenario(b, "append") }

func BenchmarkPatchFullReplace(b *testing.B) { benchmarkScenario(b, "full-replace") }
