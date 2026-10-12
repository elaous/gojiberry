package components

import (
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// A canvas draws the document as pixels, which a screen reader cannot read.
// These two components are what it reads instead: AccessibleText is a
// visually hidden copy of the document's text with its structure (headings,
// lists, quotes, tables, links, comments, suggested changes), and Announcer a
// live region that says what just happened ("Edit mode", "3 matches"). Both
// are hidden from sight with .gj-sr-only (see a11y.css), not display:none, so
// assistive technology still sees them.

// A11yKind is what kind of block an A11yBlock is.
type A11yKind int

const (
	A11yParagraph A11yKind = iota
	A11yHeading            // Level 1 to 6
	A11yListItem           // Ordered says numbered; Level is the nesting from 0
	A11yQuote
	A11yCode
	A11yTableRow // Cells
)

// A11yBlock is one block of the document, as text.
type A11yBlock struct {
	Kind    A11yKind
	Level   int
	Ordered bool
	Text    string
	// Runs, when set, are the text again as pieces with their own language: a piece with a Lang is
	// written as a span with a lang attribute, so a screen reader switches voice for it. Their
	// texts, joined, are Text.
	Runs []A11yRun
	// Cells are a table row's cells (Kind A11yTableRow); a table is the
	// consecutive rows, the first of them the header.
	Cells []string
	// Mapped says Start is known: where the block's first rune falls in the editor's text stream. A
	// mapped block carries it as data-gj-start, so the page can turn a position in the copy into a caret
	// in the editor and back (Lead is a prefix of the text that is not in the stream, such as the box of
	// a check-list item). Object is true for a block that stands for one inline object (a picture, a
	// table): the whole of it is that one rune.
	Mapped bool
	Start  int
	Lead   int
	Object bool
	// Caret says the editor's caret is in this block: it is marked aria-current="location".
	Caret bool
}

// A11yRun is a piece of a block's text; Lang is its language tag, "" for the document's own.
type A11yRun struct {
	Text string
	Lang string
	// Change is "ins" for text suggested to be added and "del" for text suggested to be removed, "" for
	// text as it is; Author is who suggested it. A reader hears them as insertions and deletions, with the
	// words "Suggested addition by Ana:" / "Suggested removal by Ana:" before.
	Change string
	Author string
	// Href, when it is a safe address, makes the piece a link (a real <a>, opening in a new tab).
	Href string
	// Comments are the IDs (comma separated) of the comments on this piece: the piece is marked and
	// points (aria-details) to the comment's own entry in the list of comments.
	Comments string
}

// A11yComment is a comment of the document, written out for a reader: who wrote it, the text it is on,
// what it says, the replies, and whether it is resolved.
type A11yComment struct {
	ID       string
	Author   string
	Excerpt  string
	Text     string
	Resolved bool
	Replies  []A11yReply
}

// A11yReply is a reply to an A11yComment.
type A11yReply struct{ Author, Text string }

// AccessibleTextProps configures AccessibleText.
type AccessibleTextProps struct {
	// ID is the element's id, for aria-describedby ("a11y-text" if empty).
	ID string
	// Label names the region ("Document text" if empty, translated).
	Label  string
	Blocks []A11yBlock
	// Lang is the language of the document's text as a BCP 47 tag, "" for the page's own: the region is
	// tagged with it, so a screen reader reads the text in that voice.
	Lang string
	// Selection is the selected text, "" for none, announced as part of the
	// region so a reader finds what is selected.
	Selection string
	// Comments are the document's comments, listed after the text as a region of their own; the text
	// they are on points to them.
	Comments []A11yComment
	// OnGoto, when set, is called when an entry of the outline is activated, with the heading's element id
	// and, if the block is mapped, its stream position, and the link's own jump is not made (a page with a
	// <base> element would take "#id" for another address). The host focuses the heading (it can take
	// focus) and moves the editor's caret there.
	OnGoto func(headingID string, start int, mapped bool)
}

// AccessibleText is the document's text for assistive technology: one element
// per block, in order, with real heading, list, quote and table markup. The
// links are real links, the first row of a table holds column headers (th
// scope="col"), the headings are listed in a navigation landmark (the
// outline), commented text and suggested changes are marked, and the comments
// follow in a region of their own.
func AccessibleText(p AccessibleTextProps) xango.Node {
	id := p.ID
	if id == "" {
		id = "a11y-text"
	}
	label := p.Label
	if label == "" {
		label = tr("a11y.documentText", "Document text")
	}
	known := map[string]bool{}
	for _, c := range p.Comments {
		known[c.ID] = true
	}
	var kids []xango.Node
	var outline []a11yHeadingRef
	for i := 0; i < len(p.Blocks); i++ {
		b := p.Blocks[i]
		switch b.Kind {
		case A11yHeading:
			hid := id + "-h" + strconv.Itoa(len(outline))
			lvl := min(max(b.Level, 1), 6)
			outline = append(outline, a11yHeadingRef{id: hid, level: lvl, text: b.Text, start: b.Start, mapped: b.Mapped})
			kids = append(kids, xango.El("h"+strconv.Itoa(lvl), a11yBlockAttrs(b, xango.A("id", hid), xango.A("tabindex", "-1")), a11yInline(b, id, known)...))
		case A11yListItem:
			// Consecutive items of one kind make one list.
			j := i
			for j < len(p.Blocks) && p.Blocks[j].Kind == A11yListItem && p.Blocks[j].Ordered == b.Ordered {
				j++
			}
			tag := "ul"
			if b.Ordered {
				tag = "ol"
			}
			var items []xango.Node
			for _, it := range p.Blocks[i:j] {
				items = append(items, xango.El("li", a11yBlockAttrs(it, xango.A("aria-level", strconv.Itoa(it.Level+1))), a11yInline(it, id, known)...))
			}
			kids = append(kids, xango.El(tag, nil, items...))
			i = j - 1
		case A11yQuote:
			kids = append(kids, xango.El("blockquote", nil, xango.El("p", a11yBlockAttrs(b), a11yInline(b, id, known)...)))
		case A11yCode:
			kids = append(kids, xango.El("pre", a11yBlockAttrs(b), xango.El("code", nil, xango.Text(b.Text))))
		case A11yTableRow:
			j := i
			for j < len(p.Blocks) && p.Blocks[j].Kind == A11yTableRow {
				j++
			}
			var head, body []xango.Node
			for r, row := range p.Blocks[i:j] {
				var cs []xango.Node
				for _, c := range row.Cells {
					if r == 0 {
						cs = append(cs, xango.El("th", []xango.Attr{xango.A("scope", "col")}, xango.Text(c)))
					} else {
						cs = append(cs, xango.El("td", nil, xango.Text(c)))
					}
				}
				if r == 0 {
					head = append(head, xango.El("tr", nil, cs...))
				} else {
					body = append(body, xango.El("tr", nil, cs...))
				}
			}
			tb := []xango.Node{xango.El("thead", nil, head...)}
			if len(body) > 0 {
				tb = append(tb, xango.El("tbody", nil, body...))
			}
			tbl := b
			for _, row := range p.Blocks[i:j] {
				tbl.Caret = tbl.Caret || row.Caret
			}
			kids = append(kids, xango.El("table", a11yBlockAttrs(tbl), tb...))
			i = j - 1
		default:
			kids = append(kids, xango.El("p", a11yBlockAttrs(b), a11yInline(b, id, known)...))
		}
	}
	if p.Selection != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-a11y-selection")},
			xango.Text(trf("a11y.selected", "Selected: %s", p.Selection))))
	}
	if len(p.Comments) > 0 {
		kids = append(kids, a11yCommentList(id, p.Comments))
	}
	// The outline comes first: a reader finds it as a landmark and goes to a heading from it.
	if len(outline) > 0 {
		kids = append([]xango.Node{a11yOutline(id, outline, p.OnGoto)}, kids...)
	}
	attrs := []xango.Attr{xango.A("id", id), xango.A("class", "gj-sr-only"), xango.A("role", "document"), xango.A("aria-label", label)}
	if p.Lang != "" {
		attrs = append(attrs, xango.A("lang", p.Lang))
	}
	return xango.El("div", attrs, kids...)
}

