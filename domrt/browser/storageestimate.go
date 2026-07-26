//go:build js && wasm

package browser

import (
	"errors"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// StorageEstimate is navigator.storage.estimate()'s own {usage, quota}
// result, in bytes -- a browser's own best estimate of how much of
// this origin's storage quota (LocalStorage + IndexedDB + Cache
// Storage combined, everything StorageManager covers) is used versus
// available, not an exact accounting.
type StorageEstimate struct {
	Usage int64
	Quota int64
}

// EstimateStorage wraps navigator.storage.estimate(), blocking until
// the browser responds. Returns an error on older browsers with no
// StorageManager API at all (navigator.storage itself undefined) --
// checked directly rather than letting a bare .Call on an undefined
// value panic, since unlike most of this package's own browser APIs,
// this one has real gaps in the wild worth degrading from gracefully.
func EstimateStorage() (StorageEstimate, error) {
	storage := js.Global().Get("navigator").Get("storage")
	if storage.IsUndefined() {
		return StorageEstimate{}, errors.New("navigator.storage is not available in this browser")
	}
	estimate := storage.Get("estimate")
	if estimate.IsUndefined() {
		return StorageEstimate{}, errors.New("navigator.storage.estimate is not available in this browser")
	}
	v, err := domrt.Await(storage.Call("estimate"))
	if err != nil {
		return StorageEstimate{}, err
	}
	return StorageEstimate{
		Usage: int64(v.Get("usage").Float()),
		Quota: int64(v.Get("quota").Float()),
	}, nil
}
