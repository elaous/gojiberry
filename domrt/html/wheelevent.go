//go:build js && wasm

package html

import "syscall/js"

// WheelEvent wraps a wheel DOM event with its deltas.
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

// PreventDefault suppresses the browser's default wheel scrolling.
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
