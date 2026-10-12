package export

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// Page geometry used for formats that have pages (CSS pixels, 96 to the
// inch): US Letter with 3/4 inch margins.
const (
	defaultPageWidth  = 816
	defaultPageHeight = 1056
	defaultMargin     = 72
)

var headingScales = [5]float64{2, 1.6, 1.35, 1.2, 1.05}

// line is one paragraph while the content is being read.
type line struct {
	runs []docfile.Chars
	para docfile.Para
	// after are the blocks of non-text content (an image, a table cell) this
	// line stands for; such a line has no text of its own.
	after []Block
}

// FromDocument turns a saved document into the format-agnostic Snapshot the
// exporters read: one Block per paragraph (heading, list item, quote, code),
// table cell or image, with character formatting as Segments.
func FromDocument(d docfile.Document) Snapshot { return fromDocument(acceptedView(d)) }

// FromDocumentTracked is FromDocument that keeps the suggested changes: a run proposed to be added has
// Format.Change Inserted and one proposed to be removed has Deleted (with who and when), for a format that
// can show them (HTML, EPUB and Word write them as insertions and deletions). FromDocument shows the
// document as it would be with every suggestion accepted, which is what a format that cannot show
// them (plain text, Markdown, PDF, RTF) writes, so struck text never reaches a file that cannot strike it.
func FromDocumentTracked(d docfile.Document) Snapshot { return fromDocument(d) }

// acceptedView is the document with every suggestion accepted: text proposed to be removed is gone and text
// proposed to be added is ordinary text.
func acceptedView(d docfile.Document) docfile.Document {
	has := false
	for _, it := range d.Content {
		if it.Chars != nil && it.Chars.Format.Proposal != "" {
			has = true
			break
		}
	}
	if !has {
		return d
	}
	out := d
	out.Content = make([]docfile.Item, 0, len(d.Content))
	for _, it := range d.Content {
		if it.Chars != nil && it.Chars.Format.Proposal != "" {
			m, ok := canvas.ParseProposalMark(it.Chars.Format.Proposal)
			if ok && m.Kind == canvas.ProposalDelete {
				continue
			}
			c := *it.Chars
			c.Format.Proposal = ""
			it.Chars = &c
		}
		out.Content = append(out.Content, it)
	}
	return out
}

