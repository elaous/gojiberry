//go:build js && wasm

package domrt

import "github.com/elaous/gojiberry/xango"

// InstallStyles adds a style element to the document head.
func InstallStyles() {
	sheets := xango.RegisteredStyles()
	if len(sheets) == 0 {
		return
	}
	doc := document()
	head := doc.Get("head")
	have := map[string]bool{}
	existing := doc.Call("querySelectorAll", "style[data-xango-style]")
	for i := 0; i < existing.Get("length").Int(); i++ {
		have[existing.Call("item", i).Call("getAttribute", "data-xango-style").String()] = true
	}
	for _, s := range sheets {
		if have[s.ID] {
			continue
		}
		el := doc.Call("createElement", "style")
		el.Call("setAttribute", "data-xango-style", s.ID)
		el.Set("textContent", s.CSS)
		head.Call("appendChild", el)
	}
}
