package gox

// Document is the <html><head>...</head><body>...</body></html>
// wrapper almost every top-level page needs, so individual pages don't
// each hand-roll that boilerplate. Head holds extra <head> children
// (e.g. a Style-injected stylesheet, <meta>, <link>); Body is the
// single node rendered inside <body> -- use Frag to group multiple
// top-level children there.
type Document struct {
	Title string
	Head  []Node
	Body  Node
}

// Render assembles the document into a single <html> Node tree.
func (d Document) Render() Node {
	head := make([]Node, 0, len(d.Head)+1)
	if d.Title != "" {
		head = append(head, El("title", nil, Text(d.Title)))
	}
	head = append(head, d.Head...)
	return El("html", nil,
		El("head", nil, head...),
		El("body", nil, d.Body),
	)
}

// Style returns a <style> element injecting css verbatim -- e.g. the
// string a gox CSS resource import binds -- for use in a Document's
// Head. css is trusted, unescaped content, same caveat as Raw.
func Style(css string) Node {
	return El("style", nil, Raw(css))
}
