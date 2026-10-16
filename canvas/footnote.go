package canvas

import "strconv"

// Footnotes: a FootnoteRef (an inline object, so it moves, copies and
// deletes with the text) marks the place, drawn as a small raised number;
// the note's text is in a FootnoteSet under the ref's ID. The flow numbers
// the refs in text order, and draws each page's notes in a band reserved at
// the bottom of that page, under a short rule. A page gives its notes exactly the room they need (the text above
// gives way, so a mark and its note stay on the same page), up to a cap (FootnoteBand, by default 40 % of the
// page); a note that does not fit in the cap goes on at the foot of the next page. A page always holds at least the
// start of the notes of its marks. Nothing is cut: no note ends in an ellipsis.

// Footnote is a note's text.
type Footnote struct {
	ID   string
	Text string // plain text, or Markdown (shown with its marks stripped on the page)
}

// FootnoteSet holds a document's footnotes.
type FootnoteSet struct {
	// IDPrefix is CommentSet.IDPrefix's counterpart for footnote IDs.
	IDPrefix string

	list []Footnote
	next int
}

// NewFootnoteSet returns a set holding notes.
func NewFootnoteSet(notes ...Footnote) *FootnoteSet {
	s := &FootnoteSet{}
	for _, n := range notes {
		s.Add(n)
	}
	return s
}

// Add stores n, giving it an ID ("n1", "n2", ...) if it has none, and
// returns it. An existing ID replaces that note.
func (s *FootnoteSet) Add(n Footnote) Footnote {
	if rs := []rune(n.Text); len(rs) > MaxFootnoteRunes {
		n.Text = string(rs[:MaxFootnoteRunes])
	}
	if n.ID == "" {
		for {
			s.next++
			n.ID = s.IDPrefix + "n" + strconv.Itoa(s.next)
			if _, taken := s.Get(n.ID); !taken {
				break
			}
		}
	}
	for i := range s.list {
		if s.list[i].ID == n.ID {
			s.list[i] = n
			return n
		}
	}
	s.list = append(s.list, n)
	return n
}

// Get returns the note with the ID.
func (s *FootnoteSet) Get(id string) (Footnote, bool) {
	if s != nil {
		for _, n := range s.list {
			if n.ID == id {
				return n, true
			}
		}
	}
	return Footnote{}, false
}

// Delete removes the note with the ID.
func (s *FootnoteSet) Delete(id string) bool {
	if s != nil {
		for i := range s.list {
			if s.list[i].ID == id {
				s.list = append(s.list[:i], s.list[i+1:]...)
				return true
			}
		}
	}
	return false
}

// List returns the notes in the order they were added.
func (s *FootnoteSet) List() []Footnote {
	if s == nil {
		return nil
	}
	return append([]Footnote(nil), s.list...)
}

// Len is the number of notes.
func (s *FootnoteSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.list)
}

// FootnoteRef is the footnote mark in the text: use it as *FootnoteRef in
// an Inline item. N is set by the flow (1, 2, ... in text order).
type FootnoteRef struct {
	ID string
	N  int
	w  float64
}

// Measure implements InlineObject.
func (r *FootnoteRef) Measure(p Painter, _, _ float64) (float64, float64) {
	p.SetFont(footnoteMarkFont)
	r.w = p.MeasureTextWidth(strconv.Itoa(max(r.N, 1))) + 2
	return r.w, 8 // short: it must not make the line taller
}

// DrawAt implements InlineObject.
func (r *FootnoteRef) DrawAt(p Painter, x, y float64) {
	p.SetFont(footnoteMarkFont)
	p.SetFillStyle(LinkColor)
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	p.FillText(strconv.Itoa(max(r.N, 1)), x+1, y+2)
}

var _ InlineObject = (*FootnoteRef)(nil)

const (
	footnoteMarkFont = "bold 10px sans-serif"
	footnoteFont     = "11px sans-serif"
	footnoteLineH    = 14.0
	// footnotePad is the room above a page's notes (the rule) and below them.
	footnotePad = 8.0
	// footnoteCapShare is the share of a page's text area its notes may take when FootnoteBand is not set.
	footnoteCapShare = 0.4
	// MaxFootnoteRunes is the longest a note can be (FootnoteSet.Add keeps the first MaxFootnoteRunes characters).
	MaxFootnoteRunes = 1000
)

// footnoteBandOf is the height kept at the bottom of page for its notes: none when it has none. It is set by the
// layout (see Layout), which places the notes of each page and gives the text above only what is left.
func (f *TextFlow) footnoteBandOf(page int) float64 {
	if f.Footnotes.Len() == 0 || page < 0 || page >= len(f.fnBands) {
		return 0
	}
	return f.fnBands[page]
}

// FootnoteEntry is a note that is referenced from the text.
type FootnoteEntry struct {
	N      int // its number
	ID     string
	Text   string
	Offset int // where its mark is in the text stream
}

