//go:build !(js && wasm)

package main

import "errors"

// main is a no-op so the package still builds natively (go build ./...);
// the real entry point in main.go is js/wasm-only.
func main() {}

// callFiles is the request to the file worker, which only exists in the browser.
func callFiles(op, format string, in []byte) ([]byte, error) {
	return nil, errors.New("the file worker runs in the browser")
}
