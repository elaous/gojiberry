package components

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
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
func Outline(props OutlineProps) gox.Node {
	var items []gox.Node
	for i, e := range props.Entries {
		i, e := i, e
		class := "gj-outline-item"
		if i == props.Current {
			class += " gj-outline-item-on"
		}
		attrs := []gox.Attr{gox.A("type", "button"), gox.A("class", class), gox.A("data-level", itoa(e.Level)), gox.A("data-index", itoa(i)),
			gox.A("style", "padding-left:"+itoa(8+(e.Level-1)*14)+"px")}
		if i == props.Current {
			attrs = append(attrs, gox.A("aria-current", "location"))
		}
		if props.OnSelect != nil {
			attrs = append(attrs, gox.On("click", func(gox.Event) { props.OnSelect(i, e) }))
		}
		items = append(items, gox.El("li", nil, gox.El("button", attrs, gox.Text(e.Text))))
	}
	body := gox.El("ul", []gox.Attr{gox.A("class", "gj-outline-list")}, items...)
	if len(items) == 0 {
		body = gox.El("p", []gox.Attr{gox.A("class", "gj-outline-empty")}, gox.Text("No headings yet. Use Heading 1-3 styles."))
	}
	return gox.El("nav", []gox.Attr{gox.A("class", "gj-outline"), gox.A("aria-label", "Outline")},
		gox.El("h4", []gox.Attr{gox.A("class", "gj-outline-title")}, gox.Text("Outline")), body)
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
func BookmarkList(props BookmarksProps) gox.Node {
	var items []gox.Node
	for _, b := range props.Bookmarks {
		b := b
		go_ := []gox.Attr{gox.A("type", "button"), gox.A("class", "gj-bookmark-go"), gox.A("data-bookmark", b.Name)}
		if props.OnGoto != nil {
			go_ = append(go_, gox.On("click", func(gox.Event) { props.OnGoto(b.Name) }))
		}
		row := []gox.Node{
			gox.El("button", go_, gox.El("strong", nil, gox.Text(b.Name)), gox.El("span", []gox.Attr{gox.A("class", "gj-bookmark-text")}, gox.Text(props.Excerpts[b.Name]))),
			gox.El("code", []gox.Attr{gox.A("class", "gj-bookmark-link")}, gox.Text("#"+b.Name)),
		}
		if props.CanEdit {
			rm := []gox.Attr{gox.A("type", "button"), gox.A("class", "gj-bookmark-remove"), gox.A("data-remove", b.Name)}
			if props.OnRemove != nil {
				rm = append(rm, gox.On("click", func(gox.Event) { props.OnRemove(b.Name) }))
			} else {
				rm = append(rm, gox.A("disabled", "disabled"))
			}
			row = append(row, gox.El("button", rm, gox.Text("Remove")))
		}
		items = append(items, gox.El("li", []gox.Attr{gox.A("class", "gj-bookmark")}, row...))
	}
	body := gox.El("ul", []gox.Attr{gox.A("class", "gj-bookmark-list")}, items...)
	if len(items) == 0 {
		body = gox.El("p", []gox.Attr{gox.A("class", "gj-outline-empty")}, gox.Text("No bookmarks. Select text and add one."))
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-bookmarks")},
		gox.El("h4", []gox.Attr{gox.A("class", "gj-outline-title")}, gox.Text("Bookmarks")), body)
}
