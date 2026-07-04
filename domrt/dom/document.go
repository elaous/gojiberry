//go:build js && wasm

package dom

import (
	"syscall/js"
)

type Document struct {
	Document js.Value
}

// wrapAll wraps a DOM collection (NodeList/HTMLCollection) as a
// []Element.
func wrapAll(v js.Value) []Element {
	result := make([]Element, v.Length())
	for i := range result {
		result[i] = Element{Element: v.Index(i)}
	}
	return result
}

func (d *Document) GetElementsByTagName(tag string) []Element {
	return wrapAll(d.Document.Call("getElementsByTagName", tag))
}

func (d *Document) GetElementsByClassName(class string) []Element {
	return wrapAll(d.Document.Call("getElementsByClassName", class))
}

func (d *Document) GetElementsByName(name string) []Element {
	return wrapAll(d.Document.Call("getElementsByName", name))
}

// GetElementsByTagNameNS looks up elements by namespace and tag (e.g.
// SVG's "http://www.w3.org/2000/svg"). Unlike GetElementsBy{Tag,Class}Name,
// there is no non-namespaced getElementsByClassNameNS/getElementsByNameNS
// in the DOM -- those aren't real browser APIs, so this package doesn't
// pretend to wrap them.
func (d *Document) GetElementsByTagNameNS(namespace, tag string) []Element {
	return wrapAll(d.Document.Call("getElementsByTagNameNS", namespace, tag))
}

func (d *Document) GetElementByID(id string) *Element {
	return &Element{Element: d.Document.Call("getElementById", id)}
}

func (d *Document) CreateElement(tag string) *Element {
	return &Element{Element: d.Document.Call("createElement", tag)}
}

func (d *Document) CreateTextNode(text string) *Element {
	return &Element{Element: d.Document.Call("createTextNode", text)}
}

// Get returns a named property of document not covered by a typed
// getter above (e.g. Get("title"), Get("readyState")).
func (d *Document) Get(name string) js.Value {
	return d.Document.Get(name)
}

// Call invokes a document method not covered by a typed wrapper above.
func (d *Document) Call(method string, args ...any) js.Value {
	return d.Document.Call(method, args...)
}

func GetDocument() *Document {
	return &Document{Document: js.Global().Get("document")}
}
