//go:build js && wasm

package browser

import (
	"fmt"
	"syscall/js"
)

// Position is one geolocation reading. Altitude, AltitudeAccuracy,
// Heading, and Speed are nil when the device doesn't report them.
type Position struct {
	Latitude, Longitude, Accuracy float64
	Altitude, AltitudeAccuracy    *float64
	Heading, Speed                *float64
}

// Geolocation error codes, matching the real PositionError.code values.
const (
	GeolocationPermissionDenied    = 1
	GeolocationPositionUnavailable = 2
	GeolocationTimeout             = 3
)

// GeolocationError is returned when a geolocation request fails.
type GeolocationError struct {
	Code    int // one of the Geolocation* constants above
	Message string
}

// Error implements the standard error interface.
func (e *GeolocationError) Error() string {
	return fmt.Sprintf("geolocation: %s (code %d)", e.Message, e.Code)
}

func positionFromJS(v js.Value) Position {
	coords := v.Get("coords")
	return Position{
		Latitude:         coords.Get("latitude").Float(),
		Longitude:        coords.Get("longitude").Float(),
		Accuracy:         coords.Get("accuracy").Float(),
		Altitude:         nullableFloat(coords.Get("altitude")),
		AltitudeAccuracy: nullableFloat(coords.Get("altitudeAccuracy")),
		Heading:          nullableFloat(coords.Get("heading")),
		Speed:            nullableFloat(coords.Get("speed")),
	}
}

func nullableFloat(v js.Value) *float64 {
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	f := v.Float()
	return &f
}

func geolocationErrorFromJS(v js.Value) *GeolocationError {
	return &GeolocationError{Code: v.Get("code").Int(), Message: v.Get("message").String()}
}

// CurrentPosition wraps navigator.geolocation.getCurrentPosition,
// blocking until the browser resolves it (prompting for permission
// first if needed) or reports an error.
func CurrentPosition() (Position, error) {
	result := make(chan Position, 1)
	fail := make(chan error, 1)

	onSuccess := js.FuncOf(func(this js.Value, args []js.Value) any {
		result <- positionFromJS(args[0])
		return nil
	})
	onError := js.FuncOf(func(this js.Value, args []js.Value) any {
		fail <- geolocationErrorFromJS(args[0])
		return nil
	})
	defer onSuccess.Release()
	defer onError.Release()

	js.Global().Get("navigator").Get("geolocation").Call("getCurrentPosition", onSuccess, onError)

	select {
	case p := <-result:
		return p, nil
	case err := <-fail:
		return Position{}, err
	}
}

// WatchPosition calls fn with every position update (or error) until
// the returned stop function is called.
func WatchPosition(fn func(Position, error)) (stop func()) {
	onSuccess := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(positionFromJS(args[0]), nil)
		return nil
	})
	onError := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(Position{}, geolocationErrorFromJS(args[0]))
		return nil
	})

	geolocation := js.Global().Get("navigator").Get("geolocation")
	id := geolocation.Call("watchPosition", onSuccess, onError)

	return func() {
		geolocation.Call("clearWatch", id)
		onSuccess.Release()
		onError.Release()
	}
}
