//go:build !js

package console

import (
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/transfer"
)

func init() { registerExport = func(r *transfer.Registry) { export.Register(r) } }
