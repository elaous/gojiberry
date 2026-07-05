//go:build js && wasm

package html

import "syscall/js"

// ImageElement is an Element known to be an <img>.
type ImageElement struct{ Element }

// AsImage casts e to an ImageElement, or returns nil if e isn't an <img>.
func (e *Element) AsImage() *ImageElement {
	if e.GetTagName() != "IMG" {
		return nil
	}
	return &ImageElement{Element: *e}
}

// NaturalWidth returns the image's intrinsic width in pixels (0 until it loads).
func (i *ImageElement) NaturalWidth() int { return i.Get("naturalWidth").Int() }

// NaturalHeight returns the image's intrinsic height in pixels (0 until it loads).
func (i *ImageElement) NaturalHeight() int { return i.Get("naturalHeight").Int() }

// Complete returns whether the image has finished loading (successfully
// or not) or has no src set at all.
func (i *ImageElement) Complete() bool { return i.Get("complete").Bool() }

// Src returns the image's resolved (absolute) source URL.
func (i *ImageElement) Src() string { return i.Get("src").String() }

// SetSrc sets the image's source URL, starting a new load.
func (i *ImageElement) SetSrc(src string) { i.Set("src", src) }

// Alt returns the image's alternative text.
func (i *ImageElement) Alt() string { return i.Get("alt").String() }

// SetAlt sets the image's alternative text.
func (i *ImageElement) SetAlt(alt string) { i.Set("alt", alt) }

// OnLoad registers fn for the load event, which fires once the image
// finishes loading successfully.
func (i *ImageElement) OnLoad(fn func()) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any { fn(); return nil })
	i.AddEventListener("load", cb)
	return cb
}

// OnError registers fn for the error event, which fires if the image
// fails to load.
func (i *ImageElement) OnError(fn func()) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any { fn(); return nil })
	i.AddEventListener("error", cb)
	return cb
}
