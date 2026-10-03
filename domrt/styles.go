//go:build js && wasm

package domrt

import "github.com/elaous/gojiberry/gox"

// InstallStyles adds a <style data-gox-style="..."> element to the
// document's <head> for every stylesheet registered by a CSS import
// (see gox.RegisterStyle) that is not there yet -- including ones the
// server already rendered into the page, which it leaves alone. Mount
// calls it, so a program that mounts components has their styles; call
// it yourself if you register sheets after mounting.
func InstallStyles() {
	sheets := gox.RegisteredStyles()
	if len(sheets) == 0 {
		return
	}
	doc := document()
	head := doc.Get("head")
	have := map[string]bool{}
	existing := doc.Call("querySelectorAll", "style[data-gox-style]")
	for i := 0; i < existing.Get("length").Int(); i++ {
		have[existing.Call("item", i).Call("getAttribute", "data-gox-style").String()] = true
	}
	for _, s := range sheets {
		if have[s.ID] {
			continue
		}
		el := doc.Call("createElement", "style")
		el.Call("setAttribute", "data-gox-style", s.ID)
		el.Set("textContent", s.CSS)
		head.Call("appendChild", el)
	}
}
