// Package serve is the side of package export/remote that does the work: it holds the whole of package
// export and answers one request at a time.
package serve

import (
	"encoding/json"
	"errors"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/export/remote"
	"github.com/elaous/gojiberry/transfer"
)

var registry = func() *transfer.Registry {
	r := transfer.Default()
	export.Register(r)
	return r
}()

// ErrUnknownOp is returned for an operation this package does not know.
var ErrUnknownOp = errors.New("serve: unknown operation")

// Handle answers one request of package remote.
func Handle(op, format string, in []byte) ([]byte, error) {
	switch op {
	case remote.OpImport:
		d, err := registry.Import(format, "", in)
		if err != nil {
			return nil, err
		}
		return docfile.JSON.Marshal(d)
	case remote.OpExport:
		d, err := docfile.JSON.Unmarshal(in)
		if err != nil {
			return nil, err
		}
		_, data, _, err := registry.Export(format, d)
		return data, err
	case remote.OpFidelity:
		d, err := docfile.JSON.Unmarshal(in)
		if err != nil {
			return nil, err
		}
		return json.Marshal(export.Fidelity(format, d))
	case remote.OpImportFidelity:
		return json.Marshal(export.ImportFidelity(format, in))
	}
	return nil, ErrUnknownOp
}
