package canvas

import "strconv"

// Footnotes: a FootnoteRef (an inline object, so it moves, copies and
// deletes with the text) marks the place, drawn as a small raised number;
// the note's text is in a FootnoteSet under the ref's ID. The flow numbers
// the refs in text order, and draws each page's notes in a band reserved at
// the bottom of that page (FootnoteBand), under a short rule. A note too
// long for the band is cut with an ellipsis.

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
	// DefaultFootnoteBand is the room at the bottom of each page kept for
	// its notes when TextFlow.FootnoteBand is not set.
	DefaultFootnoteBand = 64.0
)

// footnoteBand is the height reserved at the bottom of every page: none
// when there are no notes.
func (f *TextFlow) footnoteBand() float64 {
	if f.Footnotes.Len() == 0 {
		return 0
	}
	if f.FootnoteBand > 0 {
		return f.FootnoteBand
	}
	return DefaultFootnoteBand
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

// drawFootnotes draws each page's notes in its reserved band.
func (f *TextFlow) drawFootnotes(lay Layout) {
	band := f.footnoteBand()
	if band == 0 || f.Painter == nil {
		return
	}
	byPage := map[int][]FootnoteEntry{}
	list := f.FootnoteList()
	pageOf := map[int]int{}
	for _, ln := range lay.Lines {
		for _, o := range ln.Objects {
			pageOf[o.Offset] = ln.Page
		}
	}
	for _, e := range list {
		if pg, ok := pageOf[e.Offset]; ok {
			byPage[pg] = append(byPage[pg], e)
		}
	}
	p := f.Painter
	for pg, notes := range byPage {
		x, y, w, h := f.physRect(pg)
		y0 := y + h + 4
		p.SetFillStyle("#c4c7cd")
		p.FillRect(x, y0, min(w, 80), 1)
		p.SetFont(footnoteFont)
		p.SetFillStyle("#4b5563")
		p.SetTextAlign("left")
		p.SetTextBaseline("alphabetic")
		maxLines := int((band - 8) / footnoteLineH)
		ty := y0 + 4 + 10
		lines := 0
		for _, n := range notes {
			text := strconv.Itoa(n.N) + " " + CommentSummary(n.Text, 0)
			for _, l := range wrapToWidth(p, text, w) {
				if lines >= maxLines {
					break
				}
				if lines == maxLines-1 && l != "" {
					l = ellipsize(p, l, w)
				}
				p.FillText(l, x, ty)
				ty += footnoteLineH
				lines++
			}
		}
	}
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
