//go:build js && wasm

package html

import "syscall/js"

// MouseEvent wraps a mousedown/mousemove/mouseup DOM event with its
// position relative to the viewport (clientX/clientY), which button
// (0 = left/primary, 1 = middle, 2 = right, matching the DOM's own
// MouseEvent.button -- mousedown/mouseup fire for every button, not
// just the primary one, so check this before treating one as a plain
// click) and modifier keys -- gox.Event carries neither at all (see
// its doc comment), so,
// like DragEvent and KeyEvent, this is a typed escape hatch for
// building anything that needs a real click/drag position or a
// modifier-qualified click: canvas hit-testing, custom selection,
// image cropping, Alt+Click to add a multi-cursor caret, and so on.
type MouseEvent struct {
	Value                              js.Value
	X, Y                               float64
	Button                             int
	CtrlKey, ShiftKey, AltKey, MetaKey bool
}

func newMouseEvent(v js.Value) MouseEvent {
	return MouseEvent{
		Value:    v,
		X:        v.Get("clientX").Float(),
		Y:        v.Get("clientY").Float(),
		Button:   v.Get("button").Int(),
		CtrlKey:  v.Get("ctrlKey").Bool(),
		ShiftKey: v.Get("shiftKey").Bool(),
		AltKey:   v.Get("altKey").Bool(),
		MetaKey:  v.Get("metaKey").Bool(),
	}
}

// PreventDefault suppresses the browser's default handling of the
// event. This matters more here than it might seem: clicking a
// non-focusable element (a plain <canvas>, say) makes the browser
// clear focus from whatever was focused once every mousedown handler
// returns -- calling a different element's Focus() from inside your
// own mousedown handler gets silently undone right after unless you
// call PreventDefault first.
func (e MouseEvent) PreventDefault() { e.Value.Call("preventDefault") }

// StopPropagation stops the event from bubbling further.
func (e MouseEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// OnMouseDown registers fn for the mousedown event.
func (e *Element) OnMouseDown(fn func(MouseEvent)) js.Func {
	return e.onMouseEvent("mousedown", fn)
}

// OnMouseMove registers fn for the mousemove event.
func (e *Element) OnMouseMove(fn func(MouseEvent)) js.Func {
	return e.onMouseEvent("mousemove", fn)
}

// OnMouseUp registers fn for the mouseup event.
func (e *Element) OnMouseUp(fn func(MouseEvent)) js.Func {
	return e.onMouseEvent("mouseup", fn)
}

func (e *Element) onMouseEvent(event string, fn func(MouseEvent)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		fn(newMouseEvent(args[0]))
		return nil
	})
	e.AddEventListener(event, cb)
	return cb
}
