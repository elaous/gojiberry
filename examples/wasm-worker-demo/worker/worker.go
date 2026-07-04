//go:build js && wasm

// Package main runs inside a dedicated Web Worker (see domrt/worker):
// on each message {n: int}, it counts primes up to n via plain trial
// division and posts {n, count} back, proving a real, separate Go/WASM
// binary is doing the computation on its own background thread, not
// the page's main thread.
package main

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/worker"
)

func countPrimes(n int) int {
	count := 0
	for i := 2; i <= n; i++ {
		isPrime := true
		for d := 2; d*d <= i; d++ {
			if i%d == 0 {
				isPrime = false
				break
			}
		}
		if isPrime {
			count++
		}
	}
	return count
}

func main() {
	worker.OnMessage(func(data js.Value) {
		n := data.Get("n").Int()
		worker.PostMessage(map[string]any{"n": n, "count": countPrimes(n)})
	})

	// Fetching and instantiating this binary is itself async, so the
	// main thread's first PostMessage can arrive before the line above
	// runs and be dropped. Signal readiness explicitly instead of
	// assuming the main thread's first message will be received.
	worker.PostMessage(map[string]any{"ready": true})

	select {} // keep the worker alive to receive further messages
}
