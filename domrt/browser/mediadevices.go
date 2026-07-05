//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// MediaStreamConstraints selects which media getUserMedia should
// request. Leave a field false to not request that kind of track.
type MediaStreamConstraints struct {
	Audio bool
	Video bool
}

// GetUserMedia prompts for (or silently grants, depending on prior
// permission) camera/microphone access and returns the resulting
// MediaStream, blocking until the browser responds.
func GetUserMedia(constraints MediaStreamConstraints) (js.Value, error) {
	jsConstraints := map[string]any{
		"audio": constraints.Audio,
		"video": constraints.Video,
	}
	promise := js.Global().Get("navigator").Get("mediaDevices").Call("getUserMedia", jsConstraints)
	return domrt.Await(promise)
}

// MediaDeviceInfo describes one camera/microphone/speaker as reported
// by EnumerateDevices.
type MediaDeviceInfo struct {
	DeviceID string
	Kind     string
	Label    string
}

// EnumerateDevices lists the available media input/output devices.
// Label is only populated for devices the page already has permission
// to use (e.g. after a successful GetUserMedia call).
func EnumerateDevices() ([]MediaDeviceInfo, error) {
	promise := js.Global().Get("navigator").Get("mediaDevices").Call("enumerateDevices")
	v, err := domrt.Await(promise)
	if err != nil {
		return nil, err
	}
	out := make([]MediaDeviceInfo, v.Length())
	for i := range out {
		d := v.Index(i)
		out[i] = MediaDeviceInfo{
			DeviceID: d.Get("deviceId").String(),
			Kind:     d.Get("kind").String(),
			Label:    d.Get("label").String(),
		}
	}
	return out, nil
}

// SetVideoSource assigns stream (from GetUserMedia) as a <video>
// element's source, the srcObject-based replacement for the old
// createObjectURL(stream) pattern.
func SetVideoSource(videoElement js.Value, stream js.Value) {
	videoElement.Set("srcObject", stream)
}

// StopStream stops every track in stream (camera/mic LED off, etc.).
func StopStream(stream js.Value) {
	tracks := stream.Call("getTracks")
	for i := 0; i < tracks.Length(); i++ {
		tracks.Index(i).Call("stop")
	}
}
