package components

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// OutlineProps configures Outline.
type OutlineProps struct {
	// Entries are the headings (TextFlow.Outline).
	Entries []canvas.OutlineEntry
	// Current is the index of the heading the caret is under, or -1.
	Current int
	// OnSelect is called with the index of the clicked heading.
	OnSelect func(index int, entry canvas.OutlineEntry)
}

// Outline renders the document's headings as an indented list, the
// current one marked; clicking one reports it (go there with
// TextFlow.GotoOffset and ScrollView.RevealPending). It is controlled.
func Outline(props OutlineProps) xango.Node {
	var items []xango.Node
	for i, e := range props.Entries {
		i, e := i, e
		class := "gj-outline-item"
		if i == props.Current {
			class += " gj-outline-item-on"
		}
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.A("data-level", itoa(e.Level)), xango.A("data-index", itoa(i)),
			xango.A("style", "padding-left:"+itoa(8+(e.Level-1)*14)+"px")}
		if i == props.Current {
			attrs = append(attrs, xango.A("aria-current", "location"))
		}
		if props.OnSelect != nil {
			attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnSelect(i, e) }))
		}
		items = append(items, xango.El("li", nil, xango.El("button", attrs, xango.Text(e.Text))))
	}
	body := xango.El("ul", []xango.Attr{xango.A("class", "gj-outline-list")}, items...)
	if len(items) == 0 {
		body = xango.El("p", []xango.Attr{xango.A("class", "gj-outline-empty")}, xango.Text("No headings yet. Use Heading 1-3 styles."))
	}
	return xango.El("nav", []xango.Attr{xango.A("class", "gj-outline"), xango.A("aria-label", "Outline")},
		xango.El("h4", []xango.Attr{xango.A("class", "gj-outline-title")}, xango.Text("Outline")), body)
}

// BookmarksProps configures BookmarkList.
type BookmarksProps struct {
	Bookmarks []canvas.Bookmark
	// Excerpts maps a bookmark's name to the text it covers.
	Excerpts map[string]string
	// CanEdit shows the Remove buttons.
	CanEdit  bool
	OnGoto   func(name string)
	OnRemove func(name string)
}

// BookmarkList renders the bookmarks with the text each is on; clicking
// one goes there and, for an editor, Remove takes it off. The link form
// "#name" is shown, to type into a link.
func BookmarkList(props BookmarksProps) xango.Node {
	var items []xango.Node
	for _, b := range props.Bookmarks {
		b := b
		go_ := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-bookmark-go"), xango.A("data-bookmark", b.Name)}
		if props.OnGoto != nil {
			go_ = append(go_, xango.On("click", func(xango.Event) { props.OnGoto(b.Name) }))
		}
		row := []xango.Node{
			xango.El("button", go_, xango.El("strong", nil, xango.Text(b.Name)), xango.El("span", []xango.Attr{xango.A("class", "gj-bookmark-text")}, xango.Text(props.Excerpts[b.Name]))),
			xango.El("code", []xango.Attr{xango.A("class", "gj-bookmark-link")}, xango.Text("#"+b.Name)),
		}
		if props.CanEdit {
			rm := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-bookmark-remove"), xango.A("data-remove", b.Name)}
			if props.OnRemove != nil {
				rm = append(rm, xango.On("click", func(xango.Event) { props.OnRemove(b.Name) }))
			} else {
				rm = append(rm, xango.A("disabled", "disabled"))
			}
			row = append(row, xango.El("button", rm, xango.Text("Remove")))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-bookmark")}, row...))
	}
	body := xango.El("ul", []xango.Attr{xango.A("class", "gj-bookmark-list")}, items...)
	if len(items) == 0 {
		body = xango.El("p", []xango.Attr{xango.A("class", "gj-outline-empty")}, xango.Text("No bookmarks. Select text and add one."))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-bookmarks")},
		xango.El("h4", []xango.Attr{xango.A("class", "gj-outline-title")}, xango.Text("Bookmarks")), body)
}

// FootnoteListProps configures FootnoteList.
type FootnoteListProps struct {
	// Entries are the notes whose marks are in the text (TextFlow.FootnoteList).
	Entries []canvas.FootnoteEntry
	CanEdit bool
	// OnGoto is called when a note's number is clicked (go to its mark).
	OnGoto   func(e canvas.FootnoteEntry)
	OnEdit   func(e canvas.FootnoteEntry)
	OnDelete func(e canvas.FootnoteEntry)
}

// FootnoteList renders the numbered footnotes, their text as Markdown;
// clicking a number goes to its mark and an editor gets Edit and Delete.
func FootnoteList(props FootnoteListProps) xango.Node {
	var items []xango.Node
	for _, e := range props.Entries {
		e := e
		num := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-footnote-num"), xango.A("data-note", e.ID)}
		if props.OnGoto != nil {
			num = append(num, xango.On("click", func(xango.Event) { props.OnGoto(e) }))
		}
		row := []xango.Node{xango.El("button", num, xango.Text(itoa(e.N))), xango.El("div", []xango.Attr{xango.A("class", "gj-footnote-text")}, MarkdownText(e.Text))}
		if props.CanEdit {
			btn := func(class, label string, on func(canvas.FootnoteEntry)) xango.Node {
				a := []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.A("data-note", e.ID)}
				if on != nil {
					a = append(a, xango.On("click", func(xango.Event) { on(e) }))
				} else {
					a = append(a, xango.A("disabled", "disabled"))
				}
				return xango.El("button", a, xango.Text(label))
			}
			row = append(row, btn("gj-footnote-edit", "Edit", props.OnEdit), btn("gj-footnote-delete", "Delete", props.OnDelete))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-footnote")}, row...))
	}
	body := xango.El("ul", []xango.Attr{xango.A("class", "gj-bookmark-list")}, items...)
	if len(items) == 0 {
		body = xango.El("p", []xango.Attr{xango.A("class", "gj-outline-empty")}, xango.Text("No footnotes. Add one at the caret."))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-footnotes")},
		xango.El("h4", []xango.Attr{xango.A("class", "gj-outline-title")}, xango.Text("Footnotes")), body)
}
