//go:build js && wasm

package fonts

import (
	"errors"
	"syscall/js"
)

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
