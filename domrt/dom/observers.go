//go:build js && wasm

package dom

import "syscall/js"

// IntersectionEntry reports one observed element's visibility change.
type IntersectionEntry struct {
	Target            *Element
	IsIntersecting    bool
	IntersectionRatio float64
}

// IntersectionObserver wraps the browser's IntersectionObserver:
// fires fn whenever an observed element's visibility relative to its
// viewport/root crosses a threshold.
type IntersectionObserver struct {
	Value    js.Value
	callback js.Func
}

// NewIntersectionObserver constructs an observer that calls fn with
// every batch of visibility changes. Call Observe to start watching
// elements, and Disconnect (which also releases fn's callback) when
// done with it.
func NewIntersectionObserver(fn func(entries []IntersectionEntry)) *IntersectionObserver {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		jsEntries := args[0]
		out := make([]IntersectionEntry, jsEntries.Length())
		for i := range out {
			e := jsEntries.Index(i)
			out[i] = IntersectionEntry{
				Target:            &Element{Element: e.Get("target")},
				IsIntersecting:    e.Get("isIntersecting").Bool(),
				IntersectionRatio: e.Get("intersectionRatio").Float(),
			}
		}
		fn(out)
		return nil
	})
	ctor := js.Global().Get("IntersectionObserver").New(cb)
	return &IntersectionObserver{Value: ctor, callback: cb}
}

func (o *IntersectionObserver) Observe(el *Element)   { o.Value.Call("observe", el.Element) }
func (o *IntersectionObserver) Unobserve(el *Element) { o.Value.Call("unobserve", el.Element) }

// Disconnect stops observing every element and releases the callback
// registered at construction.
func (o *IntersectionObserver) Disconnect() {
	o.Value.Call("disconnect")
	o.callback.Release()
}

// ResizeEntry reports one observed element's new content-box size.
type ResizeEntry struct {
	Target        *Element
	Width, Height float64
}

// ResizeObserver wraps the browser's ResizeObserver: fires fn
// whenever an observed element's size changes.
type ResizeObserver struct {
	Value    js.Value
	callback js.Func
}

// NewResizeObserver constructs an observer that calls fn with every
// batch of size changes. Call Observe to start watching elements, and
// Disconnect (which also releases fn's callback) when done with it.
func NewResizeObserver(fn func(entries []ResizeEntry)) *ResizeObserver {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		jsEntries := args[0]
		out := make([]ResizeEntry, jsEntries.Length())
		for i := range out {
			e := jsEntries.Index(i)
			rect := e.Get("contentRect")
			out[i] = ResizeEntry{
				Target: &Element{Element: e.Get("target")},
				Width:  rect.Get("width").Float(),
				Height: rect.Get("height").Float(),
			}
		}
		fn(out)
		return nil
	})
	ctor := js.Global().Get("ResizeObserver").New(cb)
	return &ResizeObserver{Value: ctor, callback: cb}
}

func (o *ResizeObserver) Observe(el *Element)   { o.Value.Call("observe", el.Element) }
func (o *ResizeObserver) Unobserve(el *Element) { o.Value.Call("unobserve", el.Element) }

// Disconnect stops observing every element and releases the callback
// registered at construction.
func (o *ResizeObserver) Disconnect() {
	o.Value.Call("disconnect")
	o.callback.Release()
}
