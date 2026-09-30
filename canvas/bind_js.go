//go:build js && wasm

package canvas

import (
	"errors"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/html"
)

var (
	errNotCanvas = errors.New("canvas: element is not a <canvas>")
	errNoContext = errors.New("canvas: browser gave no 2D context")
)

// jsPainter adapts *html.Context2D to ImagePainter.
type jsPainter struct{ *html.Context2D }

// NewPainter adapts a canvas 2D context to ImagePainter.
func NewPainter(ctx *html.Context2D) ImagePainter { return jsPainter{ctx} }

// DrawImage implements ImagePainter; src must come from LoadImage.
func (p jsPainter) DrawImage(src ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64) {
	if img, ok := src.(*HTMLImage); ok {
		p.Context2D.DrawImageClipped(img.v, sx, sy, sw, sh, dx, dy, dw, dh)
	}
}

// HTMLImage is an ImageSource backed by a loaded HTMLImageElement.
type HTMLImage struct{ v js.Value }

// Size implements ImageSource.
func (i *HTMLImage) Size() (w, h float64) {
	return i.v.Get("naturalWidth").Float(), i.v.Get("naturalHeight").Float()
}

// LoadImage starts loading url (a normal or data: URL) and calls
// onLoad, on the browser's event loop, once it has decoded. onError is
// optional.
func LoadImage(url string, onLoad func(*HTMLImage), onError func()) {
	el := js.Global().Get("Image").New()
	var load, fail js.Func
	release := func() { load.Release(); fail.Release() }
	load = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		onLoad(&HTMLImage{v: el})
		return nil
	})
	fail = js.FuncOf(func(js.Value, []js.Value) any {
		release()
		if onError != nil {
			onError()
		}
		return nil
	})
	el.Call("addEventListener", "load", load)
	el.Call("addEventListener", "error", fail)
	el.Set("src", url)
}

// Bind wires the stage to a real <canvas> and a text <input> that
// captures keyboard input (so typing, paste and IME composition all
// behave natively): it gives every component the canvas's painter,
// registers mouse and keyboard handlers, blinks carets, sets the CSS
// cursor, and draws once. Width/Height default to the canvas's size.
//
// input must be focusable but can be visually hidden (e.g. opacity 0,
// 1px, absolutely positioned). Bind returns an error if canvasEl is
// not a <canvas> or has no 2D context.
//
// The returned function redraws the stage; call it after changing a
// component from outside (SetMode, editing a field, ...).
func (s *Stage) Bind(canvasEl, input *html.Element) (redraw func(), err error) {
	cv := canvasEl.AsCanvas()
	if cv == nil {
		return nil, errNotCanvas
	}
	ctx := cv.GetContext2D()
	if ctx == nil {
		return nil, errNoContext
	}
	s.SetPainter(NewPainter(ctx))
	s.Width, s.Height = canvasEl.Get("width").Float(), canvasEl.Get("height").Float()

	redraw = func() {
		s.Draw()
		canvasEl.SetStyleProperty("cursor", s.Cursor())
		if !s.WantsFocus() {
			input.Call("blur")
		}
	}
	local := func(e html.MouseEvent) (float64, float64) {
		r := canvasEl.BoundingRect()
		return e.X - r.Left, e.Y - r.Top
	}

	canvasEl.OnMouseDown(func(e html.MouseEvent) {
		if e.Button != 0 {
			return
		}
		// The canvas isn't focusable; without this the browser
		// clears the input's focus once this handler returns.
		e.PreventDefault()
		x, y := local(e)
		s.MouseDown(x, y)
		// The input needs focus to receive keys: for typing into a
		// cell, and for arrow/Enter/Escape navigation of a menu.
		if s.WantsFocus() {
			input.Focus()
		}
		redraw()
	})
	canvasEl.OnMouseMove(func(e html.MouseEvent) {
		x, y := local(e)
		if s.MouseMove(x, y) {
			redraw()
		}
	})
	// mouseup on the window so a drag that ends outside the canvas
	// still ends.
	up := js.Global()
	up.Call("addEventListener", "mouseup", js.FuncOf(func(_ js.Value, args []js.Value) any {
		r := canvasEl.BoundingRect()
		ev := args[0]
		if s.MouseUp(ev.Get("clientX").Float()-r.Left, ev.Get("clientY").Float()-r.Top) {
			redraw()
		}
		return nil
	}))
	canvasEl.AddEventListener("mouseleave", js.FuncOf(func(js.Value, []js.Value) any {
		if s.MouseLeave() {
			redraw()
		}
		return nil
	}))

	input.AddEventListener("input", js.FuncOf(func(js.Value, []js.Value) any {
		if t := input.GetValue(); t != "" {
			input.SetValue("")
			if s.InsertText(t) {
				redraw()
			}
		}
		return nil
	}))
	input.OnKeyDown(func(e html.KeyEvent) {
		if e.CtrlKey || e.MetaKey || e.AltKey {
			return
		}
		if s.HandleKey(e.Key, e.ShiftKey) {
			e.PreventDefault()
			redraw()
		}
	})

	// Blink carets.
	on := true
	blink := js.FuncOf(func(js.Value, []js.Value) any {
		on = !on
		if s.SetCaretVisible(on) {
			redraw()
		}
		return nil
	})
	js.Global().Call("setInterval", blink, 530)

	redraw()
	return redraw, nil
}

// Bind is shorthand for a single-component Stage: it wires v to a
// <canvas> and hidden <input> (see Stage.Bind), and makes Refresh work.
func (v *TableView) Bind(canvasEl, input *html.Element) error {
	st := NewStage(nil, 0, 0, v)
	redraw, err := st.Bind(canvasEl, input)
	if err != nil {
		return err
	}
	v.refresh = redraw
	return nil
}
