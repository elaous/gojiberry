//go:build !(js && wasm)

package main

// main is a no-op stand-in so this package -- whose real entry point,
// main.gox's func main, is js/wasm-only -- still satisfies "package
// main" on other platforms. This is what lets model.go/linewise.go's
// pure document logic (multi-cursor editing, line/word commands) be
// unit-tested natively via a plain `go test`, fast and without needing
// GOOS=js GOARCH=wasm or a browser at all -- canvas_editor_test.go
// (also native) still separately builds and drives the real WASM
// binary through Playwright for true end-to-end coverage.
func main() {}
