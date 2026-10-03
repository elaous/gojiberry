//go:build js && wasm

package fonts

import (
	"errors"
	"syscall/js"
)

// LoadFace registers a font file (ttf, otf, woff, woff2) with the browser
// as family at the variant's weight and style, so canvas text drawn in
// it uses it. done, if set, is called when it has loaded (or failed), on
// the browser's event loop; redraw the canvas there.
func LoadFace(family string, v Variant, data []byte, done func(error)) {
	finish := func(err error) {
		if done != nil {
			done(err)
		}
	}
	ctor := js.Global().Get("FontFace")
	if !ctor.Truthy() {
		finish(errors.New("fonts: this browser has no FontFace"))
		return
	}
	buf := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(buf, data)
	style := "normal"
	if v.Italic {
		style = "italic"
	}
	weight := v.Weight
	if weight == 0 {
		weight = 400
	}
	face := ctor.New(family, buf, map[string]any{"weight": weight, "style": style})
	var ok, fail js.Func
	release := func() { ok.Release(); fail.Release() }
	ok = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		js.Global().Get("document").Get("fonts").Call("add", args[0])
		finish(nil)
		return nil
	})
	fail = js.FuncOf(func(_ js.Value, args []js.Value) any {
		release()
		finish(errors.New("fonts: the browser could not read the font: " + args[0].Call("toString").String()))
		return nil
	})
	face.Call("load").Call("then", ok, fail)
}
