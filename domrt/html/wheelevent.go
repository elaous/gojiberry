//go:build js && wasm

package html

import "syscall/js"

// WheelEvent wraps a wheel DOM event -- xango.Event carries no delta
// information at all (see its doc comment), so, like MouseEvent and
// KeyEvent, this is a typed escape hatch for anything that needs real
// scroll input: a canvas-rendered document's own scroll offset (which
// has no native scrollbar to drive it, unlike a normal DOM element),
// a custom slider, a zoomable canvas.
type WheelEvent struct {
	Value          js.Value
	DeltaX, DeltaY float64
}

func newWheelEvent(v js.Value) WheelEvent {
	return WheelEvent{
		Value:  v,
		DeltaX: v.Get("deltaX").Float(),
		DeltaY: v.Get("deltaY").Float(),
	}
}

// PreventDefault suppresses the browser's default handling of the
// event -- e.g. stopping the page itself from scrolling while a
// canvas-rendered surface handles the wheel input instead.
func (e WheelEvent) PreventDefault() { e.Value.Call("preventDefault") }

// StopPropagation stops the event from bubbling further.
func (e WheelEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// OnWheel registers fn for the wheel event.
func (e *Element) OnWheel(fn func(WheelEvent)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		fn(newWheelEvent(args[0]))
		return nil
	})
	e.AddEventListener("wheel", cb)
	return cb
}
