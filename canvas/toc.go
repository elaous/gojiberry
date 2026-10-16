package canvas

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// A table of contents and an alphabetical index are block inline objects
// that read the document they sit in each time they are measured and
// drawn, so they stay current as the text changes. The table lists the
// outline (see Outline) with page numbers; the index lists the terms
// marked with Format.Index, each with the pages it is on.
//
// Page numbers come from the latest layout drawn, so they settle one
// redraw after a change.

const (
	tocLineH = 20.0
	tocFont  = "14px sans-serif"
	tocBold  = "bold 15px sans-serif"
)

// pageOfOffset returns the 1-based page of the line holding stream
// offset o in the latest drawn layout, or 0 if there is none.
func (f *TextFlow) pageOfOffset(o int) int {
	for _, ln := range f.lastLay.Lines {
		if o >= ln.Start && o <= ln.End {
			return ln.Page + 1
		}
	}
	return 0
}

// The looks of a table of contents (TOCObject.Style): a dotted leader from each heading to its page number, or the
// heading and the number alone.
const (
	TOCDots  = ""
	TOCPlain = "plain"
)

// TOCObject is a table of contents.
type TOCObject struct {
	// Title is the heading of the table ("Contents" if empty).
	Title string
	// Style is TOCDots (the default) or TOCPlain.
	Style string
	// Depth is the deepest heading level listed (1 to 3); 0 lists them all.
	Depth int
	flow  *TextFlow
	w     float64
}

func (o *TOCObject) bindFlow(f *TextFlow) { o.flow = f }

// Entries returns the headings the table lists now.
func (o *TOCObject) Entries() []OutlineEntry {
	if o.flow == nil {
		return nil
	}
	all := o.flow.Outline()
	if o.Depth <= 0 {
		return all
	}
	var out []OutlineEntry
	for _, e := range all {
		if e.Level <= o.Depth {
			out = append(out, e)
		}
	}
	return out
}

// EntryAt reports the heading whose row of the table, drawn with its top-left corner at (ox, oy), holds the point
// (x, y): the stream offset to go to. The row of the title and the space around the rows give none.
func (o *TOCObject) EntryAt(x, y, ox, oy float64) (int, bool) {
	if x < ox || x > ox+o.w || y < oy {
		return 0, false
	}
	row := int((y-oy)/tocLineH) - 1 // row 0 is the title
	entries := o.Entries()
	if row < 0 || row >= len(entries) {
		return 0, false
	}
	return entries[row].Start, true
}

// tocTargetAt is where a click at (x, y) on a table of contents or an index goes: the heading or the first mark of
// the term it lists there.
func (f *TextFlow) tocTargetAt(x, y float64) (int, bool) {
	for _, ln := range f.Layout().Lines {
		for _, po := range ln.Objects {
			switch o := po.Object.(type) {
			case *TOCObject:
				if off, ok := o.EntryAt(x, y, po.X, po.Y); ok {
					return off, true
				}
			case *IndexObject:
				if off, ok := o.EntryAt(x, y, po.X, po.Y); ok {
					return off, true
				}
			}
		}
	}
	return 0, false
}

// Measure implements InlineObject.
func (o *TOCObject) Measure(_ Painter, maxW, _ float64) (float64, float64) {
	o.w = maxW
	return maxW, tocLineH*float64(1+max(len(o.Entries()), 1)) + 10
}

// DrawAt implements InlineObject.
func (o *TOCObject) DrawAt(p Painter, x, y float64) {
	title := o.Title
	if title == "" {
		title = "Contents"
	}
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	p.SetFont(tocBold)
	p.SetFillStyle("#222222")
	ty := y + 15
	p.FillText(title, x, ty)
	entries := o.Entries()
	p.SetFont(tocFont)
	if len(entries) == 0 {
		p.SetFillStyle("#6b7280")
		p.FillText("No headings yet.", x, ty+tocLineH)
		return
	}
	for _, e := range entries {
		ty += tocLineH
		p.SetFillStyle("#2e3338")
		text := ellipsizeTo(p, e.Text, o.w-60-float64(e.Level-1)*16)
		tx := x + float64(e.Level-1)*16
		p.FillText(text, tx, ty)
		if pg := o.flow.pageOfOffset(e.Start); pg > 0 {
			label := strconv.Itoa(pg)
			lw := p.MeasureTextWidth(label)
			p.SetFillStyle("#6b7280")
			if o.Style == TOCDots {
				// dots from the end of the heading to the number, on a regular grid so they line up from row to row
				const step = 6.0
				dot := p.MeasureTextWidth(".")
				for dx := tx + p.MeasureTextWidth(text) + 6; dx+dot < x+o.w-lw-6; dx += step {
					g := x + math.Ceil((dx-x)/step)*step
					if g+dot >= x+o.w-lw-6 {
						break
					}
					p.FillText(".", g, ty)
				}
			}
			p.FillText(label, x+o.w-lw, ty)
		}
	}
}

