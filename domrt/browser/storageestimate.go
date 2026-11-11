//go:build js && wasm

package browser

import (
	"errors"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// StorageEstimate is navigator.storage.estimate's result.
type StorageEstimate struct {
	Usage int64
	Quota int64
}

// EstimateStorage wraps navigator.storage.estimate.
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
