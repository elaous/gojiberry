//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/html"
)

// FormData wraps a JS FormData object.
type FormData struct {
	Value js.Value
}

// NewFormData creates an empty FormData.
func NewFormData() FormData {
	return FormData{Value: js.Global().Get("FormData").New()}
}

// NewFormDataFromForm creates a FormData filled from a form.
func NewFormDataFromForm(form *html.FormElement) FormData {
	return FormData{Value: js.Global().Get("FormData").New(form.JSValue())}
}

// Append adds a string value under key, keeping existing values.
func (f FormData) Append(key, value string) { f.Value.Call("append", key, value) }

// AppendFile adds file under key with the given filename.
func (f FormData) AppendFile(key string, file File, filename string) {
	f.Value.Call("append", key, file.Value, filename)
}

// Set adds a string value under key, replacing existing values.
func (f FormData) Set(key, value string) { f.Value.Call("set", key, value) }

// Get returns the first value stored under key, or "".
func (f FormData) Get(key string) string {
	v := f.Value.Call("get", key)
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	return v.String()
}

// Has reports whether any value is stored under key.
func (f FormData) Has(key string) bool { return f.Value.Call("has", key).Bool() }

// Delete removes every value stored under key.
func (f FormData) Delete(key string) { f.Value.Call("delete", key) }
