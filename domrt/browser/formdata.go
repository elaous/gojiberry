//go:build js && wasm

package browser

import (
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/dom"
)

// FormData wraps a JS FormData object: a set of key/value entries
// (strings and/or files) suitable for a multipart/form-data request
// body -- see FetchOptions.BodyValue.
type FormData struct {
	Value js.Value
}

// NewFormData creates an empty FormData.
func NewFormData() FormData {
	return FormData{Value: js.Global().Get("FormData").New()}
}

// NewFormDataFromForm creates a FormData pre-populated from form's
// current controls, the same values a native (non-JS) submit of that
// form would send.
func NewFormDataFromForm(form *dom.FormElement) FormData {
	return FormData{Value: js.Global().Get("FormData").New(form.JSValue())}
}

// Append adds a string value under key (does not replace an existing
// value under the same key -- see Set).
func (f FormData) Append(key, value string) { f.Value.Call("append", key, value) }

// AppendFile adds file under key with the given filename.
func (f FormData) AppendFile(key string, file File, filename string) {
	f.Value.Call("append", key, file.Value, filename)
}

// Set adds a string value under key, replacing any existing value(s) there.
func (f FormData) Set(key, value string) { f.Value.Call("set", key, value) }

// Get returns the first value stored under key, or "" if there is none.
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