func fromDocument(d docfile.Document) Snapshot {
	snap := Snapshot{
		Title:     d.Title,
		PageWidth: defaultPageWidth, PageHeight: defaultPageHeight,
		MarginLeft: defaultMargin, MarginTop: defaultMargin, MarginRight: defaultMargin, MarginBottom: defaultMargin,
	}
	snap.Lang = d.Language()
	if l := d.PageLayout(); !l.IsZero() {
		snap.Header, snap.Footer, snap.TOC, snap.TOCTitle = l.Header, l.Footer, l.TOC, l.TOCTitle
		if l.Width > 0 {
			snap.PageWidth, snap.PageHeight = l.Width/0.75, l.Height/0.75 // points to CSS pixels
		}
		if l.Margins != ([4]float64{}) {
			snap.MarginTop, snap.MarginRight, snap.MarginBottom, snap.MarginLeft = l.Margins[0]/0.75, l.Margins[1]/0.75, l.Margins[2]/0.75, l.Margins[3]/0.75
		}
	}
	sum := sha1.Sum([]byte(d.Title))
	snap.ID = hex.EncodeToString(sum[:8])

	notes := map[string]string{}
	for _, n := range d.Footnotes {
		notes[n.ID] = n.Text
	}
	var noteOrder []string

	var lines []line
	cur := line{}
	flush := func() {
		lines = append(lines, cur)
		cur = line{}
	}
	// pending ends the paragraph being read before a block that is not text, if
	// it has anything in it (an empty one is only the start of the next line).
	pending := func() {
		if len(cur.runs) > 0 || cur.para.List.Kind != 0 {
			flush()
		}
	}
	for _, it := range d.Content {
		switch {
		case it.Para != nil:
			cur.para = *it.Para
		case it.Chars != nil:
			parts := strings.Split(it.Chars.Text, "\n")
			for i, p := range parts {
				if i > 0 {
					flush()
				}
				if p != "" {
					cur.runs = append(cur.runs, docfile.Chars{Text: p, Format: it.Chars.Format})
				}
			}
		case it.Footnote != nil:
			noteOrder = append(noteOrder, it.Footnote.ID)
			n := len(noteOrder)
			cur.runs = append(cur.runs, docfile.Chars{Text: itoa(n), Format: docfile.Format{Superscript: true, Style: noteRefStyle + itoa(n)}})
		case it.Rule != nil:
			pending()
			cur.runs = []docfile.Chars{{Text: strings.Repeat("─", 24)}}
			flush()
		case it.Image != nil:
			pending()
			lines = append(lines, line{after: []Block{{Kind: Image, ImageSrc: it.Image.URL, ImageW: it.Image.Width, ImageH: it.Image.Height, ImageAlt: it.Image.Alt, ImageDecorative: it.Image.Decorative}}})
		case it.Table != nil:
			pending()
			lines = append(lines, tableLines(it.Table)...)
		}
	}
	lines = append(lines, cur)
	// A trailing empty paragraph is only the end of the text.
	for len(lines) > 0 && len(lines[len(lines)-1].runs) == 0 && len(lines[len(lines)-1].after) == 0 && lines[len(lines)-1].para.List.Kind == 0 {
		lines = lines[:len(lines)-1]
	}

	snap.Blocks = blocksFrom(lines)
	if len(noteOrder) > 0 {
		snap.Blocks = append(snap.Blocks, Block{Kind: Heading2, Level: 2, Segments: []Segment{{Text: "Notes"}}})
		for i, id := range noteOrder {
			snap.Blocks = append(snap.Blocks, Block{Kind: NumberedItem, NumberedIndex: i + 1, Note: i + 1, Segments: []Segment{{Text: notes[id]}}})
		}
	}
	return snap
}

// tableLines gives a table's cells as marker lines blocksFrom turns into
// table-cell blocks, with their merges (spans, and the cells they cover) and
// bold.
func tableLines(t *docfile.TableItem) []line {
	rows := len(t.Rows)
	cols := 0
	for _, r := range t.Rows {
		cols = max(cols, len(r.Cells))
	}
	span := func(r, c int) (rs, cs int, covered bool) {
		rs, cs = 1, 1
		for _, m := range t.Merges {
			if r >= m.Row && r < m.Row+m.Rows && c >= m.Col && c < m.Col+m.Cols {
				if r == m.Row && c == m.Col {
					return m.Rows, m.Cols, false
				}
				return 1, 1, true
			}
		}
		return rs, cs, false
	}
	style := func(r, c int) docfile.TableCellStyle {
		for _, st := range t.Styles {
			if st.Row == r && st.Col == c {
				return st
			}
		}
		return docfile.TableCellStyle{}
	}
	var out []line
	for r, row := range t.Rows {
		for c := 0; c < cols; c++ {
			text := ""
			if c < len(row.Cells) {
				text = row.Cells[c]
			}
			rs, cs, covered := span(r, c)
			f := docfile.Format{}
			st := style(r, c)
			if st.Bold {
				f.FontWeight = 700
			}
			runs := []docfile.Chars{{Text: text, Format: f}}
			out = append(out, line{
				runs: runs,
				after: []Block{{Kind: TableCell, TableRow: r, TableCol: c, TableRows: rows, TableCols: cols, TableColSpan: cs, TableRowSpan: rs, TableCovered: covered, TableBackground: st.Background, TableAlign: st.Align,
					Segments: segmentsOf(runs)}},
			})
		}
	}
	return out
}

