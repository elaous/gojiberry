//go:build js && wasm

package html

import "syscall/js"

// DataTransfer wraps a drag event's dataTransfer object.
type DataTransfer struct {
	Value js.Value
}

// SetData stores data under format for the drop target.
func (d *DataTransfer) SetData(format, data string) { d.Value.Call("setData", format, data) }

// GetData retrieves the data stored under format.
func (d *DataTransfer) GetData(format string) string { return d.Value.Call("getData", format).String() }

// DropEffect returns the drop effect the target will use.
func (d *DataTransfer) DropEffect() string { return d.Value.Get("dropEffect").String() }

// SetDropEffect sets the drop effect the target will use.
func (d *DataTransfer) SetDropEffect(effect string) { d.Value.Set("dropEffect", effect) }

// EffectAllowed returns the drop effects the source permits.
func (d *DataTransfer) EffectAllowed() string { return d.Value.Get("effectAllowed").String() }

// SetEffectAllowed sets the drop effects the drag source permits.
func (d *DataTransfer) SetEffectAllowed(effect string) { d.Value.Set("effectAllowed", effect) }

// Files returns the files dropped, if any.
func (d *DataTransfer) Files() []js.Value {
	files := d.Value.Get("files")
	out := make([]js.Value, files.Length())
	for i := range out {
		out[i] = files.Index(i)
	}
	return out
}

// DragEvent wraps a drag or drop DOM event.
type DragEvent struct {
	Value        js.Value
	DataTransfer *DataTransfer
}

func newDragEvent(v js.Value) DragEvent {
	return DragEvent{Value: v, DataTransfer: &DataTransfer{Value: v.Get("dataTransfer")}}
}

// PreventDefault suppresses the browser's default handling of the event.
func (e DragEvent) PreventDefault() { e.Value.Call("preventDefault") }

// StopPropagation stops the event from bubbling further.
func (e DragEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// SetDraggable marks (or unmarks) e as a drag source.
func (e *Element) SetDraggable(draggable bool) { e.Element.Set("draggable", draggable) }

// OnDragStart, OnDragOver and OnDrop register drag-and-drop listeners.
func (e *Element) OnDragStart(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("dragstart", fn)
}

// OnDragOver registers fn for the dragover event.
func (e *Element) OnDragOver(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("dragover", fn)
}

// OnDrop registers fn for the drop event.
func (e *Element) OnDrop(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("drop", fn)
}

func (e *Element) onDragEvent(event string, fn func(evt DragEvent)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(newDragEvent(args[0]))
		return nil
	})
	e.AddEventListener(event, cb)
	return cb
}
