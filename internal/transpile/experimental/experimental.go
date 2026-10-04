// Package experimental holds xango resource-import loaders that are
// either shell-outs to (and thin wrappers around) a mature external
// tool this project doesn't try to reimplement (HCL/proto/capnp/sqlc,
// each generating a real Go subpackage) or carry real footgun risk
// (the JSON .(TypeName) annotation lives in the main transpile
// package but is gated the same way, since it's sample-based
// inference rather than a schema).
//
// None of this runs unless a caller explicitly opts in: pass
// Loaders() as transpile.Options.ExperimentalLoaders with
// Options.Experimental set to true (xgc's -experimental flag does
// this). Without that, xango still recognizes these extensions -- so it
// can give an actionable "this needs experimental mode" error -- but
// this package's code is never reached.
package experimental

import "github.com/elaous/gojiberry/internal/transpile"

// Loaders returns the loader implementations for every experimental
// resource-import extension, for wiring into
// transpile.Options.ExperimentalLoaders.
func Loaders() map[string]transpile.LoaderFunc {
	return map[string]transpile.LoaderFunc{
		".hcl":   LoadHCL,
		".proto": LoadProto,
		".capnp": LoadCapnProto,
		".sql":   LoadSQL,
	}
}
