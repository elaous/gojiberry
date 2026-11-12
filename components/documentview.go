package components

import "github.com/elaous/xango"

// DocumentViewProps configures DocumentView.
type DocumentViewProps struct {
	// Title is the document's name, shown at the top left.
	Title string
	// OnTitle, if set, makes the title an editable text box.
	OnTitle func(title string)
	MenuBar xango.Node
	// Toolbar is the formatting toolbar under it (buttons, selectors).
	Toolbar   xango.Node
	Canvas    xango.Node
	Sidebar   *xango.Node
	Status    string
	StatusBar *xango.Node
}

func DocumentView(props DocumentViewProps) xango.Node {
	title := xango.Node(xango.El("span", []xango.Attr{xango.A("class", "gj-docview-title")}, xango.Text(props.Title)))
	if props.OnTitle != nil {
		title = xango.El("input", []xango.Attr{
			xango.A("class", "gj-docview-title gj-docview-title-edit"), xango.A("type", "text"),
			xango.A("value", props.Title), xango.A("aria-label", "Document title"),
			xango.On("change", func(e xango.Event) { props.OnTitle(xango.EventValue[string](e)) }),
		})
	}
	main := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-docview-desk"), xango.A("tabindex", "-1")}, props.Canvas)}
	skip := func(class, label string) xango.Node {
		return xango.El("a", []xango.Attr{xango.A("href", "#"), xango.A("class", "gj-skip-link"),
			xango.On("click", func(e xango.Event) {
				e.PreventDefault()
				e.MoveFocus(".gj-docview", ":scope ."+class, xango.FocusFirst)
			})}, xango.Text(label))
	}
	links := []xango.Node{skip("gj-docview-desk", tr("docview.skipDoc", "Skip to the document"))}
	if props.Sidebar != nil {
		main = append(main, xango.El("aside", []xango.Attr{xango.A("class", "gj-docview-sidebar"), xango.A("tabindex", "-1")}, *props.Sidebar))
		links = append(links, skip("gj-docview-sidebar", tr("docview.skipSide", "Skip to the side panel")))
	}
	status := xango.Node(xango.Text(props.Status))
	if props.StatusBar != nil {
		status = *props.StatusBar
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-docview")},
		xango.El("nav", []xango.Attr{xango.A("class", "gj-skip"), xango.A("aria-label", tr("docview.skip", "Skip links"))}, links...),
		xango.El("header", []xango.Attr{xango.A("class", "gj-docview-top")}, title,
			xango.El("div", []xango.Attr{xango.A("class", "gj-docview-menubar")}, props.MenuBar)),
		// a labelled region, so the toolbar is inside a landmark
		xango.El("div", []xango.Attr{xango.A("role", "region"), xango.A("aria-label", tr("docview.toolbar", "Toolbar"))},
			xango.El("div", []xango.Attr{xango.A("class", "gj-docview-toolbar"), xango.A("role", "toolbar"), xango.A("aria-label", tr("docview.toolbar", "Toolbar"))}, props.Toolbar)),
		xango.El("main", []xango.Attr{xango.A("class", "gj-docview-main")}, main...),
		xango.El("footer", []xango.Attr{xango.A("class", "gj-docview-status")}, status),
	)
}
