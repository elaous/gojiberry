//go:build js && wasm

package dom

import "syscall/js"

// DataTransfer wraps a drag event's dataTransfer object, carrying
// whatever data a drag operation is moving and the drop/drag-effect
// negotiation between source and target.
type DataTransfer struct {
	Value js.Value
}

// SetData stores data under format (e.g. "text/plain") for the drop target to read.
func (d *DataTransfer) SetData(format, data string) { d.Value.Call("setData", format, data) }

// GetData retrieves the data stored under format by the drag source.
func (d *DataTransfer) GetData(format string) string { return d.Value.Call("getData", format).String() }

// DropEffect returns the drop effect the target will use (e.g. "copy", "move", "link", "none").
func (d *DataTransfer) DropEffect() string { return d.Value.Get("dropEffect").String() }

// SetDropEffect sets the drop effect the target will use.
func (d *DataTransfer) SetDropEffect(effect string) { d.Value.Set("dropEffect", effect) }

// EffectAllowed returns the set of drop effects the drag source permits.
func (d *DataTransfer) EffectAllowed() string { return d.Value.Get("effectAllowed").String() }

// SetEffectAllowed sets the drop effects the drag source permits (e.g. "copy", "copyMove", "all").
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

// PreventDefault suppresses the browser's default handling of the event
// (required on dragover for drop to fire at all).
func (e DragEvent) PreventDefault() { e.Value.Call("preventDefault") }

// StopPropagation stops the event from bubbling further.
func (e DragEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// SetDraggable marks (or unmarks) e as a drag source.
func (e *Element) SetDraggable(draggable bool) { e.Element.Set("draggable", draggable) }

// OnDragStart, OnDragOver, and OnDrop register the three listeners a
// drag-and-drop interaction needs, unwrapping each event's
// DataTransfer for fn. All return the underlying js.Func, to pass to
// RemoveEventListener later if needed.
// OnDragStart registers fn for the dragstart event (fired on the drag source).
func (e *Element) OnDragStart(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("dragstart", fn)
}

// OnDragOver registers fn for the dragover event (fired repeatedly on the drop target while hovering).
func (e *Element) OnDragOver(fn func(evt DragEvent)) js.Func {
	return e.onDragEvent("dragover", fn)
}

// OnDrop registers fn for the drop event (fired on the drop target on release).
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
