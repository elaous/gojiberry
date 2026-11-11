//go:build js && wasm

package html

import "syscall/js"

// MouseEvent wraps a mouse DOM event with its position.
type MouseEvent struct {
	Value                              js.Value
	X, Y                               float64
	Button                             int
	Detail                             int
	CtrlKey, ShiftKey, AltKey, MetaKey bool
}

func newMouseEvent(v js.Value) MouseEvent {
	return MouseEvent{
		Value:    v,
		X:        v.Get("clientX").Float(),
		Y:        v.Get("clientY").Float(),
		Button:   v.Get("button").Int(),
		Detail:   v.Get("detail").Int(),
		CtrlKey:  v.Get("ctrlKey").Bool(),
		ShiftKey: v.Get("shiftKey").Bool(),
		AltKey:   v.Get("altKey").Bool(),
		MetaKey:  v.Get("metaKey").Bool(),
	}
}

// PreventDefault suppresses the browser's default mouse handling.
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
