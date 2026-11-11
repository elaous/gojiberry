//go:build js && wasm

package svg

import "github.com/elaous/gojiberry/domrt/html"

// Namespace is the SVG namespace URI.
const Namespace = "http://www.w3.org/2000/svg"

// CreateElement creates an unattached SVG element.
func CreateElement(document *html.Document, tag string) *html.Element {
	return document.CreateElementNS(Namespace, tag)
}
