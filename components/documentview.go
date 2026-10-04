package components

import "github.com/elaous/gojiberry/xango"

// DocumentViewProps configures DocumentView.
type DocumentViewProps struct {
	// Title is the document's name, shown at the top left.
	Title string
	// OnTitle, if set, makes the title an editable text box.
	OnTitle func(title string)
	// MenuBar is the row of menus (components.MenuBar), shown beside the
	// title.
	MenuBar xango.Node
	// Toolbar is the formatting toolbar under it (buttons, selectors).
	Toolbar xango.Node
	// Canvas is the document itself: the <canvas> and its hidden input.
	Canvas xango.Node
	// Sidebar is an optional panel to the right (outline, key bindings,
	// link preview...).
	Sidebar *xango.Node
	// Status is the line at the bottom ("Page 2 of 3 · 412 words").
	Status string
}

// DocumentView lays out a word-processor window around a canvas editor,
// the way Google Docs does: the title and a menu bar across the top, a
// toolbar under them, the document on a grey desk in the middle with an
// optional sidebar beside it, and a status line at the bottom. It is
// plain structure -- header, toolbar, main (desk and sidebar), footer --
// with a stylesheet (documentview.css); everything inside is yours.
func DocumentView(props DocumentViewProps) xango.Node {
	title := xango.Node(xango.El("span", []xango.Attr{xango.A("class", "gj-docview-title")}, xango.Text(props.Title)))
	if props.OnTitle != nil {
		title = xango.El("input", []xango.Attr{
			xango.A("class", "gj-docview-title gj-docview-title-edit"), xango.A("type", "text"),
			xango.A("value", props.Title), xango.A("aria-label", "Document title"),
			xango.On("change", func(e xango.Event) { props.OnTitle(xango.EventValue[string](e)) }),
		})
	}
	main := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-docview-desk")}, props.Canvas)}
	if props.Sidebar != nil {
		main = append(main, xango.El("aside", []xango.Attr{xango.A("class", "gj-docview-sidebar")}, *props.Sidebar))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-docview")},
		xango.El("header", []xango.Attr{xango.A("class", "gj-docview-top")}, title,
			xango.El("div", []xango.Attr{xango.A("class", "gj-docview-menubar")}, props.MenuBar)),
		xango.El("div", []xango.Attr{xango.A("class", "gj-docview-toolbar"), xango.A("role", "toolbar")}, props.Toolbar),
		xango.El("main", []xango.Attr{xango.A("class", "gj-docview-main")}, main...),
		xango.El("footer", []xango.Attr{xango.A("class", "gj-docview-status")}, xango.Text(props.Status)),
	)
}
