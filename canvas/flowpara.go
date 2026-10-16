package canvas

import (
	"slices"
	"strings"
)

// Paragraph formatting in a TextFlow: see ParaFormat. The format of a
// paragraph is an item of the content stream sitting at its start;
// ApplyParagraph creates it when needed and removes it again when it
// ends up empty.

// ParagraphEditor is implemented by a target that has paragraphs with
// formats (a TextFlow); the paragraph features need it.
type ParagraphEditor interface {
	// ApplyParagraph applies fn to the format of every paragraph the
	// cursors touch, and reports whether any changed.
	ApplyParagraph(fn func(*ParaFormat)) bool
	// ParagraphFormats returns the format of every paragraph the cursors
	// touch, in order.
	ParagraphFormats() []ParaFormat
}

var _ ParagraphEditor = (*TextFlow)(nil)

// paraBounds returns the paragraph around offset o: its start, and the
// offset of its '\n' (or the end of the text).
func paraBounds(rs []rune, o int) (ps, pe int) {
	o = min(max(o, 0), len(rs))
	return lineStart(rs, o), lineEnd(rs, o)
}

// touchedParagraphs lists the start offsets of the paragraphs the
// cursors touch (a selection ending at the very start of a paragraph
// does not touch it).
func touchedParagraphs(rs []rune, cs []Cursor) []int {
	var starts []int
	for _, c := range normalize(cs) {
		from, to := c.Range()
		if to > from && to <= len(rs) && rs[to-1] == '\n' {
			to--
		}
		for ps := lineStart(rs, from); ; {
			if len(starts) == 0 || starts[len(starts)-1] != ps {
				starts = append(starts, ps)
			}
			pe := lineEnd(rs, ps)
			if pe >= to || pe >= len(rs) {
				break
			}
			ps = pe + 1
		}
	}
	return starts
}

// ParagraphFormats implements ParagraphEditor.
func (f *TextFlow) ParagraphFormats() []ParaFormat {
	d := f.flatten()
	var out []ParaFormat
	for _, ps := range touchedParagraphs(d.runes, f.Cursors()) {
		_, pe := paraBounds(d.runes, ps)
		out = append(out, d.paraIn(ps, pe))
	}
	return out
}

// ParagraphAt returns the format of the paragraph containing offset o.
func (f *TextFlow) ParagraphAt(o int) ParaFormat {
	d := f.flatten()
	ps, pe := paraBounds(d.runes, o)
	return d.paraIn(ps, pe)
}

// paraIndex returns the index in f.Items of the ParaFormat of the
// paragraph [ps, pe], or -1.
func (f *TextFlow) paraIndex(ps, pe int) int {
	off := 0
	for i, it := range f.Items {
		switch v := it.(type) {
		case Chars:
			off += len([]rune(v.Text))
		case Inline:
			off++
		case ParaFormat:
			if off >= ps && off <= pe {
				return i
			}
		}
	}
	return -1
}

// setParagraph applies fn to the format of the paragraph [ps, pe].
func (f *TextFlow) setParagraph(ps, pe int, fn func(*ParaFormat)) bool {
	f.materialize()
	idx := f.paraIndex(ps, pe)
	if idx < 0 {
		f.replaceRange(ps, ps, []Item{ParaFormat{}}, true)
		idx = f.paraIndex(ps, pe)
		if idx < 0 { // cannot happen: the item was just inserted inside [ps, pe]
			return false
		}
	}
	before := f.Items[idx].(ParaFormat)
	after := before
	fn(&after)
	switch {
	case after.IsZero():
		f.Items = slices.Delete(slices.Clone(f.Items), idx, idx+1)
	case after != before:
		f.Items[idx] = after
	}
	return after != before
}

// ApplyParagraph implements ParagraphEditor.
func (f *TextFlow) ApplyParagraph(fn func(*ParaFormat)) bool {
	changed := false
	rs := f.flatten().runes
	starts := touchedParagraphs(rs, f.Cursors())
	for _, ps := range starts {
		_, pe := paraBounds(f.flatten().runes, ps)
		if f.setParagraph(ps, pe, fn) {
			changed = true
		}
	}
	if changed {
		f.changed()
	}
	return changed
}

