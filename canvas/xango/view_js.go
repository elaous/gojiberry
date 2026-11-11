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

type View struct {
	Stage *canvas.Stage
	Root  *Root

	redraw   func()
	fallback js.Value
}

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

func (v *View) Render(n xg.Node, text string) error {
	v.fallback.Set("textContent", text)
	if err := v.Root.Render(n); err != nil {
		return err
	}
	v.redraw()
	return nil
}

func (v *View) Find(tag string, n int) any { return v.Root.Find(tag, n) }
