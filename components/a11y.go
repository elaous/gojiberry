package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// A canvas draws the document as pixels, which a screen reader cannot read.
// These two components are what it reads instead: AccessibleText is a
// visually hidden copy of the document's text with its structure (headings,
// lists, quotes, tables), and Announcer a live region that says what just
// happened ("Edit mode", "3 matches"). Both are hidden from sight with
// .gj-sr-only (see a11y.css), not display:none, so assistive technology still
// sees them.

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
}

// A11yRun is a piece of a block's text; Lang is its language tag, "" for the document's own.
type A11yRun struct {
	Text string
	Lang string
}

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
}

// AccessibleText is the document's text for assistive technology: one element
// per block, in order, with real heading, list, quote and table markup.
func AccessibleText(p AccessibleTextProps) xango.Node {
	id := p.ID
	if id == "" {
		id = "a11y-text"
	}
	label := p.Label
	if label == "" {
		label = tr("a11y.documentText", "Document text")
	}
	var kids []xango.Node
	for i := 0; i < len(p.Blocks); i++ {
		b := p.Blocks[i]
		switch b.Kind {
		case A11yHeading:
			kids = append(kids, xango.El("h"+strconv.Itoa(min(max(b.Level, 1), 6)), nil, a11yInline(b)...))
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
				items = append(items, xango.El("li", []xango.Attr{xango.A("aria-level", strconv.Itoa(it.Level+1))}, a11yInline(it)...))
			}
			kids = append(kids, xango.El(tag, nil, items...))
			i = j - 1
		case A11yQuote:
			kids = append(kids, xango.El("blockquote", nil, xango.El("p", nil, a11yInline(b)...)))
		case A11yCode:
			kids = append(kids, xango.El("pre", nil, xango.El("code", nil, xango.Text(b.Text))))
		case A11yTableRow:
			j := i
			for j < len(p.Blocks) && p.Blocks[j].Kind == A11yTableRow {
				j++
			}
			var rows []xango.Node
			for r, row := range p.Blocks[i:j] {
				cell := "td"
				if r == 0 {
					cell = "th"
				}
				var cs []xango.Node
				for _, c := range row.Cells {
					cs = append(cs, xango.El(cell, nil, xango.Text(c)))
				}
				rows = append(rows, xango.El("tr", nil, cs...))
			}
			kids = append(kids, xango.El("table", nil, xango.El("tbody", nil, rows...)))
			i = j - 1
		default:
			kids = append(kids, xango.El("p", nil, a11yInline(b)...))
		}
	}
	if p.Selection != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-a11y-selection")},
			xango.Text(trf("a11y.selected", "Selected: %s", p.Selection))))
	}
	attrs := []xango.Attr{xango.A("id", id), xango.A("class", "gj-sr-only"), xango.A("role", "document"), xango.A("aria-label", label)}
	if p.Lang != "" {
		attrs = append(attrs, xango.A("lang", p.Lang))
	}
	return xango.El("div", attrs, kids...)
}

// Announcer is a live region: when its message changes, a screen reader says it
// (politely, after what it is already saying). Keep it in the page and change
// the message; "" says nothing.
func Announcer(message string) xango.Node {
	return xango.El("div", []xango.Attr{xango.A("class", "gj-sr-only gj-announcer"), xango.A("role", "status"), xango.A("aria-live", "polite"), xango.A("aria-atomic", "true")},
		xango.Text(message))
}

// a11yInline is a block's text as nodes: plain text, or with Runs a span with a lang attribute for each
// piece that has a language of its own.
func a11yInline(b A11yBlock) []xango.Node {
	if len(b.Runs) == 0 {
		return []xango.Node{xango.Text(b.Text)}
	}
	var out []xango.Node
	for _, r := range b.Runs {
		if r.Lang == "" {
			out = append(out, xango.Text(r.Text))
			continue
		}
		out = append(out, xango.El("span", []xango.Attr{xango.A("lang", r.Lang)}, xango.Text(r.Text)))
	}
	return out
}