// ToggleCheckAt ticks or unticks the check box at (x, y), if there is
// one.
func (f *TextFlow) ToggleCheckAt(x, y float64) bool {
	x, y = f.vpt(x, y)
	return f.toggleCheckAt(x, y)
}

// toggleCheckAt is ToggleCheckAt for a point in the plane the text is laid out in.
func (f *TextFlow) toggleCheckAt(x, y float64) bool {
	for _, ln := range f.Layout().Lines {
		m := ln.Marker
		if m == nil || !m.Check || x < m.X || x > m.X+m.W || y < ln.Y || y > ln.Y+ln.H {
			continue
		}
		rs := f.flatten().runes
		ps, pe := paraBounds(rs, ln.Start)
		if f.setParagraph(ps, pe, func(p *ParaFormat) { p.List.Checked = !p.List.Checked }) {
			f.changed()
		}
		return true
	}
	return false
}

// enter handles Enter: in a formatted paragraph the new one continues
// the format (a list gets its next item), and Enter on an empty list
// item ends the list instead of adding a line.
func (f *TextFlow) enter() bool {
	if len(f.extra) > 0 || f.HasSelection() {
		return f.InsertText("\n")
	}
	if f.markdownTableOnEnter() {
		return true
	}
	rs := f.flatten().runes
	ps, pe := paraBounds(rs, f.sel.caret)
	pf := f.flatten().paraIn(ps, pe)
	if pf.List.Kind != ListNone && ps == pe {
		f.setParagraph(ps, pe, func(p *ParaFormat) { p.List = ListStyle{} })
		f.changed()
		return true
	}
	ok := f.InsertText("\n")
	if ok && !pf.IsZero() {
		next := pf
		next.DropCap = 0
		next.SpaceBefore = 0
		next.List.Checked, next.List.Start = false, 0
		rs = f.flatten().runes
		nps, npe := paraBounds(rs, f.sel.caret)
		f.setParagraph(nps, npe, func(p *ParaFormat) { *p = next })
	}
	return ok
}

// indentList changes the nesting level of the list item at the caret
// (Tab and Shift+Tab); it reports whether there was one.
func (f *TextFlow) indentList(delta int) bool {
	if len(f.extra) > 0 {
		return false
	}
	formats := f.ParagraphFormats()
	if len(formats) == 0 || formats[0].List.Kind == ListNone {
		return false
	}
	f.ApplyParagraph(func(p *ParaFormat) {
		if p.List.Kind != ListNone {
			p.List.Level = max(p.List.Level+delta, 0)
		}
	})
	return true
}

// markdownTableOnEnter turns three lines typed as a Markdown table into a table when Enter ends the third:
//
//	| A | B |
//	|---|---|
//	| a | b |
//
// A header row, the row of dashes under it and one row of cells, all with the same number of columns, is the least that
// makes a table. The lines are replaced by the table and the caret goes to the paragraph after it. Cells keep their
// text, so a cell that starts with = is a formula. It reports whether it made a table; otherwise Enter is untouched.
func (f *TextFlow) markdownTableOnEnter() bool {
	rs := f.flatten().runes
	c := f.sel.caret
	ps3, pe3 := paraBounds(rs, c)
	if c != pe3 || ps3 < 4 || rs[ps3-1] != '\n' {
		return false
	}
	ps2 := lineStart(rs, ps3-1)
	if ps2 < 2 || rs[ps2-1] != '\n' {
		return false
	}
	ps1 := lineStart(rs, ps2-1)
	lines := [3]string{string(rs[ps1 : ps2-1]), string(rs[ps2 : ps3-1]), string(rs[ps3:pe3])}
	var cells [3][]string
	for i, l := range lines {
		if !strings.Contains(l, "|") || strings.ContainsRune(l, objectRune) {
			return false
		}
		cells[i] = splitPipes(l)
	}
	if len(cells[0]) == 0 || len(cells[0]) != len(cells[1]) || len(cells[0]) != len(cells[2]) {
		return false
	}
	t, ok := TableFromMarkdown(strings.Join(lines[:], "\n"))
	if !ok {
		return false
	}
	f.Replace([]Edit{{From: ps1, To: pe3, Items: []Item{Inline{Object: NewTableObject(t), Block: true}}}})
	f.SetCursors([]Cursor{At(ps1 + 1)})
	return f.InsertText("\n")
}
