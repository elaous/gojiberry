//go:build js && wasm

package html

import "syscall/js"

// DispatchCustomEvent dispatches a CustomEvent named name on e.
func (e *Element) DispatchCustomEvent(name string, detail any, bubbles bool) {
	init := map[string]any{"detail": detail, "bubbles": bubbles}
	ev := js.Global().Get("CustomEvent").New(name, init)
	e.Element.Call("dispatchEvent", ev)
}

// OnCustomEvent registers fn for a custom event named name.
func (e *Element) OnCustomEvent(name string, fn func(detail js.Value)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		var detail js.Value
		if len(args) > 0 {
			detail = args[0].Get("detail")
		}
		fn(detail)
		return nil
	})
	e.AddEventListener(name, cb)
	return cb
}
