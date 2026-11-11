package main

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/buffer"
)

const (
	maxUndo      = 200
	maxUndoChars = 2_000_000
)

type snapshot struct {
	chars          []char
	cursor, anchor int
	extras         []cursor
}

type history struct {
	undo, redo []snapshot
	typingAt   int
}

func (d *document) snap() snapshot {
	return snapshot{chars: d.buf.ToSlice(), cursor: d.Cursor, anchor: d.Anchor, extras: append([]cursor(nil), d.ExtraCursors...)}
}

func (d *document) restore(s snapshot) {
	d.buf = buffer.GapBuffer[char]{}
	d.buf.InsertAt(0, s.chars...)
	d.Cursor, d.Anchor = s.cursor, s.anchor
	d.ExtraCursors = append([]cursor(nil), s.extras...)
}

func (d *document) edit(op func()) {
	before := d.snap()
	d.hist.typingAt = 0
	op()
	if sameSnapshot(before, d.snap()) {
		return
	}
	d.hist.undo = append(d.hist.undo, before)
	d.hist.redo = nil
	d.trimHistory()
}

func (d *document) trimHistory() {
	total := 0
	for _, s := range d.hist.undo {
		total += len(s.chars)
	}
	drop := 0
	for drop < len(d.hist.undo)-1 && (len(d.hist.undo)-drop > maxUndo || total > maxUndoChars) {
		total -= len(d.hist.undo[drop].chars)
		drop++
	}
	if drop > 0 {
		d.hist.undo = append([]snapshot(nil), d.hist.undo[drop:]...)
	}
}

func sameSnapshot(a, b snapshot) bool {
	if a.cursor != b.cursor || a.anchor != b.anchor || len(a.chars) != len(b.chars) || len(a.extras) != len(b.extras) {
		return false
	}
	for i := range a.chars {
		if a.chars[i] != b.chars[i] {
			return false
		}
	}
	for i := range a.extras {
		if a.extras[i] != b.extras[i] {
			return false
		}
	}
	return true
}

func (d *document) Undo() bool {
	if len(d.hist.undo) == 0 {
		return false
	}
	d.hist.redo = append(d.hist.redo, d.snap())
	last := d.hist.undo[len(d.hist.undo)-1]
	d.hist.undo = d.hist.undo[:len(d.hist.undo)-1]
	d.restore(last)
	d.hist.typingAt = 0
	return true
}

func (d *document) Redo() bool {
	if len(d.hist.redo) == 0 {
		return false
	}
	d.hist.undo = append(d.hist.undo, d.snap())
	next := d.hist.redo[len(d.hist.redo)-1]
	d.hist.redo = d.hist.redo[:len(d.hist.redo)-1]
	d.restore(next)
	d.hist.typingAt = 0
	return true
}

func (d *document) selectedText() string {
	cs := d.cursors()
	sort.Slice(cs, func(i, j int) bool { return min(cs[i].Pos, cs[i].Anchor) < min(cs[j].Pos, cs[j].Anchor) })
	var parts []string
	for _, c := range cs {
		lo, hi := c.Pos, c.Anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		if hi > lo {
			var b strings.Builder
			for _, ch := range d.buf.Slice(lo, hi) {
				b.WriteRune(ch.Rune)
			}
			parts = append(parts, b.String())
		}
	}
	return strings.Join(parts, "\n")
}
