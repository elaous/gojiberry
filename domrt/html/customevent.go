//go:build js && wasm

package html

import "syscall/js"

// DispatchCustomEvent dispatches a CustomEvent named name on e,
// carrying detail as the event's detail property (any structured-clone-
// able value: a string, number, bool, map, slice, or js.Value). Set
// bubbles to let ancestor listeners (registered with AddEventListener
// or OnCustomEvent) see it too.
func (e *Element) DispatchCustomEvent(name string, detail any, bubbles bool) {
	init := map[string]any{"detail": detail, "bubbles": bubbles}
	ev := js.Global().Get("CustomEvent").New(name, init)
	e.Element.Call("dispatchEvent", ev)
}

// OnCustomEvent registers fn for a custom event named name, e.g. one
// dispatched by DispatchCustomEvent (by e or a descendant, if bubbling)
// or by non-Go JS elsewhere on the page. fn receives the event's raw
// detail value; use js.Value's own accessors (or xango.EventValue-style
// type assertions) to read it, since detail's shape is whatever the
// dispatcher chose.
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
