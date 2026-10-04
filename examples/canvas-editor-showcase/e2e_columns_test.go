//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestColumnCommands drives the column commands from the Edit menu.
func TestColumnCommands(t *testing.T) { e2etest.Run(t, "e2e/columns.js") }
