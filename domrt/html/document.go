//go:build js && wasm

package html

import (
	"syscall/js"
)

// Document wraps the browser's global document object. Get/Call cover
// anything without a typed method below, the same escape hatch as the
// rest of this package.
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

// GetElementsByTagName returns every element with the given tag name.
func (d *Document) GetElementsByTagName(tag string) []Element {
	return wrapAll(d.Document.Call("getElementsByTagName", tag))
}

// GetElementsByClassName returns every element with the given class.
func (d *Document) GetElementsByClassName(class string) []Element {
	return wrapAll(d.Document.Call("getElementsByClassName", class))
}

// GetElementsByName returns every element with the given name attribute.
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

// GetElementByID returns the element with the given id.
func (d *Document) GetElementByID(id string) *Element {
	return &Element{Element: d.Document.Call("getElementById", id)}
}

// QuerySelector returns the first element in the document matching the
// CSS selector, in document order, or nil when nothing matches. See
// Element.QuerySelector for the subtree-scoped variant.
func (d *Document) QuerySelector(selector string) *Element {
	v := d.Document.Call("querySelector", selector)
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	return &Element{Element: v}
}

// QuerySelectorAll returns every element in the document matching the
// CSS selector, in document order -- empty when nothing matches.
func (d *Document) QuerySelectorAll(selector string) []Element {
	return wrapAll(d.Document.Call("querySelectorAll", selector))
}

// CreateElement creates a new, unattached element with the given tag name.
func (d *Document) CreateElement(tag string) *Element {
	return &Element{Element: d.Document.Call("createElement", tag)}
}

// CreateElementNS creates a new, unattached element in the given
// namespace (e.g. svg.Namespace) with the given tag name -- required
// for SVG/MathML elements, which createElement alone cannot make. See
// domrt/svg's CreateElement for a convenience wrapper.
func (d *Document) CreateElementNS(namespace, tag string) *Element {
	return &Element{Element: d.Document.Call("createElementNS", namespace, tag)}
}

// DocumentElement returns the root <html> element.
func (d *Document) DocumentElement() *Element {
	return &Element{Element: d.Document.Get("documentElement")}
}

// Body returns the <body> element.
func (d *Document) Body() *Element {
	return &Element{Element: d.Document.Get("body")}
}

// CreateTextNode creates a new, unattached text node with the given content.
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

// GetDocument returns the global document object.
func GetDocument() *Document {
	return &Document{Document: js.Global().Get("document")}
}