type a11yHeadingRef struct {
	id     string
	level  int
	text   string
	start  int
	mapped bool
}

// a11yBlockAttrs are the attributes that map a block to the editor (where it starts in the text stream)
// and the mark of the caret being in it.
func a11yBlockAttrs(b A11yBlock, extra ...xango.Attr) []xango.Attr {
	attrs := append([]xango.Attr(nil), extra...)
	if b.Mapped {
		attrs = append(attrs, xango.A("data-gj-start", strconv.Itoa(b.Start)))
		if b.Lead > 0 {
			attrs = append(attrs, xango.A("data-gj-lead", strconv.Itoa(b.Lead)))
		}
		if b.Object {
			attrs = append(attrs, xango.A("data-gj-object", "true"))
		}
	}
	if b.Caret {
		attrs = append(attrs, xango.A("aria-current", "location"))
	}
	return attrs
}

// a11yOutline is the headings as nested lists of links in a navigation landmark.
func a11yOutline(id string, hs []a11yHeadingRef, onGoto func(string, int, bool)) xango.Node {
	var build func(from, level int) ([]xango.Node, int)
	build = func(from, level int) ([]xango.Node, int) {
		var items []xango.Node
		i := from
		for i < len(hs) && hs[i].level >= level {
			h := hs[i]
			if h.level > level { // a deeper heading with no parent at this level: nest it where it is
				sub, n := build(i, h.level)
				items = append(items, xango.El("li", nil, xango.El("ul", nil, sub...)))
				i = n
				continue
			}
			attrs := []xango.Attr{xango.A("href", "#"+h.id)}
			if onGoto != nil {
				hid, start, mapped := h.id, h.start, h.mapped
				attrs = append(attrs, xango.On("click", func(e xango.Event) { e.PreventDefault(); onGoto(hid, start, mapped) }))
			}
			li := []xango.Node{xango.El("a", attrs, xango.Text(h.text))}
			i++
			if i < len(hs) && hs[i].level > level {
				sub, n := build(i, hs[i].level)
				li = append(li, xango.El("ul", nil, sub...))
				i = n
			}
			items = append(items, xango.El("li", nil, li...))
		}
		return items, i
	}
	first := hs[0].level
	for _, h := range hs {
		first = min(first, h.level)
	}
	items, _ := build(0, first)
	return xango.El("nav", []xango.Attr{xango.A("id", id+"-outline"), xango.A("aria-label", tr("a11y.outline", "Document outline"))},
		xango.El("ul", nil, items...))
}

