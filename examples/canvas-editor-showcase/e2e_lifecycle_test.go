//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestDocumentLifecycle drives versions, duplicate, tags and the trash in the
// Save tab.
func TestDocumentLifecycle(t *testing.T) { e2etest.Run(t, "e2e/lifecycle.js") }
