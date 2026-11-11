//go:build js && wasm

package html

import "syscall/js"

// FormElement is an Element known to be a <form>.
type FormElement struct{ Element }

// AsForm casts e to a FormElement, or returns nil.
func (e *Element) AsForm() *FormElement {
	if e.GetTagName() != "FORM" {
		return nil
	}
	return &FormElement{Element: *e}
}

// Submit submits the form, bypassing submit listeners.
func (f *FormElement) Submit() { f.Call("submit") }

// Reset resets every control to its default value.
func (f *FormElement) Reset() { f.Call("reset") }

// CheckValidity reports whether every control is valid.
func (f *FormElement) CheckValidity() bool { return f.Call("checkValidity").Bool() }

// ReportValidity checks validity and shows the first invalid control.
func (f *FormElement) ReportValidity() bool { return f.Call("reportValidity").Bool() }

// Action returns the URL the form submits to.
func (f *FormElement) Action() string { return f.Get("action").String() }

// SetAction sets the URL the form submits to.
func (f *FormElement) SetAction(action string) { f.Set("action", action) }

// Method returns the form's HTTP method ("get" or "post").
func (f *FormElement) Method() string { return f.Get("method").String() }

// SetMethod sets the form's HTTP method.
func (f *FormElement) SetMethod(method string) { f.Set("method", method) }

// Elements returns the form's controls in document order.
func (f *FormElement) Elements() []Element {
	return wrapAll(f.Get("elements"))
}

// SubmitEvent wraps a form's "submit" DOM event.
type SubmitEvent struct {
	Value js.Value
}

// PreventDefault stops the default full-page submit.
func (e SubmitEvent) PreventDefault() { e.Value.Call("preventDefault") }

// StopPropagation stops the event from bubbling further.
func (e SubmitEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// OnSubmit registers fn for the submit event.
func (f *FormElement) OnSubmit(fn func(evt SubmitEvent)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		fn(SubmitEvent{Value: ev})
		return nil
	})
	f.AddEventListener("submit", cb)
	return cb
}
