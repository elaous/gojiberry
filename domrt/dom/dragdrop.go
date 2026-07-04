//go:build js && wasm

package dom

import "syscall/js"

// DataTransfer wraps a drag event's dataTransfer object, carrying
// whatever data a drag operation is moving and the drop/drag-effect
// negotiation between source and target.
type DataTransfer struct {
	Value js.Value
}

func (d *DataTransfer) SetData(format, data string)  { d.Value.Call("setData", format, data) }
func (d *DataTransfer) GetData(format string) string { return d.Value.Call("getData", format).String() }

func (d *DataTransfer) DropEffect() string             { return d.Value.Get("dropEffect").String() }
func (d *DataTransfer) SetDropEffect(effect string)    { d.Value.Set("dropEffect", effect) }
func (d *DataTransfer) EffectAllowed() string          { return d.Value.Get("effectAllowed").String() }
func (d *DataTransfer) SetEffectAllowed(effect string) { d.Value.Set("effectAllowed", effect) }

// Files returns the raw File objects dropped (e.g. from a desktop
// file drag), if any -- empty for a drag that only carries setData
// values.
func (d *DataTransfer) Files() []js.Value {
	files := d.Value.Get("files")
	out := make([]js.Value, files.Length())
	for i := range out {
		out[i] = files.Index(i)
	}
	return out
}

// DragEvent wraps a drag/dragover/drop DOM event with its
// DataTransfer already unwrapped.
type DragEvent struct {
	Value        js.Value
	DataTransfer *DataTransfer
}

func newDragEvent(v js.Value) DragEvent {
	return DragEvent{Value: v, DataTransfer: &DataTransfer{Value: v.Get("dataTransfer")}}
}

func (e DragEvent) PreventDefault()  { e.Value.Call("preventDefault") }
func (e DragEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// SetDraggable marks (or unmarks) e as a drag source.
func (e *Element) SetDraggable(draggable bool) { e.Element.Set("draggable", draggable) }

// OnDragStart, OnDragOver, and OnDrop register the three listeners a
// drag-and-drop interaction needs, unwrapping each event's
// DataTransfer for fn. All return the underlying js.Func, to pass to
// RemoveEventListener later if needed.
func (e *Element) OnDragStart(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("dragstart", fn)
}

func (e *Element) OnDragOver(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("dragover", fn)
}

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
