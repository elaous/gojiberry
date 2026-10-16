//go:build js && wasm

package xango

import (
	"errors"
	"fmt"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt/html"
	xg "github.com/elaous/gojiberry/xango"
)

// View is a canvas in an ordinary page with a canvas/xango tree drawn in it: the piece to reach for when a page built
// with DOM components (the xango tree that domrt mounts) needs a canvas of text inside it. It makes the <canvas>, the
// hidden input that takes the keys, and a visually hidden text copy of the document beside them, because a canvas
// is opaque to a screen reader and to a page that is read without the canvas (the copy is the text you give Render).
//
//	v, err := xango.NewView(container, 900, 600)
//	err = v.Render(xango.Canvas(xango.CanvasProps{...children...}), "the text of the document")
//
// Find reaches the components of the last tree, as Root.Find does.
type View struct {
	Stage *canvas.Stage
	Root  *Root

	redraw   func()
	fallback js.Value
}

// NewView appends a canvas of width x height pixels, its input and its text copy to parent, and binds a stage to them.
func NewView(parent *html.Element, width, height int) (*View, error) {
	if parent == nil {
		return nil, errors.New("canvas/xango: NewView needs a parent element")
	}
	doc := js.Global().Get("document")
	wrap := doc.Call("createElement", "div")
	wrap.Get("classList").Call("add", "gj-canvas-view")
	cv := doc.Call("createElement", "canvas")
	cv.Set("width", width)
	cv.Set("height", height)
	cv.Call("setAttribute", "role", "img")
	cv.Call("setAttribute", "aria-label", "Document canvas; the text of the document follows")
	in := doc.Call("createElement", "input")
	in.Call("setAttribute", "type", "text")
	in.Call("setAttribute", "autocomplete", "off")
	in.Call("setAttribute", "aria-label", "Document text input")
	in.Get("style").Set("cssText", "position:fixed;top:0;left:0;width:1px;height:1px;opacity:0")
	copyEl := doc.Call("createElement", "div")
	copyEl.Call("setAttribute", "class", "gj-sr-only")
	copyEl.Get("style").Set("cssText", "position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0 0 0 0);white-space:pre-wrap")
	for _, n := range []js.Value{cv, in, copyEl} {
		wrap.Call("appendChild", n)
	}
	parent.Element.Call("appendChild", wrap)

	stage := canvas.NewStage(nil, 0, 0)
	cvEl, inEl := &html.Element{Element: cv}, &html.Element{Element: in}
	redraw, err := stage.Bind(cvEl, inEl)
	if err != nil {
		return nil, fmt.Errorf("canvas/xango: %w", err)
	}
	root := NewRoot(stage)
	root.Redraw = redraw
	return &View{Stage: stage, Root: root, redraw: redraw, fallback: copyEl}, nil
}

// Render draws the tree n (a Canvas element) and sets the text copy beside the canvas.
func (v *View) Render(n xg.Node, text string) error {
	v.fallback.Set("textContent", text)
	if err := v.Root.Render(n); err != nil {
		return err
	}
	v.redraw()
	return nil
}

// Find returns the component for the n-th element with tag in the last tree (see Root.Find).
func (v *View) Find(tag string, n int) any { return v.Root.Find(tag, n) }
