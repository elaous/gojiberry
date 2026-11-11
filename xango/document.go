package xango

import (
	"regexp"
	"strings"
)

// CSS-import stylesheets go in the head before Head
type Document struct {
	Title string
	Head  []Node
	Body  Node
	// NoStyles leaves the registered stylesheets out.
	NoStyles bool
	// Styles limits the registered stylesheets: nil means all, empty means none.
	Styles []string
}

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

// Style injects css verbatim; "</style" inside is escaped as "<\/style".
func Style(css string) Node {
	return El("style", nil, Raw(styleEnd.ReplaceAllString(css, `<\/$1`)))
}

var styleEnd = regexp.MustCompile(`(?i)<\/(style)`)
