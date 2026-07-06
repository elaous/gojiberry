//go:build js && wasm

// Package main implements a canvas-rendered rich text document editor:
// the document is drawn directly onto an HTML5 <canvas> via
// domrt/html's Context2D wrapper, the way Google Docs' own editor
// renders its page content, instead of using contenteditable or a
// <textarea>. model.go holds the document itself, independent of any
// rendering or DOM concern; layout.go does word-wrap/hit-testing
// against a Context2D; main.gox wires it all up to the canvas, a
// hidden input for real keyboard capture, and a formatting toolbar.
package main

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/elaous/gojiberry/buffer"
)

// charFormat is the formatting applied to a single character.
type charFormat struct {
	Bold   bool
	Italic bool
}

// char pairs a rune with its own format in a single buffer.GapBuffer
// element -- deliberately one buffer, not two parallel ones (a rune
// buffer plus a format buffer kept in lockstep), since two structures
// that must always agree on length and index is a real, avoidable bug
// class; one struct per position can't drift out of sync with itself.
type char struct {
	Rune rune
	Fmt  charFormat
}

// document is the whole editor buffer: a buffer.GapBuffer of char
// (rune + per-character format together), plus a cursor and an
// optional selection. A GapBuffer here (rather than the plain []rune/
// []charFormat pair this document used before) is what makes typing
// in a large document cheap: see buffer.GapBuffer's own doc comment
// for why a flat slice's O(n)-shift-per-keystroke doesn't scale to
// "500+ pages without dropping below 60fps". document's zero value
// (buf's own zero value is an empty, ready-to-use GapBuffer) is
// itself ready to use -- no constructor required.
type document struct {
	buf buffer.GapBuffer[char]

	Cursor int // index where the next typed character is inserted
	Anchor int // selection anchor; selection is empty when Anchor == Cursor
}

// Len returns the number of characters in the document.
func (d *document) Len() int { return d.buf.Len() }

// runeAt returns the rune at index i (0 <= i < Len()).
func (d *document) runeAt(i int) rune { return d.buf.At(i).Rune }

// fmtAt returns the format at index i (0 <= i < Len()).
func (d *document) fmtAt(i int) charFormat { return d.buf.At(i).Fmt }

// setFmtAt overwrites the format at index i (0 <= i < Len()).
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

// selection returns the current selection as an ordered [lo, hi) range
// (possibly empty, when Anchor == Cursor).
func (d *document) selection() (lo, hi int) {
	if d.Anchor < d.Cursor {
		return d.Anchor, d.Cursor
	}
	return d.Cursor, d.Anchor
}

// hasSelection reports whether there's a non-empty selection.
func (d *document) hasSelection() bool {
	lo, hi := d.selection()
	return hi > lo
}

// insert inserts text at the cursor (replacing the selection first, if
// any), formatted like the character immediately before the insertion
// point (or unformatted, at the very start of the document).
func (d *document) insert(text string) {
	if d.hasSelection() {
		d.deleteSelection()
	}
	format := charFormat{}
	if d.Cursor > 0 {
		format = d.fmtAt(d.Cursor - 1)
	}
	items := make([]char, 0, utf8.RuneCountInString(text))
	for _, r := range text {
		items = append(items, char{Rune: r, Fmt: format})
	}
	d.buf.InsertAt(d.Cursor, items...)
	d.Cursor += len(items)
	d.Anchor = d.Cursor
}

// deleteSelection removes the current selection, if any, and collapses
// the cursor to where it started. Reports whether anything was deleted.
func (d *document) deleteSelection() bool {
	lo, hi := d.selection()
	if hi <= lo {
		return false
	}
	d.buf.DeleteAt(lo, hi-lo)
	d.Cursor = lo
	d.Anchor = lo
	return true
}

// backspace deletes the selection, or the character before the
// cursor if there's no selection.
func (d *document) backspace() {
	if d.deleteSelection() {
		return
	}
	if d.Cursor == 0 {
		return
	}
	d.buf.DeleteAt(d.Cursor-1, 1)
	d.Cursor--
	d.Anchor = d.Cursor
}

// delete deletes the selection, or the character after the cursor if
// there's no selection.
func (d *document) delete() {
	if d.deleteSelection() {
		return
	}
	if d.Cursor >= d.Len() {
		return
	}
	d.buf.DeleteAt(d.Cursor, 1)
}

// moveCursor sets the cursor to idx (clamped in range), extending the
// selection instead of collapsing it when extend is true (e.g.
// Shift+ArrowLeft/Right).
func (d *document) moveCursor(idx int, extend bool) {
	if idx < 0 {
		idx = 0
	}
	if idx > d.Len() {
		idx = d.Len()
	}
	d.Cursor = idx
	if !extend {
		d.Anchor = idx
	}
}

// formatKind selects which field toggleSelectionFormat flips.
type formatKind int

const (
	formatBold formatKind = iota
	formatItalic
)

// toggleSelectionFormat flips Bold or Italic (per kind) across every
// character in [lo, hi). The new value is based on the *first*
// selected character, so selecting mixed formatting and clicking Bold
// makes the whole selection bold (rather than an inconsistent
// per-char toggle).
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

// run is a maximal span of consecutive characters sharing the same
// format -- the unit a document (de)serializes to/from, and the unit
// layout.go measures/draws in one font-switch instead of
// per-character.
type run struct {
	Text   string `json:"text"`
	Bold   bool   `json:"bold"`
	Italic bool   `json:"italic"`
}

// runs merges d's per-character format into the fewest runs that
// reproduce it exactly.
func (d *document) runs() []run {
	var out []run
	n := d.Len()
	for i := 0; i < n; i++ {
		c := d.buf.At(i)
		if m := len(out); m > 0 && out[m-1].Bold == c.Fmt.Bold && out[m-1].Italic == c.Fmt.Italic {
			out[m-1].Text += string(c.Rune)
			continue
		}
		out = append(out, run{Text: string(c.Rune), Bold: c.Fmt.Bold, Italic: c.Fmt.Italic})
	}
	return out
}

// marshalDocument serializes d to JSON via its run-length encoding
// (compact, and a natural fit for a future real rich-text format;
// unchanged by the move to a GapBuffer, so a document saved before
// that change still loads correctly).
func marshalDocument(d *document) (string, error) {
	b, err := json.Marshal(d.runs())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// unmarshalDocument rebuilds a document from marshalDocument's output.
func unmarshalDocument(data string) (*document, error) {
	var runs []run
	if err := json.Unmarshal([]byte(data), &runs); err != nil {
		return nil, err
	}
	var items []char
	for _, r := range runs {
		for _, ch := range r.Text {
			items = append(items, char{Rune: ch, Fmt: charFormat{Bold: r.Bold, Italic: r.Italic}})
		}
	}
	d := &document{}
	d.buf.InsertAt(0, items...)
	return d, nil
}