// a11yCommentList is the comments as a complementary region, one article for each.
func a11yCommentList(id string, cs []A11yComment) xango.Node {
	var items []xango.Node
	for _, c := range cs {
		who := c.Author
		if who == "" {
			who = tr("proposals.someone", "Someone")
		}
		head := trf("a11y.commentBy", "Comment by %s", who)
		if c.Resolved {
			head = trf("a11y.commentByResolved", "Comment by %s (resolved)", who)
		}
		kids := []xango.Node{xango.El("p", nil, xango.Text(head+": "+c.Text))}
		if c.Excerpt != "" {
			kids = append(kids, xango.El("p", nil, xango.Text(trf("a11y.commentOn", "On the text: %s", c.Excerpt))))
		}
		for _, r := range c.Replies {
			rw := r.Author
			if rw == "" {
				rw = tr("proposals.someone", "Someone")
			}
			kids = append(kids, xango.El("p", nil, xango.Text(trf("a11y.replyBy", "Reply by %s: %s", rw, r.Text))))
		}
		items = append(items, xango.El("article", []xango.Attr{xango.A("id", a11yCommentID(id, c.ID))}, kids...))
	}
	return xango.El("aside", []xango.Attr{xango.A("id", id+"-comments"), xango.A("aria-label", trn("a11y.comments", "%d comment", "%d comments", len(cs)))}, items...)
}

func a11yCommentID(id, comment string) string { return id + "-c-" + comment }

// Announcer is a live region: when its message changes, a screen reader says it
// (politely, after what it is already saying). Keep it in the page and change
// the message; "" says nothing.
func Announcer(message string) xango.Node {
	return xango.El("div", []xango.Attr{xango.A("class", "gj-sr-only gj-announcer"), xango.A("role", "status"), xango.A("aria-live", "polite"), xango.A("aria-atomic", "true")},
		xango.Text(message))
}

// a11yInline is a block's text as nodes: plain text, or with Runs a node for each piece: a span with a
// lang attribute for a piece that has a language of its own, a link for one that is a link, a mark
// (pointing to its comment) for commented text, and ins or del for a suggested change.
func a11yInline(b A11yBlock, id string, known map[string]bool) []xango.Node {
	if len(b.Runs) == 0 {
		return []xango.Node{xango.Text(b.Text)}
	}
	var out []xango.Node
	for _, r := range b.Runs {
		n := xango.Text(r.Text)
		if r.Lang != "" {
			n = xango.El("span", []xango.Attr{xango.A("lang", r.Lang)}, xango.Text(r.Text))
		}
		if r.Href != "" && a11ySafeHref(r.Href) {
			n = xango.El("a", []xango.Attr{xango.A("href", r.Href), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer")}, n)
		}
		if r.Comments != "" {
			var refs []string
			for _, cid := range strings.Split(r.Comments, ",") {
				if known[cid] {
					refs = append(refs, a11yCommentID(id, cid))
				}
			}
			if len(refs) > 0 {
				n = xango.El("mark", []xango.Attr{xango.A("aria-details", strings.Join(refs, " "))},
					xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(tr("a11y.commentStart", "Commented text: "))), n)
			}
		}
		if r.Change != "" {
			who := r.Author
			if who == "" {
				who = tr("proposals.someone", "Someone")
			}
			lead := trf("a11y.suggestedAdd", "Suggested addition by %s: ", who)
			tag := "ins"
			if r.Change == "del" {
				lead, tag = trf("a11y.suggestedRemove", "Suggested removal by %s: ", who), "del"
			}
			n = xango.El(tag, nil, xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(lead)), n)
		}
		out = append(out, n)
	}
	return out
}

// a11ySafeHref reports whether a link may be put in the copy: a relative reference, a #bookmark, or an
// http, https, mailto or tel address (never javascript: or data:).
func a11ySafeHref(u string) bool {
	u = strings.TrimSpace(u)
	i := strings.IndexAny(u, ":/?#")
	if i < 0 || u[i] != ':' {
		return true
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}
