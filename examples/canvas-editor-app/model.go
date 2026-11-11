package main

import (
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/elaous/gojiberry/buffer"
)

type charFormat struct {
	Bold   bool
	Italic bool
}

type char struct {
	Rune rune
	Fmt  charFormat
}

type cursor struct {
	Pos, Anchor int
}

type document struct {
	buf buffer.GapBuffer[char]

	Cursor, Anchor int
	ExtraCursors   []cursor

	hist history // undo and redo (history.go)
}

// Len returns the number of characters in the document.
func (d *document) Len() int { return d.buf.Len() }

func (d *document) runeAt(i int) rune { return d.buf.At(i).Rune }

func (d *document) fmtAt(i int) charFormat { return d.buf.At(i).Fmt }

func (d *document) setFmtAt(i int, f charFormat) {
	c := d.buf.At(i)
	c.Fmt = f
	d.buf.Set(i, c)
}

// String returns the document's full text.
func (d *document) String() string {
	n := d.buf.Len()
	runes := make([]rune, n)
	for i := 0; i < n; i++ {
		runes[i] = d.runeAt(i)
	}
	return string(runes)
}

func (d *document) selection() (lo, hi int) {
	if d.Anchor < d.Cursor {
		return d.Anchor, d.Cursor
	}
	return d.Cursor, d.Anchor
}

func (d *document) hasSelection() bool {
	lo, hi := d.selection()
	return hi > lo
}

func (d *document) cursors() []cursor {
	out := make([]cursor, 0, 1+len(d.ExtraCursors))
	out = append(out, cursor{Pos: d.Cursor, Anchor: d.Anchor})
	out = append(out, d.ExtraCursors...)
	return out
}

func (d *document) setCursors(cs []cursor) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].Pos < cs[j].Pos })
	merged := cs[:0]
	for _, c := range cs {
		if n := len(merged); n > 0 && merged[n-1].Pos == c.Pos {
			continue
		}
		merged = append(merged, c)
	}
	d.Cursor, d.Anchor = merged[0].Pos, merged[0].Anchor
	if len(merged) > 1 {
		d.ExtraCursors = append([]cursor{}, merged[1:]...)
	} else {
		d.ExtraCursors = nil
	}
}

func (d *document) addCursorAt(pos int) {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	pos = clamp(pos, 0, d.Len())
	d.setCursors(append(d.cursors(), cursor{Pos: pos, Anchor: pos}))
}

func (d *document) resetCursorTo(pos int) {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	pos = clamp(pos, 0, d.Len())
	d.Cursor, d.Anchor = pos, pos
	d.ExtraCursors = nil
}

func (d *document) collapseCursors() {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	d.ExtraCursors = nil
}

func (d *document) selectAll() {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	d.Anchor, d.Cursor = 0, d.Len()
	d.ExtraCursors = nil
}

func (d *document) moveCursors(newPos func(pos int) int, extend bool) {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	cs := d.cursors()
	for i := range cs {
		p := clamp(newPos(cs[i].Pos), 0, d.Len())
		cs[i].Pos = p
		if !extend {
			cs[i].Anchor = p
		}
	}
	d.setCursors(cs)
}

