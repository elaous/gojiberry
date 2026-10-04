package xango

import (
	"fmt"
	"html"
	"io"
	"strings"
)

// voidElements never have a closing tag or children per the HTML spec.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// Render writes n as HTML to w. Text content and attribute values are
// escaped automatically; use Raw to inject pre-trusted HTML deliberately.
func Render(w io.Writer, n Node) error {
	rw := renderWriter{w: w}
	rw.render(n)
	return rw.err
}

// RenderString is a convenience wrapper around Render for tests and
// callers that don't have an io.Writer handy.
func RenderString(n Node) (string, error) {
	var b strings.Builder
	if err := Render(&b, n); err != nil {
		return "", err
	}
	return b.String(), nil
}

type renderWriter struct {
	w   io.Writer
	err error
}

func (rw *renderWriter) writeString(s string) {
	if rw.err != nil {
		return
	}
	_, rw.err = io.WriteString(rw.w, s)
}

func (rw *renderWriter) render(n Node) {
	if rw.err != nil {
		return
	}
	switch n.Kind {
	case KindText:
		rw.writeString(html.EscapeString(n.Text))
	case KindRaw:
		rw.writeString(n.Text)
	case KindFragment:
		for _, c := range n.Children {
			rw.render(c)
		}
	case KindElement:
		rw.renderElement(n)
	case KindHost:
		rw.err = fmt.Errorf("xango: host node %q has no HTML form; render it with its own renderer", n.Tag)
	default:
		rw.err = fmt.Errorf("xango: unknown node kind %d", n.Kind)
	}
}

func (rw *renderWriter) renderElement(n Node) {
	rw.writeString("<")
	rw.writeString(n.Tag)
	for _, a := range n.Attrs {
		if a.Handler != nil {
			// Event bindings are inert in server-side HTML; the client
			// runtime (domrt) wires the real listener after hydration.
			continue
		}
		rw.writeString(" ")
		rw.writeString(html.EscapeString(a.Name))
		rw.writeString(`="`)
		rw.writeString(html.EscapeString(a.Value))
		rw.writeString(`"`)
	}
	rw.writeString(">")
	if voidElements[n.Tag] {
		return
	}
	for _, c := range n.Children {
		rw.render(c)
	}
	rw.writeString("</")
	rw.writeString(n.Tag)
	rw.writeString(">")
}

// Raw constructs a text node whose content is emitted verbatim, without
// HTML-escaping. Callers are responsible for ensuring the content is safe;
// this is the equivalent of dangerouslySetInnerHTML.
func Raw(html string) Node {
	return Node{Kind: KindRaw, Text: html}
}
