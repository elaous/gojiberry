package canvas

import "strings"

// flowSel is a TextFlow's text selection: stream offsets, anchor to
// caret (so it may run backwards).
type flowSel struct {
	anchor, caret int
	after         bool // the caret is after the breaks at its offset (see Cursor.After)
	dragging      bool
}

// Hitter is implemented by overlapping objects (images, floating text,
// tables) that want presses on them kept away from the text beneath.
type Hitter interface{ ContainsPoint(x, y float64) bool }

// Selection returns the selected range of the text stream, [from, to);
// from == to means nothing is selected.
func (f *TextFlow) Selection() (from, to int) {
	return min(f.sel.anchor, f.sel.caret), max(f.sel.anchor, f.sel.caret)
}

// SetSelection selects [from, to) of the text stream.
func (f *TextFlow) SetSelection(from, to int) { f.sel.anchor, f.sel.caret = from, to }

// ClearSelection selects nothing.
func (f *TextFlow) ClearSelection() { f.sel = flowSel{} }

// HasSelection reports whether any text is selected.
func (f *TextFlow) HasSelection() bool {
	from, to := f.Selection()
	return from != to
}

// SelectAll selects all the text.
func (f *TextFlow) SelectAll() {
	f.Layout()
	f.sel = flowSel{anchor: 0, caret: len(f.data.runes)}
}

// SelectedText returns the selected text (an inline object contributes
// nothing).
func (f *TextFlow) SelectedText() string {
	if !f.HasSelection() {
		return ""
	}
	f.Layout()
	from, to := f.Selection()
	from, to = max(from, 0), min(to, len(f.data.runes))
	var b strings.Builder
	for _, r := range f.data.runes[from:to] {
		if r != objectRune {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CommonFormat returns the formatting shared by all the selected text --
// a field is left unset where the selection varies (the minimum common
// formatting), which is what the format painter copies.
func (f *TextFlow) CommonFormat() Format {
	f.Layout()
	from, to := f.Selection()
	from, to = max(from, 0), min(to, len(f.data.runes))
	var fs []Format
	seen := map[int]bool{}
	for i := from; i < to; i++ {
		id := f.data.rs[i]
		if !seen[id] && f.data.runes[i] != objectRune {
			seen[id] = true
			fs = append(fs, f.data.formats[id])
		}
	}
	return CommonFormat(fs...)
}

// obstructed reports whether (x, y) is on something laid over the text.
func (f *TextFlow) obstructed(x, y float64) bool {
	for _, o := range f.obstacles {
		if h, ok := o.(Hitter); ok && h.ContainsPoint(x, y) {
			return true
		}
		if x0, x1, ok := o.Exclusion(y, y+1); ok && x >= x0 && x < x1 {
			return true
		}
	}
	return false
}

// inText reports whether (x, y) is inside some page's text area.
func (f *TextFlow) inText(x, y float64) bool {
	for i := range f.pageCount() {
		px, py, pw, ph := f.pageRect(i)
		if inRect(x, y, [4]float64{px, py, pw, ph}) {
			return true
		}
	}
	return false
}

// beginSelect starts a selection drag at (x, y) if it is on the text; a
// press elsewhere clears the selection. It returns whether anything
// visible changed.
func (f *TextFlow) beginSelect(x, y float64) bool {
	had := f.HasSelection()
	if f.Painter == nil || f.obstructed(x, y) || !f.inText(x, y) {
		f.ClearSelection()
		f.stopEditing()
		return had
	}
	o, after := f.offsetAt(x, y)
	f.sel = flowSel{anchor: o, caret: o, after: after, dragging: true}
	f.extra, f.pending, f.caretOff = nil, Format{}, false
	f.editing = f.mode == ModeEdit
	return had || f.editing
}

// DoubleClick selects the word at (x, y).
func (f *TextFlow) DoubleClick(x, y float64) bool {
	if f.Painter == nil || f.obstructed(x, y) || !f.inText(x, y) {
		return false
	}
	o := f.OffsetAt(x, y)
	f.Layout()
	r := f.data.runes
	if len(r) == 0 {
		return false
	}
	o = min(o, len(r)-1)
	from, to := o, o
	if isIdentRune(r[o]) {
		for from > 0 && isIdentRune(r[from-1]) {
			from--
		}
		for to < len(r) && isIdentRune(r[to]) {
			to++
		}
	} else {
		to = o + 1
	}
	f.sel = flowSel{anchor: from, caret: to}
	return true
}

// drawSelection tints the selected text of every cursor, line by line.
func (f *TextFlow) drawSelection(lay Layout) {
	p := f.Painter
	p.SetFillStyle(f.ChromeStyle.SelectColor)
	for _, c := range f.Cursors() {
		from, to := c.Range()
		if from == to {
			continue
		}
		for _, ln := range lay.Lines {
			s, e := max(from, ln.Start), min(to, ln.End)
			if s >= e {
				continue
			}
			x0 := ln.X + f.data.measure(ln.Start, s)
			x1 := ln.X + f.data.measure(ln.Start, e)
			p.FillRect(x0, ln.Y, x1-x0, ln.H)
		}
	}
}

// SetFormatClipboard implements FormatClipboardSetter.
func (f *TextFlow) SetFormatClipboard(c *FormatClipboard) { f.formats = c }

var (
	_ Copier                = (*TextFlow)(nil)
	_ Commander             = (*TextFlow)(nil)
	_ Cutter                = (*TextFlow)(nil)
	_ Paster                = (*TextFlow)(nil)
	_ TextInserter          = (*TextFlow)(nil)
	_ KeyHandler            = (*TextFlow)(nil)
	_ Editable              = (*TextFlow)(nil)
	_ ModMouseDowner        = (*TextFlow)(nil)
	_ FormatClipboardSetter = (*TextFlow)(nil)
	_ Focuser               = (*TextFlow)(nil)
	_ DoubleClicker         = (*TextFlow)(nil)
)
