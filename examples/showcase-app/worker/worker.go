//go:build js && wasm

package main

import (
	"syscall/js"

	"github.com/elaous/xango/domrt/worker"
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

	worker.PostMessage(map[string]any{"ready": true})

	select {} // keep the worker alive to receive further messages
}
