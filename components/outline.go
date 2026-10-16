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
	// Collapsed lists the indexes of headings whose subsections are hidden; OnToggle is called with the index of a
	// heading whose toggle was pressed (the caller adds it to or removes it from Collapsed). A heading with nothing
	// under it has no toggle. With OnToggle nil there are no toggles and everything shows.
	Collapsed []int
	OnToggle  func(index int)
	// OnMove, when set, lets a section be moved: each heading gets "Move section up" and "Move section down"
	// buttons, and can be dragged onto another heading. It is called with the heading and the heading to put it in
	// front of (len(Entries) for the end); pass them to TextFlow.MoveSection.
	OnMove func(from, before int)
}

// outlineHidden says which entries sit under a collapsed heading.
func outlineHidden(es []canvas.OutlineEntry, collapsed []int) []bool {
	hidden := make([]bool, len(es))
	for _, c := range collapsed {
		if c < 0 || c >= len(es) {
			continue
		}
		for j := c + 1; j < len(es) && es[j].Level > es[c].Level; j++ {
			hidden[j] = true
		}
	}
	return hidden
}

// OutlineUp is the heading to put section i in front of to move it up one place among its siblings, or -1 when it
// is the first of them.
func OutlineUp(es []canvas.OutlineEntry, i int) int {
	for j := i - 1; j >= 0; j-- {
		if es[j].Level < es[i].Level {
			return -1
		}
		if es[j].Level == es[i].Level {
			return j
		}
	}
	return -1
}

// OutlineDown is the heading to put section i in front of to move it down one place among its siblings
// (len(es) for the end), or -1 when it is the last of them.
func OutlineDown(es []canvas.OutlineEntry, i int) int {
	j := i + 1
	for j < len(es) && es[j].Level > es[i].Level {
		j++
	}
	if j >= len(es) || es[j].Level < es[i].Level {
		return -1
	}
	k := j + 1
	for k < len(es) && es[k].Level > es[j].Level {
		k++
	}
	return k
}

// Outline renders the document's headings as an indented list, the
// current one marked; clicking one reports it (go there with
// TextFlow.GotoOffset and ScrollView.RevealPending). It is controlled.
func Outline(props OutlineProps) xango.Node {
	var items []xango.Node
	hidden := outlineHidden(props.Entries, props.Collapsed)
	collapsed := map[int]bool{}
	for _, c := range props.Collapsed {
		collapsed[c] = true
	}
	dragging := -1
	for i, e := range props.Entries {
		i, e := i, e
		if hidden[i] && props.OnToggle != nil {
			continue
		}
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
		var row []xango.Node
		hasKids := i+1 < len(props.Entries) && props.Entries[i+1].Level > e.Level
		if props.OnToggle != nil && hasKids {
			label, mark := "Collapse "+e.Text, "▾"
			if collapsed[i] {
				label, mark = "Expand "+e.Text, "▸"
			}
			row = append(row, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-outline-toggle"),
				xango.A("aria-expanded", boolString(!collapsed[i])), xango.A("aria-label", label),
				xango.On("click", func(xango.Event) { props.OnToggle(i) })}, xango.Text(mark)))
		}
		row = append(row, xango.El("button", attrs, xango.Text(e.Text)))
		liAttrs := []xango.Attr(nil)
		if props.OnMove != nil {
			if up := OutlineUp(props.Entries, i); up >= 0 {
				row = append(row, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-outline-move"),
					xango.A("aria-label", "Move section up: "+e.Text), xango.On("click", func(xango.Event) { props.OnMove(i, up) })}, xango.Text("↑")))
			}
			if down := OutlineDown(props.Entries, i); down >= 0 {
				row = append(row, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-outline-move"),
					xango.A("aria-label", "Move section down: "+e.Text), xango.On("click", func(xango.Event) { props.OnMove(i, down) })}, xango.Text("↓")))
			}
			liAttrs = []xango.Attr{xango.A("draggable", "true"),
				xango.On("dragstart", func(xango.Event) { dragging = i }),
				xango.On("dragover", func(ev xango.Event) { ev.PreventDefault() }),
				xango.On("drop", func(ev xango.Event) {
					ev.PreventDefault()
					if from := dragging; from >= 0 && from != i {
						dragging = -1
						props.OnMove(from, i)
					}
				})}
		}
		items = append(items, xango.El("li", liAttrs, row...))
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