// FootnoteList returns the notes whose marks are in the text, in order
// (a note whose mark was deleted is not listed).
func (f *TextFlow) FootnoteList() []FootnoteEntry {
	var out []FootnoteEntry
	off := 0
	for _, it := range f.items() {
		switch v := it.(type) {
		case Chars:
			off += len([]rune(v.Text))
		case Inline:
			if ref, ok := v.Object.(*FootnoteRef); ok {
				if n, ok := f.Footnotes.Get(ref.ID); ok {
					out = append(out, FootnoteEntry{N: len(out) + 1, ID: ref.ID, Text: n.Text, Offset: off})
				}
			}
			off++
		}
	}
	return out
}

// drawFootnotes draws each page's notes in the band the layout gave them.
func (f *TextFlow) drawFootnotes(lay Layout) {
	if f.Painter == nil || len(f.fnLines) == 0 {
		return
	}
	p := f.Painter
	for pg, lines := range f.fnLines {
		if len(lines) == 0 {
			continue
		}
		x, y, w, h := f.physRect(pg)
		y0 := y + h + 4
		p.SetFillStyle("#c4c7cd")
		p.FillRect(x, y0, min(w, 80), 1)
		p.SetFont(footnoteFont)
		p.SetFillStyle("#4b5563")
		p.SetTextAlign("left")
		p.SetTextBaseline("alphabetic")
		ty := y0 + 4 + 10
		for _, l := range lines {
			p.FillText(l.text, x, ty)
			ty += footnoteLineH
		}
	}
}

// fnLine is one line of a page's notes and the note it belongs to.
type fnLine struct{ text, id string }

// footnoteAt is the note whose line on a page's band holds the point (x, y), if any: a click there edits the note
// on the page itself.
func (f *TextFlow) footnoteAt(x, y float64) (string, bool) {
	for pg, lines := range f.fnLines {
		if len(lines) == 0 {
			continue
		}
		px, py, pw, ph := f.physRect(pg)
		ty := py + ph + 4 + 4 // the first line's top, below the rule
		if x < px || x > px+pw || y < ty || y >= ty+footnoteLineH*float64(len(lines)) {
			continue
		}
		return lines[int((y-ty)/footnoteLineH)].id, true
	}
	return "", false
}

// footnotePlan works out, for the layout lay, which lines of notes each page shows and how tall its band is. The
// notes of the marks on a page come after what the pages before could not hold; a page shows as many lines as its
// cap allows (at least two) and passes the rest on; the last page shows everything up to four fifths of itself.
func (f *TextFlow) footnotePlan(lay Layout) (bands []float64, lines [][]fnLine) {
	n := f.pageCount()
	bands, lines = make([]float64, n), make([][]fnLine, n)
	pageOf := map[int]int{}
	for _, ln := range lay.Lines {
		for _, o := range ln.Objects {
			pageOf[o.Offset] = ln.Page
		}
	}
	byPage := map[int][]FootnoteEntry{}
	for _, e := range f.FootnoteList() {
		if pg, ok := pageOf[e.Offset]; ok {
			byPage[pg] = append(byPage[pg], e)
		}
	}
	p := f.Painter
	if p != nil {
		p.SetFont(footnoteFont)
	}
	var carry []fnLine
	for pg := range n {
		_, _, w, h := f.pageContent(pg)
		queue := carry
		carry = nil
		for _, e := range byPage[pg] {
			text := strconv.Itoa(e.N) + " " + CommentSummary(e.Text, 0)
			if p == nil {
				queue = append(queue, fnLine{text, e.ID})
				continue
			}
			for _, l := range wrapToWidth(p, text, w) {
				queue = append(queue, fnLine{l, e.ID})
			}
		}
		if len(queue) == 0 {
			continue
		}
		capH := f.FootnoteBand
		if capH <= 0 {
			capH = footnoteCapShare * h
		}
		if pg == n-1 {
			capH = max(capH, 0.8*h)
		}
		most := max(int((capH-footnotePad)/footnoteLineH), 2)
		take := min(len(queue), most)
		lines[pg], carry = queue[:take], queue[take:]
		bands[pg] = footnotePad + float64(take)*footnoteLineH
	}
	return bands, lines
}

// pageContent is the rectangle of page's text area before the notes take their share.
func (f *TextFlow) pageContent(page int) (x, y, w, h float64) {
	if f.Pages != nil {
		return f.Pages.ContentRect(page)
	}
	return f.X, f.Y, f.Width, f.Height
}

// wrapToWidth breaks text into lines no wider than w (in the painter's
// current font), at spaces.
func wrapToWidth(p Painter, text string, w float64) []string {
	var lines []string
	cur := ""
	for _, word := range splitWords(text) {
		try := word
		if cur != "" {
			try = cur + " " + word
		}
		if cur != "" && p.MeasureTextWidth(try) > w {
			lines = append(lines, cur)
			cur = word
		} else {
			cur = try
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func splitWords(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// ellipsize shortens l so l+"…" fits in w.
func ellipsize(p Painter, l string, w float64) string {
	rs := []rune(l)
	for len(rs) > 0 && p.MeasureTextWidth(string(rs)+"…") > w {
		rs = rs[:len(rs)-1]
	}
	return string(rs) + "…"
}
