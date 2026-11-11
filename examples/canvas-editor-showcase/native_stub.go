//go:build !(js && wasm)

package main

import "errors"

func main() {}

func callFiles(op, format string, in []byte) ([]byte, error) {
	return nil, errors.New("the file worker runs in the browser")
}
