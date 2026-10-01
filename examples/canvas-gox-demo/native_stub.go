//go:build !(js && wasm)

package main

// main is a no-op so the package still builds natively (go build ./...);
// the real entry point in main.go is js/wasm-only.
func main() {}
