//go:build !js

package console

import (
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
)

// A program that is not a page links the writers of the file formats in; a page does not (they are a second
// program there, export/remote: it gives the Shell a Registry of its own).
func init() { registerExport = func(r *transfer.Registry) { export.Register(r) } }
