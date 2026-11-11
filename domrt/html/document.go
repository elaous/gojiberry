//go:build js && wasm

package html

import (
	"syscall/js"
)

// Document wraps the browser's global document object.
type Document struct {
	Document js.Value
}

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

// GetElementsByTagNameNS looks up elements by namespace and tag name.
func (d *Document) GetElementsByTagNameNS(namespace, tag string) []Element {
	return wrapAll(d.Document.Call("getElementsByTagNameNS", namespace, tag))
}

// GetElementByID returns the element with the given id, or nil.
func (d *Document) GetElementByID(id string) *Element {
	v := d.Document.Call("getElementById", id)
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	return &Element{Element: v}
}

// QuerySelector returns the first element matching a CSS selector.
func (d *Document) QuerySelector(selector string) *Element {
	v := d.Document.Call("querySelector", selector)
	if v.IsNull() || v.IsUndefined() {
		return nil
	}
	return &Element{Element: v}
}

// QuerySelectorAll returns every element matching a CSS selector.
func (d *Document) QuerySelectorAll(selector string) []Element {
	return wrapAll(d.Document.Call("querySelectorAll", selector))
}

// CreateElement creates an unattached element.
func (d *Document) CreateElement(tag string) *Element {
	return &Element{Element: d.Document.Call("createElement", tag)}
}

// CreateElementNS creates an element in a namespace, such as SVG.
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

// CreateTextNode creates an unattached text node.
func (d *Document) CreateTextNode(text string) *Element {
	return &Element{Element: d.Document.Call("createTextNode", text)}
}

// Get returns a named document property.
func (d *Document) Get(name string) js.Value {
	return d.Document.Get(name)
}

// Call invokes a document method.
func (d *Document) Call(method string, args ...any) js.Value {
	return d.Document.Call(method, args...)
}

// GetDocument returns the global document object.
func GetDocument() *Document {
	return &Document{Document: js.Global().Get("document")}
}
