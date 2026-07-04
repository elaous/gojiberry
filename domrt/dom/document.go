//go:build js && wasm

package dom

import (
	"syscall/js"
)


type Document struct {
	Document js.Value
}

func (d *Document) GetElementsByTagName(tag string) []Element {
	elements := d.Document.Call("getElementsByTagName", tag)
	result := make([]Element, elements.Length())
	for i := 0; i < elements.Length(); i++ {
		result[i] = Element{Element: elements.Index(i)}
	}
	return result
}

func (d *Document) GetElementsByClassName(class string) []Element {
	elements := d.Document.Call("getElementsByClassName", class)
	result := make([]Element, elements.Length())
	for i := 0; i < elements.Length(); i++ {
		result[i] = Element{Element: elements.Index(i)}
	}
	return result
}

func (d *Document) GetElementsByName(name string) []Element {
	elements := d.Document.Call("getElementsByName", name)
	result := make([]Element, elements.Length())
	for i := 0; i < elements.Length(); i++ {
		result[i] = Element{Element: elements.Index(i)}
	}
	return result
}

func (d *Document) GetElementsByTagNameNS(namespace, tag string) []Element {
	elements := d.Document.Call("getElementsByTagNameNS", namespace, tag)
	result := make([]Element, elements.Length())
	for i := 0; i < elements.Length(); i++ {
		result[i] = Element{Element: elements.Index(i)}
	}
	return result
}

func (d *Document) GetElementsByClassNameNS(namespace, class string) []Element {
	elements := d.Document.Call("getElementsByClassNameNS", namespace, class)
	result := make([]Element, elements.Length())
	for i := 0; i < elements.Length(); i++ {
		result[i] = Element{Element: elements.Index(i)}
	}
	return result
}

func (d *Document) GetElementsByNameNS(namespace, name string) []Element {
	elements := d.Document.Call("getElementsByNameNS", namespace, name)
	result := make([]Element, elements.Length())
	for i := 0; i < elements.Length(); i++ {
		result[i] = Element{Element: elements.Index(i)}
	}
	return result
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

func GetDocument() *Document {
	return &Document{Document: js.Global().Get("document")}
}
