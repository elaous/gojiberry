package components

import "github.com/elaous/gojiberry/gox"

// DocumentViewProps configures DocumentView.
type DocumentViewProps struct {
	// Title is the document's name, shown at the top left.
	Title string
	// OnTitle, if set, makes the title an editable text box.
	OnTitle func(title string)
	// MenuBar is the row of menus (components.MenuBar), shown beside the
	// title.
	MenuBar gox.Node
	// Toolbar is the formatting toolbar under it (buttons, selectors).
	Toolbar gox.Node
	// Canvas is the document itself: the <canvas> and its hidden input.
	Canvas gox.Node
	// Sidebar is an optional panel to the right (outline, key bindings,
	// link preview...).
	Sidebar *gox.Node
	// Status is the line at the bottom ("Page 2 of 3 · 412 words").
	Status string
}

// DocumentView lays out a word-processor window around a canvas editor,
// the way Google Docs does: the title and a menu bar across the top, a
// toolbar under them, the document on a grey desk in the middle with an
// optional sidebar beside it, and a status line at the bottom. It is
// plain structure -- header, toolbar, main (desk and sidebar), footer --
// with a stylesheet (documentview.css); everything inside is yours.
func DocumentView(props DocumentViewProps) gox.Node {
	title := gox.Node(gox.El("span", []gox.Attr{gox.A("class", "gj-docview-title")}, gox.Text(props.Title)))
	if props.OnTitle != nil {
		title = gox.El("input", []gox.Attr{
			gox.A("class", "gj-docview-title gj-docview-title-edit"), gox.A("type", "text"),
			gox.A("value", props.Title), gox.A("aria-label", "Document title"),
			gox.On("change", func(e gox.Event) { props.OnTitle(gox.EventValue[string](e)) }),
		})
	}
	main := []gox.Node{gox.El("div", []gox.Attr{gox.A("class", "gj-docview-desk")}, props.Canvas)}
	if props.Sidebar != nil {
		main = append(main, gox.El("aside", []gox.Attr{gox.A("class", "gj-docview-sidebar")}, *props.Sidebar))
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-docview")},
		gox.El("header", []gox.Attr{gox.A("class", "gj-docview-top")}, title,
			gox.El("div", []gox.Attr{gox.A("class", "gj-docview-menubar")}, props.MenuBar)),
		gox.El("div", []gox.Attr{gox.A("class", "gj-docview-toolbar"), gox.A("role", "toolbar")}, props.Toolbar),
		gox.El("main", []gox.Attr{gox.A("class", "gj-docview-main")}, main...),
		gox.El("footer", []gox.Attr{gox.A("class", "gj-docview-status")}, gox.Text(props.Status)),
	)
}
