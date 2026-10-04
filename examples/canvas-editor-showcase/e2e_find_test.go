//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestFindAndReplace drives find and replace in a real browser.
func TestFindAndReplace(t *testing.T) { e2etest.Run(t, "e2e/find.js") }
