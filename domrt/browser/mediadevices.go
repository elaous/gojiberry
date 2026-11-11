//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// MediaStreamConstraints selects which media getUserMedia returns.
type MediaStreamConstraints struct {
	Audio bool
	Video bool
}

// GetUserMedia prompts for, or silently grants, camera and microphone access.
func GetUserMedia(constraints MediaStreamConstraints) (js.Value, error) {
	jsConstraints := map[string]any{
		"audio": constraints.Audio,
		"video": constraints.Video,
	}
	promise := js.Global().Get("navigator").Get("mediaDevices").Call("getUserMedia", jsConstraints)
	return domrt.Await(promise)
}

// MediaDeviceInfo describes one camera, microphone or speaker.
type MediaDeviceInfo struct {
	DeviceID string
	Kind     string
	Label    string
}

// EnumerateDevices lists the available media devices.
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

// SetVideoSource assigns stream as the source of a video element.
func SetVideoSource(videoElement js.Value, stream js.Value) {
	videoElement.Set("srcObject", stream)
}

// StopStream stops every track in stream.
func StopStream(stream js.Value) {
	tracks := stream.Call("getTracks")
	for i := 0; i < tracks.Length(); i++ {
		tracks.Index(i).Call("stop")
	}
}
