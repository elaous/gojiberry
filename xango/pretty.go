package xango

import (
	"html"
	"strings"
)

var blockTags = map[string]bool{
	"div": true, "section": true, "header": true, "footer": true,
	"nav": true, "main": true, "article": true, "aside": true,
	"ul": true, "ol": true, "li": true, "dl": true, "dt": true, "dd": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"form": true, "fieldset": true, "figure": true, "figcaption": true,
	"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "details": true, "summary": true,
}

func isBlockLevel(n Node) bool {
	return n.Kind == KindElement && blockTags[n.Tag]
}

// a newline between two inline siblings renders as a visible space
func canBreakBoundary(a, b Node) bool {
	return isBlockLevel(a) || isBlockLevel(b)
}

// RenderIndented is Render's pretty-printing counterpart.
func RenderIndented(n Node) (string, error) {
	var b strings.Builder
	pw := &prettyWriter{b: &b}
	pw.render(n, 0)
	return b.String(), pw.err
}

type prettyWriter struct {
	b   *strings.Builder
	err error
}

func hasTextOrRawChild(children []Node) bool {
	for _, c := range children {
		if c.Kind == KindText || c.Kind == KindRaw {
			return true
		}
	}
	return false
}

func (pw *prettyWriter) render(n Node, depth int) {
	if pw.err != nil {
		return
	}
	if (n.Kind != KindElement && n.Kind != KindFragment) ||
		len(n.Children) == 0 || hasTextOrRawChild(n.Children) {
		s, err := RenderString(n)
		if err != nil {
			pw.err = err
			return
		}
		pw.b.WriteString(s)
		return
	}
	if n.Kind == KindFragment {
		pw.renderChildren(n.Children, depth)
		return
	}
	pw.writeOpenTag(n)
	pw.newline(depth + 1)
	pw.renderChildren(n.Children, depth+1)
	pw.newline(depth)
	pw.b.WriteString("</")
	pw.b.WriteString(n.Tag)
	pw.b.WriteString(">")
}

func (pw *prettyWriter) renderChildren(children []Node, depth int) {
	for i, c := range children {
		if i > 0 && canBreakBoundary(children[i-1], c) {
			pw.newline(depth)
		}
		pw.render(c, depth)
	}
}

func (pw *prettyWriter) newline(depth int) {
	pw.b.WriteString("\n")
	pw.b.WriteString(strings.Repeat("  ", depth))
}

func (pw *prettyWriter) writeOpenTag(n Node) {
	pw.b.WriteString("<")
	pw.b.WriteString(n.Tag)
	for _, a := range n.Attrs {
		if a.Handler != nil {
			continue
		}
		pw.b.WriteString(" ")
		pw.b.WriteString(html.EscapeString(a.Name))
		pw.b.WriteString(`="`)
		pw.b.WriteString(html.EscapeString(a.Value))
		pw.b.WriteString(`"`)
	}
	pw.b.WriteString(">")
}
