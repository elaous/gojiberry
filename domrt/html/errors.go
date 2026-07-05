//go:build js && wasm

package html

import "syscall/js"

// JSError wraps an error surfaced from JS itself -- an uncaught
// exception (Window.OnError) or an unhandled promise rejection
// (Window.OnUnhandledRejection) -- rather than one returned by a typed
// wrapper in this package.
type JSError struct {
	Message string
	Source  string // the script's URL; "" for a rejection (there's no single throw site)
	Line    int    // 1-based; 0 for a rejection
	Column  int    // 1-based; 0 for a rejection
	Value   js.Value
}

// Error implements the standard error interface.
func (e *JSError) Error() string { return e.Message }

// AsGoError returns e as a plain error, for call sites that want that
// explicitly rather than relying on *JSError already satisfying it.
func (e *JSError) AsGoError() error { return e }

// AsJSException returns the underlying JS value (the Error object for
// an uncaught exception, or the rejection reason for an unhandled
// promise) -- for reading anything JSError doesn't model, like a
// custom error subclass's own fields or its stack trace.
func (e *JSError) AsJSException() js.Value { return e.Value }

// NewJSErrorFromEvent builds a JSError from a browser ErrorEvent (the
// event OnError and browser.Worker's OnError both receive) -- exported
// for domrt/browser's Worker to reuse the same construction.
func NewJSErrorFromEvent(ev js.Value) *JSError {
	value := ev.Get("error")
	message := ev.Get("message").String()
	if message == "" {
		message = messageFromValue(value)
	}
	return &JSError{
		Message: message,
		Source:  ev.Get("filename").String(),
		Line:    ev.Get("lineno").Int(),
		Column:  ev.Get("colno").Int(),
		Value:   value,
	}
}

// jsErrorFromRejection builds a JSError from a PromiseRejectionEvent
// (the event OnUnhandledRejection receives).
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

// OnError registers fn for the window's global "error" event: an
// uncaught exception from anywhere on the page (this program or any
// other script sharing it), not something a typed wrapper here already
// surfaces as a Go error.
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

// OnUnhandledRejection registers fn for the window's global
// "unhandledrejection" event: a rejected Promise nothing ever called
// .catch (or the second argument of .then) on.
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
