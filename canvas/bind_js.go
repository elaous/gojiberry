//go:build js && wasm

package canvas

import (
	"errors"
	"strings"
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

// ClipRect implements ImagePainter.
func (p jsPainter) ClipRect(x, y, w, h float64) {
	p.BeginPath()
	p.Rect(x, y, w, h)
	p.Clip()
}

// DrawImage implements ImagePainter; src must come from LoadImage.
func (p jsPainter) DrawImage(src ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64) {
	if img, ok := src.(*HTMLImage); ok {
		p.DrawImageClipped(img.v, sx, sy, sw, sh, dx, dy, dw, dh)
	}
}

// HTMLImage is an ImageSource backed by a loaded HTMLImageElement.
type HTMLImage struct {
	v   js.Value
	url string
}

// URL is the address the image was loaded from (a data: URL included), so a
// document that holds it can be saved and the image fetched again on load.
func (i *HTMLImage) URL() string { return i.url }

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
		onLoad(&HTMLImage{v: el, url: url})
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
// 1px, position: fixed at the top-left so the browser never scrolls to
// it while you type). Bind returns an error if canvasEl is
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
	s.Redraw = redraw
	s.Prompt = func(message, initial string) (string, bool) {
		v := js.Global().Call("prompt", message, initial)
		if v.IsNull() || v.IsUndefined() {
			return "", false
		}
		return v.String(), true
	}
	s.OpenLink = func(url string) { js.Global().Call("open", url, "_blank", "noopener") }
	// The async clipboard API (needs a secure context and, for reading,
	// the user's permission; without it the commands do nothing).
	if cb := js.Global().Get("navigator").Get("clipboard"); cb.Truthy() {
		s.Clipboard = Clipboard{
			Write: func(text string) { cb.Call("writeText", text) },
			Read: func(fn func(string)) {
				var then js.Func
				then = js.FuncOf(func(_ js.Value, args []js.Value) any {
					fn(args[0].String())
					then.Release()
					return nil
				})
				cb.Call("readText").Call("then", then)
			},
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
		s.MouseDownMods(x, y, Mods{Ctrl: e.CtrlKey, Shift: e.ShiftKey, Alt: e.AltKey, Meta: e.MetaKey})
		if e.Detail == 2 { // the second press of a double click
			s.DoubleClick(x, y)
		}
		// The input needs focus to receive keys: for typing into a
		// cell, and for arrow/Enter/Escape navigation of a menu.
		if s.WantsFocus() {
			// preventScroll: focusing the (offscreen) input must not make the
			// page jump.
			input.Call("focus", map[string]any{"preventScroll": true})
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
			// A drag-selection only has something selected once it ends.
			if s.WantsFocus() {
				input.Call("focus", map[string]any{"preventScroll": true})
			}
			redraw()
		}
		return nil
	}))
	canvasEl.OnWheel(func(e html.WheelEvent) {
		x, y := local(html.MouseEvent{X: mouseX(e.Value), Y: mouseY(e.Value)})
		if s.MouseWheel(x, y, e.DeltaX, e.DeltaY) {
			e.PreventDefault()
			redraw()
		}
	})
	canvasEl.AddEventListener("mouseleave", js.FuncOf(func(js.Value, []js.Value) any {
		if s.MouseLeave() {
			redraw()
		}
		return nil
	}))

	// Composition (an input method editor: Chinese, Japanese, Korean, accents by dead keys): while the person
	// is composing, the input holds the preedit text and must be left alone; clearing it would end the
	// composition. The text is inserted once, when the composition ends. Browsers differ in the order of the
	// last "input" and "compositionend", so the text is taken from the input (or the event) at the end and
	// the input is emptied, which leaves a later "input" with nothing to insert.
	composing := false
	insert := func(t string) {
		if t != "" && s.InsertText(t) {
			redraw()
		}
	}
	input.AddEventListener("compositionstart", js.FuncOf(func(js.Value, []js.Value) any {
		composing = true
		return nil
	}))
	input.AddEventListener("compositionend", js.FuncOf(func(_ js.Value, args []js.Value) any {
		composing = false
		t := input.GetValue()
		if t == "" && len(args) > 0 {
			if d := args[0].Get("data"); d.Type() == js.TypeString {
				t = d.String()
			}
		}
		input.SetValue("")
		insert(t)
		return nil
	}))
	input.AddEventListener("input", js.FuncOf(func(js.Value, []js.Value) any {
		if composing {
			return nil
		}
		if t := input.GetValue(); t != "" {
			input.SetValue("")
			insert(t)
		}
		return nil
	}))
	input.OnKeyDown(func(e html.KeyEvent) {
		// While an input method is composing, its keys (Enter to choose a candidate, the arrows, Backspace in
		// the preedit) are the method's, not the editor's. keyCode 229 is what browsers send for them.
		if composing || e.Value.Get("isComposing").Bool() || e.Value.Get("keyCode").Int() == 229 {
			return
		}
		if e.CtrlKey || e.MetaKey || e.AltKey {
			key := e.Key
			// Alt changes the character on some layouts; the physical key
			// is what a shortcut means.
			if strings.HasPrefix(e.Code, "Key") && len(e.Code) == 4 {
				key = e.Code[3:]
			}
			// With Shift, a digit key types a symbol (Shift+8 is "*").
			if strings.HasPrefix(e.Code, "Digit") && len(e.Code) == 6 {
				key = e.Code[5:]
			}
			if s.HandleChord(Chord(key, e.CtrlKey, e.ShiftKey, e.AltKey, e.MetaKey)) {
				e.PreventDefault()
				redraw()
			}
			return
		}
		if s.HandleKey(e.Key, e.ShiftKey) {
			e.PreventDefault()
			redraw()
		}
	})

	// The system clipboard, through the browser's own events (which is
	// how a page is allowed to use it): copy and cut put the selection on
	// it, paste inserts from it.
	clip := func(args []js.Value) js.Value { return args[0].Get("clipboardData") }
	input.AddEventListener("copy", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if text, ok := s.Copy(); ok {
			clip(args).Call("setData", "text/plain", text)
			args[0].Call("preventDefault")
		}
		return nil
	}))
	input.AddEventListener("cut", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if text, ok := s.Cut(); ok {
			clip(args).Call("setData", "text/plain", text)
			args[0].Call("preventDefault")
			redraw()
		}
		return nil
	}))
	input.AddEventListener("paste", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if s.Paste(clip(args).Call("getData", "text/plain").String()) {
			args[0].Call("preventDefault")
			redraw()
		}
		return nil
	}))

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

func mouseX(ev js.Value) float64 { return ev.Get("clientX").Float() }
func mouseY(ev js.Value) float64 { return ev.Get("clientY").Float() }
