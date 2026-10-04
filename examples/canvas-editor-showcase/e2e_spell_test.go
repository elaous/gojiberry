//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestSpellCheckAndHyphenation drives the spelling tab and the hyphenation
// switch in a real browser.
func TestSpellCheckAndHyphenation(t *testing.T) { e2etest.Run(t, "e2e/spell.js") }