func (d *document) forEachCursorDescending(edit func(pos, anchor int) (int, int)) {
	cs := d.cursors()
	sort.Slice(cs, func(i, j int) bool {
		return max(cs[i].Pos, cs[i].Anchor) > max(cs[j].Pos, cs[j].Anchor)
	})
	for i := range cs {
		before := d.Len()
		cs[i].Pos, cs[i].Anchor = edit(cs[i].Pos, cs[i].Anchor)
		delta := d.Len() - before
		if delta != 0 {
			for j := 0; j < i; j++ {
				cs[j].Pos += delta
				cs[j].Anchor += delta
			}
		}
	}
	d.setCursors(cs)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (d *document) insert(text string) {
	typing := text != "\n" && utf8.RuneCountInString(text) <= 1
	if typing && d.hist.typingAt == d.Cursor+1 && len(d.ExtraCursors) == 0 && !d.hasSelection() {
		d.insertNow(text)
		d.hist.typingAt = d.Cursor + 1
		return
	}
	d.edit(func() { d.insertNow(text) })
	if typing {
		d.hist.typingAt = d.Cursor + 1
	}
}

func (d *document) insertNow(text string) {
	d.forEachCursorDescending(func(pos, anchor int) (int, int) {
		lo, hi := pos, anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi > lo {
			d.buf.DeleteAt(lo, hi-lo)
			pos = lo
		}
		format := charFormat{}
		if pos > 0 {
			format = d.fmtAt(pos - 1)
		}
		items := make([]char, 0, utf8.RuneCountInString(text))
		for _, r := range text {
			items = append(items, char{Rune: r, Fmt: format})
		}
		d.buf.InsertAt(pos, items...)
		newPos := pos + len(items)
		return newPos, newPos
	})
}

func (d *document) backspace() { d.edit(d.backspaceNow) }

func (d *document) backspaceNow() {
	d.forEachCursorDescending(func(pos, anchor int) (int, int) {
		lo, hi := pos, anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi > lo {
			d.buf.DeleteAt(lo, hi-lo)
			return lo, lo
		}
		if pos == 0 {
			return pos, anchor
		}
		d.buf.DeleteAt(pos-1, 1)
		return pos - 1, pos - 1
	})
}

func (d *document) delete() { d.edit(d.deleteNow) }

func (d *document) deleteNow() {
	d.forEachCursorDescending(func(pos, anchor int) (int, int) {
		lo, hi := pos, anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi > lo {
			d.buf.DeleteAt(lo, hi-lo)
			return lo, lo
		}
		if pos >= d.Len() {
			return pos, anchor
		}
		d.buf.DeleteAt(pos, 1)
		return pos, pos
	})
}

func (d *document) cutSelections() { d.edit(d.cutNow) }

func (d *document) cutNow() {
	d.forEachCursorDescending(func(pos, anchor int) (int, int) {
		lo, hi := pos, anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi > lo {
			d.buf.DeleteAt(lo, hi-lo)
			return lo, lo
		}
		return pos, anchor
	})
}

type formatKind int

const (
	formatBold formatKind = iota
	formatItalic
)

func (d *document) toggleSelectionFormat(lo, hi int, kind formatKind) {
	if hi <= lo || lo < 0 || hi > d.Len() {
		return
	}
	first := d.fmtAt(lo)
	var target bool
	if kind == formatBold {
		target = !first.Bold
	} else {
		target = !first.Italic
	}
	for i := lo; i < hi; i++ {
		f := d.fmtAt(i)
		if kind == formatBold {
			f.Bold = target
		} else {
			f.Italic = target
		}
		d.setFmtAt(i, f)
	}
}

func (d *document) toggleSelectionFormatAllCursors(kind formatKind) {
	d.edit(func() { d.toggleFormatNow(kind) })
}

func (d *document) toggleFormatNow(kind formatKind) {
	for _, c := range d.cursors() {
		lo, hi := c.Pos, c.Anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		d.toggleSelectionFormat(lo, hi, kind)
	}
}

func (d *document) transformCaseRange(lo, hi int, upper bool) {
	for i := lo; i < hi; i++ {
		c := d.buf.At(i)
		if upper {
			c.Rune = unicode.ToUpper(c.Rune)
		} else {
			c.Rune = unicode.ToLower(c.Rune)
		}
		d.buf.Set(i, c)
	}
}

func (d *document) transformSelectionCaseAllCursors(upper bool) {
	d.edit(func() { d.transformCaseNow(upper) })
}

func (d *document) transformCaseNow(upper bool) {
	for _, c := range d.cursors() {
		lo, hi := c.Pos, c.Anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		d.transformCaseRange(lo, hi, upper)
	}
}
