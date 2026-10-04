//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestAccessibilityAndTranslation drives the screen-reader copy of the
// document and the interface language switch.
func TestAccessibilityAndTranslation(t *testing.T) { e2etest.Run(t, "e2e/a11y.js") }
