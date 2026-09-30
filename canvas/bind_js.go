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

// Bind wires v to a real <canvas> and a text <input> that captures
// keyboard input (so typing, paste and IME composition all behave
// natively): it sets v.Painter to the canvas's 2D context, registers
// the mouse and keyboard handlers, blinks the caret, and draws once.
//
// input must be focusable but can be visually hidden (e.g. opacity 0,
// 1px, absolutely positioned). Bind returns an error if canvasEl is not
// a <canvas> or has no 2D context.
func (v *TableView) Bind(canvasEl, input *html.Element) error {
	cv := canvasEl.AsCanvas()
	if cv == nil {
		return errNotCanvas
	}
	ctx := cv.GetContext2D()
	if ctx == nil {
		return errNoContext
	}
	v.Painter = ctx

	redraw := func() {
		ctx.ClearRect(0, 0, canvasEl.Get("width").Float(), canvasEl.Get("height").Float())
		v.Draw()
		cursor := "default"
		if v.mode == ModeEdit {
			switch v.hover.Kind {
			case HitAddColumn, HitAddRow:
				cursor = "pointer"
			case HitCell:
				cursor = "text"
			}
		}
		if _, _, ok := v.Active(); !ok {
			input.Call("blur")
		}
		canvasEl.SetStyleProperty("cursor", cursor)
	}
	local := func(e html.MouseEvent) (float64, float64) {
		r := canvasEl.BoundingRect()
		return e.X - r.Left, e.Y - r.Top
	}

	v.refresh = redraw

	canvasEl.OnMouseDown(func(e html.MouseEvent) {
		if v.mode != ModeEdit || e.Button != 0 {
			return
		}
		// The canvas isn't focusable; without this the browser
		// clears the input's focus once this handler returns.
		e.PreventDefault()
		x, y := local(e)
		v.MouseDown(x, y)
		if _, _, ok := v.Active(); ok {
			input.Focus()
		}
		redraw()
	})
	canvasEl.OnMouseMove(func(e html.MouseEvent) {
		x, y := local(e)
		if v.MouseMove(x, y) {
			redraw()
		}
	})
	canvasEl.AddEventListener("mouseleave", js.FuncOf(func(js.Value, []js.Value) any {
		if v.MouseLeave() {
			redraw()
		}
		return nil
	}))

	input.AddEventListener("input", js.FuncOf(func(js.Value, []js.Value) any {
		if s := input.GetValue(); s != "" {
			input.SetValue("")
			if v.InsertText(s) {
				redraw()
			}
		}
		return nil
	}))
	input.OnKeyDown(func(e html.KeyEvent) {
		if e.CtrlKey || e.MetaKey || e.AltKey {
			return
		}
		if v.HandleKey(e.Key, e.ShiftKey) {
			e.PreventDefault()
			redraw()
		}
	})

	// Blink the caret.
	var blink js.Func
	on := true
	blink = js.FuncOf(func(js.Value, []js.Value) any {
		on = !on
		if v.SetCaretVisible(on) {
			redraw()
		}
		return nil
	})
	js.Global().Call("setInterval", blink, 530)

	redraw()
	return nil
}