// ellipsizeTo shortens s to fit w, with an ellipsis, if it is too wide.
func ellipsizeTo(p Painter, s string, w float64) string {
	if p.MeasureTextWidth(s) <= w {
		return s
	}
	return ellipsize(p, s, w)
}

// IndexEntry is a term and the pages it is marked on.
type IndexEntry struct {
	Term  string
	Pages []int
	// Start is the stream offset of the term's first mark: where a click on its row goes.
	Start int
}

// IndexObject is an alphabetical index of the terms marked with
// Format.Index.
type IndexObject struct {
	// Title is the heading of the index ("Index" if empty).
	Title string
	flow  *TextFlow
	w     float64
}

func (o *IndexObject) bindFlow(f *TextFlow) { o.flow = f }

// charIndexTerms lists the index term of every character of items.
func charIndexTerms(items []Item) []string {
	var out []string
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			for range v.Text {
				out = append(out, v.Format.Index)
			}
		case Inline:
			out = append(out, "")
		}
	}
	return out
}

// IndexEntries returns the marked terms, alphabetically (ignoring case),
// each with the pages it is on (empty before the first draw).
func (f *TextFlow) IndexEntries() []IndexEntry {
	byTerm := map[string]map[int]bool{}
	first := map[string]int{}
	var terms []string
	prev := ""
	for i, t := range charIndexTerms(f.items()) {
		if t == "" {
			prev = ""
			continue
		}
		if byTerm[t] == nil {
			byTerm[t] = map[int]bool{}
			first[t] = i
			terms = append(terms, t)
		}
		if t != prev { // one mark per stretch
			if pg := f.pageOfOffset(i); pg > 0 {
				byTerm[t][pg] = true
			}
		}
		prev = t
	}
	sort.Slice(terms, func(i, j int) bool {
		a, b := strings.ToLower(terms[i]), strings.ToLower(terms[j])
		if a != b {
			return a < b
		}
		return terms[i] < terms[j]
	})
	out := make([]IndexEntry, len(terms))
	for i, t := range terms {
		out[i].Term, out[i].Start = t, first[t]
		for pg := range byTerm[t] {
			out[i].Pages = append(out[i].Pages, pg)
		}
		sort.Ints(out[i].Pages)
	}
	return out
}

// Entries returns the terms the index lists now.
func (o *IndexObject) Entries() []IndexEntry {
	if o.flow == nil {
		return nil
	}
	return o.flow.IndexEntries()
}

// EntryAt reports the term whose row, drawn with its top-left corner at (ox, oy), holds the point (x, y): the stream
// offset of its first mark. The row of the title and the space around the rows give none.
func (o *IndexObject) EntryAt(x, y, ox, oy float64) (int, bool) {
	if x < ox || x > ox+o.w || y < oy {
		return 0, false
	}
	row := int((y-oy)/tocLineH) - 1 // row 0 is the title
	entries := o.Entries()
	if row < 0 || row >= len(entries) {
		return 0, false
	}
	return entries[row].Start, true
}

// Measure implements InlineObject.
func (o *IndexObject) Measure(_ Painter, maxW, _ float64) (float64, float64) {
	o.w = maxW
	return maxW, tocLineH*float64(1+max(len(o.Entries()), 1)) + 10
}

// DrawAt implements InlineObject.
func (o *IndexObject) DrawAt(p Painter, x, y float64) {
	title := o.Title
	if title == "" {
		title = "Index"
	}
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	p.SetFont(tocBold)
	p.SetFillStyle("#222222")
	ty := y + 15
	p.FillText(title, x, ty)
	entries := o.Entries()
	p.SetFont(tocFont)
	if len(entries) == 0 {
		p.SetFillStyle("#6b7280")
		p.FillText("No index entries yet.", x, ty+tocLineH)
		return
	}
	for _, e := range entries {
		ty += tocLineH
		p.SetFillStyle("#2e3338")
		p.FillText(ellipsizeTo(p, e.Term, o.w-80), x, ty)
		var pages []string
		for _, pg := range e.Pages {
			pages = append(pages, strconv.Itoa(pg))
		}
		label := strings.Join(pages, ", ")
		p.SetFillStyle("#6b7280")
		p.FillText(label, x+o.w-p.MeasureTextWidth(label), ty)
	}
}

var (
	_ InlineObject = (*TOCObject)(nil)
	_ InlineObject = (*IndexObject)(nil)
	_ flowBinder   = (*TOCObject)(nil)
	_ flowBinder   = (*IndexObject)(nil)
)
