//go:build !(js && wasm)

package main

// main is a no-op so the package still builds natively (go build ./...).
func main() {}
