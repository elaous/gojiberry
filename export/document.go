package export

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

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
	// start is where the line begins in the text stream (see Block.Start).
	start int
}

// FromDocument turns a saved document into the format-agnostic Snapshot the
// exporters read: one Block per paragraph (heading, list item, quote, code),
// table cell or image, with character formatting as Segments.
func FromDocument(d docfile.Document) Snapshot { return fromDocument(acceptedView(d), false) }

// FromDocumentTracked is FromDocument that keeps the suggested changes: a run proposed to be added has
// Format.Change Inserted and one proposed to be removed has Deleted (with who and when), for a format that
// can show them (HTML, EPUB and Word write them as insertions and deletions). FromDocument shows the
// document as it would be with every suggestion accepted, which is what a format that cannot show
// them (plain text, Markdown, PDF, RTF) writes, so struck text never reaches a file that cannot strike it.
func FromDocumentTracked(d docfile.Document) Snapshot { return fromDocument(d, false) }

// FromDocumentWithMedia is FromDocumentTracked for a format that can embed audio and video (HTML): clips stay
// clips (MediaClip blocks) instead of becoming a link with the transcript as text.
func FromDocumentWithMedia(d docfile.Document) Snapshot { return fromDocument(d, true) }

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

func fromDocument(d docfile.Document, keepMedia bool) Snapshot {
	if !keepMedia {
		d = d.MediaAsText() // a format that cannot embed a clip writes it as a link with its transcript as text
	}
	snap := Snapshot{
		Title:     d.Title,
		PageWidth: defaultPageWidth, PageHeight: defaultPageHeight,
		MarginLeft: defaultMargin, MarginTop: defaultMargin, MarginRight: defaultMargin, MarginBottom: defaultMargin,
	}
	snap.Lang = d.Language()
	snap.Theme = d.Theme()
	if l := d.PageLayout(); !l.IsZero() {
		snap.Header, snap.Footer, snap.TOC, snap.TOCTitle = l.Header, l.Footer, l.TOC, l.TOCTitle
		snap.FirstDiffers, snap.FirstHeader, snap.FirstFooter = l.FirstDiffers, l.FirstHeader, l.FirstFooter
		snap.EvenDiffers, snap.EvenHeader, snap.EvenFooter = l.EvenDiffers, l.EvenHeader, l.EvenFooter
		snap.Vertical = l.Vertical
		if l.Width > 0 {
			snap.PageWidth, snap.PageHeight = l.Width/0.75, l.Height/0.75 // points to CSS pixels
		}
		if l.Margins != ([4]float64{}) {
			snap.MarginTop, snap.MarginRight, snap.MarginBottom, snap.MarginLeft = l.Margins[0]/0.75, l.Margins[1]/0.75, l.Margins[2]/0.75, l.Margins[3]/0.75
		}
	}
	// A table of contents the document holds stands for the contents the exports write at the start (once, never
	// two lists): its title, look and depth are the ones used.
	tocHeld := false
	for _, it := range d.Content {
		if it.TOC != nil {
			tocHeld = true
			snap.TOC = true
			if it.TOC.Title != "" {
				snap.TOCTitle = it.TOC.Title
			}
			snap.TOCStyle, snap.TOCDepth = it.TOC.Style, it.TOC.Depth
			break
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
	pos := 0 // where the next rune of the content falls in the text stream
	flush := func() {
		lines = append(lines, cur)
		cur = line{start: pos}
	}
	// pending ends the paragraph being read before a block that is not text, if
	// it has anything in it (an empty one is only the start of the next line).
	pending := func() {
		if len(cur.runs) > 0 || cur.para.List.Kind != 0 {
			flush()
		}
	}
	tocPos := -1 // where the held table of contents is in the stream
	for _, it := range d.Content {
		switch {
		case it.TOC != nil:
			if tocPos < 0 {
				tocPos = pos
			}
		case it.Para != nil:
			cur.para = *it.Para
		case it.Chars != nil:
			parts := strings.Split(it.Chars.Text, "\n")
			for i, p := range parts {
				if i > 0 {
					pos++ // the paragraph break
					flush()
				}
				if p != "" {
					cur.runs = append(cur.runs, docfile.Chars{Text: p, Format: it.Chars.Format})
					pos += utf8.RuneCountInString(p)
				}
			}
		case it.Footnote != nil:
			noteOrder = append(noteOrder, it.Footnote.ID)
			n := len(noteOrder)
			cur.runs = append(cur.runs, docfile.Chars{Text: itoa(n), Format: docfile.Format{Superscript: true, Style: noteRefStyle + itoa(n)}})
			pos++ // an inline object: one rune of the stream
		case it.Rule != nil:
			pending()
			cur.runs = []docfile.Chars{{Text: strings.Repeat("─", 24)}}
			pos++
			flush()
		case it.Image != nil:
			pending()
			after := []Block{{Start: pos, Kind: Image, ImageSrc: it.Image.URL, ImageW: it.Image.Width, ImageH: it.Image.Height, ImageAlt: it.Image.Alt, ImageDecorative: it.Image.Decorative, ImageCredit: it.Image.Credit, ImageSourceURL: it.Image.SourceURL}}
			if d := strings.TrimSpace(it.Image.LongDesc); d != "" {
				// the long description is text after the picture, so that every format carries it
				after = append(after, Block{Start: -1, Kind: Paragraph, DescribesImage: true, Segments: []Segment{{Text: d}}})
			}
			if lg := strings.TrimSpace(it.Image.Legend); lg != "" {
				// the legend is a paragraph in italics under the picture, so that every format carries it
				legend := Block{Start: -1, Kind: Paragraph, Segments: []Segment{{Text: lg, Format: Format{Italic: true}}}}
				after = append([]Block{after[0], legend}, after[1:]...)
			}
			lines = append(lines, line{start: pos, after: after})
			pos++
			cur.start = pos
		case it.Media != nil:
			pending()
			m := it.Media
			lines = append(lines, line{start: pos, after: []Block{{Start: pos, Kind: MediaClip, MediaURL: m.URL, MediaKind: m.KindName(),
				MediaTitle: m.Title, MediaCaptions: m.Captions, MediaCaptionsURL: m.CaptionsURL, MediaTranscript: m.Words(), MediaW: m.Width, MediaH: m.Height}}})
			pos++
			cur.start = pos
		case it.Form != nil:
			// a form field is written as its label and what is in it, in the line it stands in
			if w := it.Form.Words(); w != "" {
				style := ""
				if b, err := json.Marshal(it.Form); err == nil {
					style = formFieldStyle + string(b)
				}
				cur.runs = append(cur.runs, docfile.Chars{Text: w, Format: docfile.Format{Style: style}})
			}
			pos++
		case it.Equation != nil && it.Equation.Inline:
			// a formula in the line is written as the words that stand for it (the text alternative), so that every
			// format keeps it in its sentence
			if w := it.Equation.Words(); w != "" {
				cur.runs = append(cur.runs, docfile.Chars{Text: w, Format: docfile.Format{Style: formulaStyle + it.Equation.Formula}})
			}
			pos++
		case it.Equation != nil:
			pending()
			eq := EquationBlock(it.Equation.Formula, it.Equation.Alt)
			eq.Start = pos
			lines = append(lines, line{start: pos, after: []Block{eq}})
			pos++
			cur.start = pos
		case it.Table != nil:
			pending()
			lines = append(lines, tableLines(it.Table, pos)...)
			pos++
			cur.start = pos
		}
	}
	lines = append(lines, cur)
	// A trailing empty paragraph is only the end of the text.
	for len(lines) > 0 && len(lines[len(lines)-1].runs) == 0 && len(lines[len(lines)-1].after) == 0 && lines[len(lines)-1].para.List.Kind == 0 {
		lines = lines[:len(lines)-1]
	}

	snap.Blocks = blocksFrom(lines)
	if tocHeld && tocPos >= 0 {
		snap.TOCAt = tocBlock(snap.Blocks, tocPos)
	}
	if len(noteOrder) > 0 {
		snap.Blocks = append(snap.Blocks, Block{Start: -1, Kind: Heading2, Level: 2, Segments: []Segment{{Text: "Notes"}}})
		for i, id := range noteOrder {
			snap.Blocks = append(snap.Blocks, Block{Start: -1, Kind: NumberedItem, NumberedIndex: i + 1, Note: i + 1, Segments: []Segment{{Text: notes[id]}}})
		}
	}
	return snap
}

// tableLines gives a table's cells as marker lines blocksFrom turns into
// table-cell blocks, with their merges (spans, and the cells they cover) and
// bold.
func tableLines(t *docfile.TableItem, start int) []line {
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
	raw := make([][]string, len(t.Rows))
	for i, r := range t.Rows {
		raw[i] = r.Cells
	}
	values := canvas.Values(raw) // a formula is written as its value
	var out []line
	for r, row := range t.Rows {
		for c := 0; c < cols; c++ {
			text := ""
			if c < len(row.Cells) {
				text = values[r][c]
			}
			rs, cs, covered := span(r, c)
			f := docfile.Format{}
			st := style(r, c)
			if st.Bold {
				f.FontWeight = 700
			}
			runs := []docfile.Chars{{Text: text, Format: f}}
			out = append(out, line{
				runs:  runs,
				start: start,
				after: []Block{{Start: start, Kind: TableCell, TableRow: r, TableCol: c, TableRows: rows, TableCols: cols, TableColSpan: cs, TableRowSpan: rs, TableCovered: covered, TableBackground: st.Background, TableAlign: st.Align,
					Segments: segmentsOf(runs)}},
			})
		}
	}
	return out
}

func blocksFrom(lines []line) []Block {
	var out []Block
	var counts []int // the position within each level of the numbered list being read
	for _, ln := range lines {
		if len(ln.after) > 0 { // a table cell
			out = append(out, ln.after...)
			counts = counts[:0]
			continue
		}
		b := Block{Segments: segmentsOf(ln.runs), Start: ln.start}
		level := headingLevel(ln.runs, ln.para.Style)
		switch {
		case level > 0:
			b.Kind, b.Level = Heading1+BlockKind(level-1), level
			if level > 3 {
				b.Kind, b.Level = Heading3, level // levels 4 to 6 are a Heading3 block that says its level
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
			b.Lead = len([]rune(box))
		case ln.para.List.Kind == 2: // ordered
			b.Kind = NumberedItem
		case ln.para.QuoteBar != "":
			b.Kind = Quote
		case allMonospace(ln.runs) || strings.EqualFold(ln.para.Style, "code"):
			b.Kind = Code
			b.CodeLang = ln.para.CodeLang
			var sb strings.Builder
			for _, r := range ln.runs {
				sb.WriteString(r.Text)
			}
			b.Segments = []Segment{{Text: sb.String()}}
		default:
			b.Kind = Paragraph
		}
		lvl := min(max(ln.para.List.Level, 0), 8)
		switch b.Kind {
		case NumberedItem:
			b.Level = lvl
			for len(counts) <= lvl {
				counts = append(counts, 0)
			}
			counts = counts[:lvl+1] // a shallower item ends the deeper numbering
			counts[lvl]++
			b.NumberedIndex = counts[lvl]
		case BulletItem:
			b.Level = lvl
			if lvl == 0 {
				counts = counts[:0]
			}
		default:
			counts = counts[:0]
		}
		if n := len(out); b.Kind == Code && n > 0 && out[n-1].Kind == Code && out[n-1].CodeLang == b.CodeLang && len(out[n-1].Segments) == 1 {
			// the lines of one code block are paragraphs of their own in the editor: one block in the file
			out[n-1].Segments = []Segment{{Text: out[n-1].Segments[0].Text + "\n" + b.Segments[0].Text}}
			continue
		}
		out = append(out, b)
	}
	return out
}

// headingLevel is 1-5 when the line is a heading: its paragraph style is
// "Title" or "Heading n", or every run has a heading's scale.
// HeadingLevel is the level (1 to 6) of a paragraph made of these runs with this paragraph style, or 0
// if it is not a heading: the style "Title" or "Heading N", else every run at a heading's scale. It is
// what FromDocument uses (which gives a heading of level 4 to 6 the kind Heading3 and its real level in Block.Level).
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
			Superscript: r.Format.Superscript, Subscript: r.Format.Subscript, Href: r.Format.Link, Lang: r.Format.Lang, Comment: r.Format.Comment,
		}
		if js, ok := strings.CutPrefix(r.Format.Style, formFieldStyle); ok {
			var ff docfile.FormFieldItem
			if json.Unmarshal([]byte(js), &ff) == nil {
				f.Field = &ff
				out = append(out, Segment{Text: r.Text, Format: f}) // whole: the words of a field are not split
				continue
			}
		}
		if fm, ok := strings.CutPrefix(r.Format.Style, formulaStyle); ok {
			f.Formula = fm
			out = append(out, Segment{Text: r.Text, Format: f}) // whole: the words of a formula are not split
			continue
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

// formFieldStyle likewise marks the run of a form field, followed by the field as JSON.
const formFieldStyle = "\x00formfield:"

// formulaStyle likewise marks the run of an inline formula, followed by the formula's notation.
const formulaStyle = "\x00formula:"

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

// tocBlock is the index of the block a table of contents at stream offset pos comes before: the first block that
// starts at or after it, moved back to the first cell of a table it would fall inside; past the last block when
// the contents are at the end.
func tocBlock(blocks []Block, pos int) int {
	for i, b := range blocks {
		if b.Start < 0 || b.Start < pos {
			continue
		}
		if b.Kind == TableCell {
			i -= b.TableRow*b.TableCols + b.TableCol
		}
		return max(i, 0)
	}
	return len(blocks)
}

// imageCredit is the line kept with a picture found in an image repository: its credit and the page it came from.
func imageCredit(b Block) string {
	switch {
	case b.ImageCredit != "" && b.ImageSourceURL != "":
		return b.ImageCredit + " - " + b.ImageSourceURL
	case b.ImageCredit != "":
		return b.ImageCredit
	}
	return b.ImageSourceURL
}
