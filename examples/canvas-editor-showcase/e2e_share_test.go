//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestShareAndStats checks the word count and the share-the-selection link in
// a real browser.
func TestShareAndStats(t *testing.T) { e2etest.Run(t, "e2e/share.js") }
