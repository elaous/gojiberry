//go:build js && wasm

// Package svg helps create SVG elements: createElement alone produces
// an element in the wrong namespace, and SVG won't render it. Once
// created, an SVG element is a plain *html.Element like any other --
// presentation attributes (d, cx, cy, r, viewBox, fill, stroke, ...)
// are ordinary attributes, so html.Element's GetAttribute/SetAttribute
// already cover them, and its BoundingRect/Style/event-listener
// methods work the same on SVG elements as on HTML ones. The one
// caveat is className: on an SVG element it's an SVGAnimatedString
// object, not a plain string, so use html.Element's
// ClassListAdd/ClassListRemove/ClassListToggle/ClassListContains
// there instead of GetClassName/SetClassName.
package svg

import "github.com/elaous/gojiberry/domrt/html"

// Namespace is the namespace URI for SVG elements, for use with
// html.Document.CreateElementNS (or CreateElement, which already passes it).
const Namespace = "http://www.w3.org/2000/svg"

// CreateElement creates a new, unattached SVG element (e.g. "svg",
// "path", "circle", "rect", "g") in document.
func CreateElement(document *html.Document, tag string) *html.Element {
	return document.CreateElementNS(Namespace, tag)
}