func blocksFrom(lines []line) []Block {
	var out []Block
	number, prevOrdered := 0, false
	for _, ln := range lines {
		if len(ln.after) > 0 { // a table cell
			out = append(out, ln.after...)
			prevOrdered = false
			continue
		}
		b := Block{Segments: segmentsOf(ln.runs)}
		level := headingLevel(ln.runs, ln.para.Style)
		switch {
		case level > 0:
			b.Kind, b.Level = Heading1+BlockKind(level-1), level
			if level > 3 {
				b.Kind, b.Level = Heading3, 3
			}
		case ln.para.List.Kind == 1: // bullet
			b.Kind = BulletItem
		case ln.para.List.Kind == 3: // check list: shown as a bullet with its box
			b.Kind = BulletItem
			box := "☐ "
			if ln.para.List.Checked {
				box = "☑ "
			}
			b.Segments = append([]Segment{{Text: box}}, b.Segments...)
		case ln.para.List.Kind == 2: // ordered
			b.Kind = NumberedItem
		case ln.para.QuoteBar != "":
			b.Kind = Quote
		case allMonospace(ln.runs):
			b.Kind = Code
			var sb strings.Builder
			for _, r := range ln.runs {
				sb.WriteString(r.Text)
			}
			b.Segments = []Segment{{Text: sb.String()}}
		default:
			b.Kind = Paragraph
		}
		if b.Kind == NumberedItem {
			if !prevOrdered {
				number = 0
			}
			number++
			b.NumberedIndex = number
		}
		prevOrdered = b.Kind == NumberedItem
		out = append(out, b)
	}
	return out
}

// headingLevel is 1-5 when the line is a heading: its paragraph style is
// "Title" or "Heading n", or every run has a heading's scale.
// HeadingLevel is the level (1 to 6) of a paragraph made of these runs with this paragraph style, or 0
// if it is not a heading: the style "Title" or "Heading N", else every run at a heading's scale. It is
// what FromDocument uses (which folds the levels above 3 into Heading3), for a caller that needs the real level.
func HeadingLevel(runs []docfile.Chars, style string) int { return headingLevel(runs, style) }

func headingLevel(runs []docfile.Chars, style string) int {
	if strings.EqualFold(style, "title") {
		return 1
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(style), "heading "); ok && len(rest) == 1 && rest[0] >= '1' && rest[0] <= '6' {
		return int(rest[0] - '0')
	}
	if len(runs) == 0 {
		return 0
	}
	for lvl, s := range headingScales {
		all := true
		for _, r := range runs {
			if r.Format.Scale != s {
				all = false
				break
			}
		}
		if all {
			return lvl + 1
		}
	}
	return 0
}

func allMonospace(runs []docfile.Chars) bool {
	if len(runs) == 0 {
		return false
	}
	for _, r := range runs {
		if r.Format.FontFamily != "monospace" {
			return false
		}
	}
	return true
}

// segmentsOf maps runs to segments; U+2028 (a soft line break) becomes a
// SoftBreak segment.
func segmentsOf(runs []docfile.Chars) []Segment {
	var out []Segment
	for _, r := range runs {
		f := Format{
			Bold: r.Format.FontWeight >= 600, Italic: r.Format.FontStyle == "italic" || r.Format.FontStyle == "oblique",
			Underline: r.Format.Underline, Strikethrough: r.Format.Strike,
			Superscript: r.Format.Superscript, Subscript: r.Format.Subscript, Href: r.Format.Link, Lang: r.Format.Lang,
		}
		if n, ok := strings.CutPrefix(r.Format.Style, noteRefStyle); ok {
			f.NoteRef, _ = strconv.Atoi(n)
		}
		if m, ok := canvas.ParseProposalMark(r.Format.Proposal); ok {
			f.Change, f.Author, f.Time = Inserted, m.Author, m.Time.Unix()
			if m.Kind == canvas.ProposalDelete {
				f.Change = Deleted
			}
		}
		parts := strings.Split(r.Text, " ")
		for i, p := range parts {
			if i > 0 {
				out = append(out, Segment{SoftBreak: true})
			}
			if p != "" {
				out = append(out, Segment{Text: p, Format: f})
			}
		}
	}
	return out
}

// noteRefStyle marks the run of a footnote's number while a document is turned into a snapshot (no
// stored style name starts with a NUL).
const noteRefStyle = "\x00note:"

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
