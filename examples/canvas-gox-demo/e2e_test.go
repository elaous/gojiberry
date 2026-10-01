//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestEndToEnd builds the WASM client and drives it in headless
// Chromium through e2e/verify.js (skipped when node, playwright or
// chromium aren't available).
func TestEndToEnd(t *testing.T) { e2etest.Run(t, "e2e/verify.js") }
