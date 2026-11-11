//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
)

// WriteClipboardText writes text to the system clipboard.
func WriteClipboardText(text string) error {
	promise := js.Global().Get("navigator").Get("clipboard").Call("writeText", text)
	_, err := domrt.Await(promise)
	return err
}

// ReadClipboardText reads the current system clipboard text.
func ReadClipboardText() (string, error) {
	promise := js.Global().Get("navigator").Get("clipboard").Call("readText")
	v, err := domrt.Await(promise)
	if err != nil {
		return "", err
	}
	return v.String(), nil
}
