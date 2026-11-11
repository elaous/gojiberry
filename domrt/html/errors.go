//go:build js && wasm

package html

import "syscall/js"

// JSError wraps an error surfaced from JavaScript.
type JSError struct {
	Message string
	Source  string
	Line    int // 1-based; 0 for a rejection
	Column  int // 1-based; 0 for a rejection
	Value   js.Value
}

// Error implements the standard error interface.
func (e *JSError) Error() string { return e.Message }

// AsGoError returns e as a plain error.
func (e *JSError) AsGoError() error { return e }

// AsJSException returns the underlying JS value.
func (e *JSError) AsJSException() js.Value { return e.Value }

// NewJSErrorFromEvent builds a JSError from an ErrorEvent.
func NewJSErrorFromEvent(ev js.Value) *JSError {
	// a plain Event (a script that failed to load) is no ErrorEvent
	str := func(k string) string {
		if v := ev.Get(k); v.Type() == js.TypeString {
			return v.String()
		}
		return ""
	}
	num := func(k string) int {
		if v := ev.Get(k); v.Type() == js.TypeNumber {
			return v.Int()
		}
		return 0
	}
	value := ev.Get("error")
	message := str("message")
	if message == "" {
		message = messageFromValue(value)
	}
	return &JSError{
		Message: message,
		Source:  str("filename"),
		Line:    num("lineno"),
		Column:  num("colno"),
		Value:   value,
	}
}

func jsErrorFromRejection(ev js.Value) *JSError {
	reason := ev.Get("reason")
	return &JSError{Message: messageFromValue(reason), Value: reason}
}

func messageFromValue(v js.Value) string {
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	if v.Type() == js.TypeString {
		return v.String()
	}
	if m := v.Get("message"); m.Type() == js.TypeString {
		return m.String()
	}
	return v.Call("toString").String()
}

// OnError registers fn for uncaught exceptions on the page.
func (w *Window) OnError(fn func(err *JSError)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		fn(NewJSErrorFromEvent(args[0]))
		return nil
	})
	w.Window.Call("addEventListener", "error", cb)
	return cb
}

// OnUnhandledRejection registers fn for unhandled promise rejections.
func (w *Window) OnUnhandledRejection(fn func(err *JSError)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		fn(jsErrorFromRejection(args[0]))
		return nil
	})
	w.Window.Call("addEventListener", "unhandledrejection", cb)
	return cb
}
