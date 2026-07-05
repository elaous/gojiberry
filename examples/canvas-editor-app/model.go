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

import "encoding/json"

// charFormat is the formatting applied to a single character.
type charFormat struct {
	Bold   bool
	Italic bool
}

// document is the whole editor buffer: a flat sequence of characters,
// each with its own format, plus a cursor and an optional selection.
// Keeping format per-character (rather than per-run) makes insertion,
// deletion, and toggling bold/italic on an arbitrary range simple slice
// operations, at the cost of some redundant memory for long runs of
// identically-formatted text -- a fine trade for a demo-sized document.
type document struct {
	Chars []rune
	Fmt   []charFormat

	Cursor int // index into Chars/Fmt where the next typed character is inserted
	Anchor int // selection anchor; selection is empty when Anchor == Cursor
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
	fmt := charFormat{}
	if d.Cursor > 0 {
		fmt = d.Fmt[d.Cursor-1]
	}
	runes := []rune(text)
	newChars := make([]rune, 0, len(d.Chars)+len(runes))
	newFmt := make([]charFormat, 0, len(d.Fmt)+len(runes))
	newChars = append(newChars, d.Chars[:d.Cursor]...)
	newFmt = append(newFmt, d.Fmt[:d.Cursor]...)
	for range runes {
		newFmt = append(newFmt, fmt)
	}
	newChars = append(newChars, runes...)
	newChars = append(newChars, d.Chars[d.Cursor:]...)
	newFmt = append(newFmt, d.Fmt[d.Cursor:]...)
	d.Chars = newChars
	d.Fmt = newFmt
	d.Cursor += len(runes)
	d.Anchor = d.Cursor
}

// deleteSelection removes the current selection, if any, and collapses
// the cursor to where it started. Reports whether anything was deleted.
func (d *document) deleteSelection() bool {
	lo, hi := d.selection()
	if hi <= lo {
		return false
	}
	d.Chars = append(d.Chars[:lo], d.Chars[hi:]...)
	d.Fmt = append(d.Fmt[:lo], d.Fmt[hi:]...)
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
	d.Chars = append(d.Chars[:d.Cursor-1], d.Chars[d.Cursor:]...)
	d.Fmt = append(d.Fmt[:d.Cursor-1], d.Fmt[d.Cursor:]...)
	d.Cursor--
	d.Anchor = d.Cursor
}

// delete deletes the selection, or the character after the cursor if
// there's no selection.
func (d *document) delete() {
	if d.deleteSelection() {
		return
	}
	if d.Cursor >= len(d.Chars) {
		return
	}
	d.Chars = append(d.Chars[:d.Cursor], d.Chars[d.Cursor+1:]...)
	d.Fmt = append(d.Fmt[:d.Cursor], d.Fmt[d.Cursor+1:]...)
}

// moveCursor sets the cursor to idx (clamped in range), extending the
// selection instead of collapsing it when extend is true (e.g.
// Shift+ArrowLeft/Right).
func (d *document) moveCursor(idx int, extend bool) {
	if idx < 0 {
		idx = 0
	}
	if idx > len(d.Chars) {
		idx = len(d.Chars)
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
	if hi <= lo || lo < 0 || hi > len(d.Fmt) {
		return
	}
	var target bool
	if kind == formatBold {
		target = !d.Fmt[lo].Bold
	} else {
		target = !d.Fmt[lo].Italic
	}
	for i := lo; i < hi; i++ {
		if kind == formatBold {
			d.Fmt[i].Bold = target
		} else {
			d.Fmt[i].Italic = target
		}
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
	for i, ch := range d.Chars {
		f := d.Fmt[i]
		if n := len(out); n > 0 && out[n-1].Bold == f.Bold && out[n-1].Italic == f.Italic {
			out[n-1].Text += string(ch)
			continue
		}
		out = append(out, run{Text: string(ch), Bold: f.Bold, Italic: f.Italic})
	}
	return out
}

// marshalDocument serializes d to JSON via its run-length encoding
// (compact, and a natural fit for a future real rich-text format).
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
	d := &document{}
	for _, r := range runs {
		for _, ch := range r.Text {
			d.Chars = append(d.Chars, ch)
			d.Fmt = append(d.Fmt, charFormat{Bold: r.Bold, Italic: r.Italic})
		}
	}
	return d, nil
}
