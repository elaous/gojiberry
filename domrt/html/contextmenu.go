//go:build js && wasm

package html

import "syscall/js"

// OnContextMenu registers fn for the contextmenu event (a right-click
// or its platform equivalent), suppressing the browser's native menu
// and reporting the click's position relative to the viewport --
// pair with components.ContextMenu (or your own) to render one there.
func (e *Element) OnContextMenu(fn func(x, y float64)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		ev := args[0]
		ev.Call("preventDefault")
		fn(ev.Get("clientX").Float(), ev.Get("clientY").Float())
		return nil
	})
	e.AddEventListener("contextmenu", cb)
	return cb
}
