package xango

import (
	"regexp"
	"strings"
)

// Document is the <html><head>...</head><body>...</body></html>
// wrapper almost every top-level page needs, so individual pages don't
// each hand-roll that boilerplate. Head holds extra <head> children
// (e.g. a Style-injected stylesheet, <meta>, <link>); Body is the
// single node rendered inside <body> -- use Frag to group multiple
// top-level children there.
//
// The stylesheets registered by CSS imports (see RegisterStyle) go in the
// head first, ahead of Head, so a page's own styles can override them.
type Document struct {
	Title string
	Head  []Node
	Body  Node
	// NoStyles leaves the registered stylesheets out.
	NoStyles bool
	// Styles, if not nil, limits the registered stylesheets to these: an
	// entry is a sheet's full ID or the end of it ("components/button.css",
	// "button.css"). An empty, non-nil slice means none -- the per-page
	// choice (see the @styles page annotation).
	Styles []string
}

// styleWanted reports whether a sheet with this ID is in the list.
func styleWanted(id string, only []string) bool {
	if only == nil {
		return true
	}
	for _, o := range only {
		if id == o || strings.HasSuffix(id, "/"+o) {
			return true
		}
	}
	return false
}

// Render assembles the document into a single <html> Node tree.
func (d Document) Render() Node {
	head := make([]Node, 0, len(d.Head)+1)
	if d.Title != "" {
		head = append(head, El("title", nil, Text(d.Title)))
	}
	if !d.NoStyles {
		for _, s := range RegisteredStyles() {
			if styleWanted(s.ID, d.Styles) {
				head = append(head, El("style", []Attr{A("data-xango-style", s.ID)}, Raw(styleEnd.ReplaceAllString(s.CSS, `<\/$1`))))
			}
		}
	}
	head = append(head, d.Head...)
	return El("html", nil,
		El("head", nil, head...),
		El("body", nil, d.Body),
	)
}

// Style returns a <style> element injecting css verbatim -- e.g. the
// string an xango CSS resource import binds -- for use in a Document's
// Head. css is trusted content, but a "</style" inside it (in a string or a
// comment) cannot end the element early: it is written as the equivalent
// CSS escape "<\/style".
func Style(css string) Node {
	return El("style", nil, Raw(styleEnd.ReplaceAllString(css, `<\/$1`)))
}

var styleEnd = regexp.MustCompile(`(?i)<\/(style)`)
