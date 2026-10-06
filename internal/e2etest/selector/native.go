//go:build !(js && wasm)

package main

// main is empty off the browser: the program is built for js/wasm (main.go), and this lets
// `go build ./...` and `go test` (pure.go's functions) work on the host.
func main() {}
