//go:build js && wasm

package canvas

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt/html"
)

var (
	errNotCanvas = errors.New("canvas: element is not a <canvas>")
	errNoContext = errors.New("canvas: browser gave no 2D context")
)

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

// URL is the load address, including data: URLs.
func (i *HTMLImage) URL() string { return i.url }

// Size implements ImageSource.
func (i *HTMLImage) Size() (w, h float64) {
	return i.v.Get("naturalWidth").Float(), i.v.Get("naturalHeight").Float()
}

func (i *HTMLImage) Pixels(maxSide int) (rgba []byte, w, h int, ok bool) {
	nw, nh := i.Size()
	if nw <= 0 || nh <= 0 || maxSide <= 0 {
		return nil, 0, 0, false
	}
	k := float64(maxSide) / max(nw, nh)
	if k > 1 {
		k = 1
	}
	w, h = max(int(nw*k), 1), max(int(nh*k), 1)
	doc := js.Global().Get("document")
	cv := doc.Call("createElement", "canvas")
	cv.Set("width", w)
	cv.Set("height", h)
	ctx := cv.Call("getContext", "2d")
	if !ctx.Truthy() {
		return nil, 0, 0, false
	}
	ok = true
	func() {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		ctx.Call("drawImage", i.v, 0, 0, w, h)
		data := ctx.Call("getImageData", 0, 0, w, h).Get("data")
		rgba = make([]byte, w*h*4)
		js.CopyBytesToGo(rgba, data)
	}()
	return rgba, w, h, ok
}

func (i *HTMLImage) PNG(maxSide int) (png []byte, ok bool) {
	nw, nh := i.Size()
	if nw <= 0 || nh <= 0 || maxSide <= 0 {
		return nil, false
	}
	k := min(float64(maxSide)/max(nw, nh), 1)
	w, h := max(int(nw*k), 1), max(int(nh*k), 1)
	cv := js.Global().Get("document").Call("createElement", "canvas")
	cv.Set("width", w)
	cv.Set("height", h)
	ctx := cv.Call("getContext", "2d")
	if !ctx.Truthy() {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			png, ok = nil, false
		}
	}()
	ctx.Call("drawImage", i.v, 0, 0, w, h)
	url := cv.Call("toDataURL", "image/png").String()
	_, b64, found := strings.Cut(url, ",")
	if !found {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, false
	}
	return data, true
}

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

	focusByPointer := false
	focusInput := func() {
		focusByPointer = true
		input.Call("focus", map[string]any{"preventScroll": true})
		focusByPointer = false
	}
	canvasEl.OnMouseDown(func(e html.MouseEvent) {
		if e.Button != 0 {
			return
		}
		e.PreventDefault()
		x, y := local(e)
		s.MouseDownMods(x, y, Mods{Ctrl: e.CtrlKey, Shift: e.ShiftKey, Alt: e.AltKey, Meta: e.MetaKey})
		if e.Detail == 2 { // the second press of a double click
			s.DoubleClick(x, y)
		}
		if s.WantsFocus() {
			focusInput()
		}
		redraw()
	})
	canvasEl.OnMouseMove(func(e html.MouseEvent) {
		x, y := local(e)
		if s.MouseMove(x, y) {
			redraw()
		}
	})
	up := js.Global()
	up.Call("addEventListener", "mouseup", js.FuncOf(func(_ js.Value, args []js.Value) any {
		r := canvasEl.BoundingRect()
		ev := args[0]
		if s.MouseUp(ev.Get("clientX").Float()-r.Left, ev.Get("clientY").Float()-r.Top) {
			if s.WantsFocus() {
				focusInput()
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

	composing := false
	insert := func(t string) {
		if t != "" && s.InsertText(t) {
			redraw()
		}
	}
	place := func() {
		x, y, h, ok := s.CompositionAnchor()
		if !ok {
			return
		}
		r := canvasEl.BoundingRect()
		st := input.Get("style")
		st.Set("left", strconv.FormatFloat(r.Left+x, 'f', 1, 64)+"px")
		st.Set("top", strconv.FormatFloat(r.Top+y+h, 'f', 1, 64)+"px")
	}
	showPreedit := func(t string) {
		if s.SetComposition(t) {
			place()
			redraw()
		}
	}
	input.AddEventListener("focus", js.FuncOf(func(js.Value, []js.Value) any {
		if !focusByPointer && s.FocusIn() {
			redraw()
		}
		return nil
	}))
	input.AddEventListener("compositionstart", js.FuncOf(func(js.Value, []js.Value) any {
		composing = true
		showPreedit(input.GetValue())
		return nil
	}))
	input.AddEventListener("compositionupdate", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if d := args[0].Get("data"); d.Type() == js.TypeString {
			showPreedit(d.String())
		}
		return nil
	}))
	input.AddEventListener("compositionend", js.FuncOf(func(_ js.Value, args []js.Value) any {
		composing = false
		s.SetComposition("")
		st := input.Get("style")
		st.Call("removeProperty", "left")
		st.Call("removeProperty", "top")
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
		if composing || e.Value.Get("isComposing").Bool() || e.Value.Get("keyCode").Int() == 229 {
			return
		}
		if e.CtrlKey || e.MetaKey || e.AltKey {
			key := e.Key
			if strings.HasPrefix(e.Code, "Key") && len(e.Code) == 4 {
				key = e.Code[3:]
			}
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
