// Package main implements a canvas-rendered rich text document editor:
// the document is drawn directly onto an HTML5 <canvas> via
// domrt/html's Context2D wrapper, the way Google Docs' own editor
// renders its page content, instead of using contenteditable or a
// <textarea>. model.go holds the document itself, independent of any
// rendering or DOM concern; linewise.go adds line/word commands on top
// of it; layout.go does word-wrap/hit-testing against a Context2D;
// main.xg wires it all up to the canvas, a hidden input for real
// keyboard capture, and a formatting toolbar. model.go/linewise.go
// carry no js/wasm-only dependency (only layout.go and main.xg do,
// via domrt/html), so they -- and native_stub.go's no-op func main --
// build and unit-test natively via a plain `go test`, fast and without
// a browser; canvas_editor_test.go separately drives the real,
// GOOS=js GOARCH=wasm-built binary through Playwright for true
// end-to-end coverage.
package main

import (
	"encoding/json"
	"sort"
	"unicode"
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

// cursor is one caret plus its selection anchor (Anchor == Pos means
// no selection).
type cursor struct {
	Pos, Anchor int
}

// document is the whole editor buffer: a buffer.GapBuffer of char
// (rune + per-character format together), plus one or more
// simultaneous cursors. A GapBuffer here (rather than the plain
// []rune/[]charFormat pair this document used before) is what makes
// typing in a large document cheap: see buffer.GapBuffer's own doc
// comment for why a flat slice's O(n)-shift-per-keystroke doesn't
// scale to "500+ pages without dropping below 60fps". document's zero
// value (buf's own zero value is an empty, ready-to-use GapBuffer) is
// itself ready to use -- no constructor required.
//
// Cursor/Anchor is always the leftmost (lowest-Pos) live cursor --
// "primary" only in the sense that a few single-cursor-oriented
// operations (moveLineUp/Down, duplicateLine -- see their own doc
// comments in linewise.go for why multi-cursor support isn't
// implemented for those three specifically) act on it and drop any
// others. ExtraCursors holds every additional simultaneous cursor
// (Alt+Click adds one; Escape drops back to just the primary), kept
// sorted by Pos and never coincident with the primary or each other --
// setCursors enforces this after every edit that touches cursors.
type document struct {
	buf buffer.GapBuffer[char]

	Cursor, Anchor int
	ExtraCursors   []cursor
}

// Len returns the number of characters in the document.
func (d *document) Len() int { return d.buf.Len() }

// runeAt returns the rune at index i (0 <= i < Len()).
func (d *document) runeAt(i int) rune { return d.buf.At(i).Rune }

// isNewline reports whether character i is a line break.
func (d *document) isNewline(i int) bool { return i >= 0 && i < d.Len() && d.runeAt(i) == '\n' }

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

// selection returns the primary cursor's selection as an ordered
// [lo, hi) range (possibly empty, when Anchor == Cursor).
func (d *document) selection() (lo, hi int) {
	if d.Anchor < d.Cursor {
		return d.Anchor, d.Cursor
	}
	return d.Cursor, d.Anchor
}

// hasSelection reports whether the primary cursor has a non-empty
// selection.
func (d *document) hasSelection() bool {
	lo, hi := d.selection()
	return hi > lo
}

// cursors returns every live cursor (primary first, then
// ExtraCursors) as a fresh slice -- mutate freely and pass to
// setCursors to write changes back.
func (d *document) cursors() []cursor {
	out := make([]cursor, 0, 1+len(d.ExtraCursors))
	out = append(out, cursor{Pos: d.Cursor, Anchor: d.Anchor})
	out = append(out, d.ExtraCursors...)
	return out
}

// setCursors replaces every live cursor with cs (in any order),
// sorting by Pos ascending and merging any two that land at the exact
// same Pos (the common outcome of two cursors' edits ending up in the
// same place) down to one. The lowest-Pos survivor becomes the
// primary Cursor/Anchor; the rest become ExtraCursors.
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

// addCursorAt appends a new, unselected cursor at pos (clamped into
// range) -- Alt+Click's effect.
func (d *document) addCursorAt(pos int) {
	pos = clamp(pos, 0, d.Len())
	d.setCursors(append(d.cursors(), cursor{Pos: pos, Anchor: pos}))
}

// resetCursorTo collapses every cursor to a single one at pos, with no
// selection -- a plain (non-modifier) click's effect.
func (d *document) resetCursorTo(pos int) {
	pos = clamp(pos, 0, d.Len())
	d.Cursor, d.Anchor = pos, pos
	d.ExtraCursors = nil
}

// collapseCursors drops every ExtraCursor, keeping only the primary
// (always the leftmost live cursor -- see setCursors) where it already
// is -- Escape's effect. Adding a cursor to the left of the original
// one (Alt+Click before it) makes *that* the primary, so collapsing
// afterward keeps the new leftmost position, not "whichever cursor was
// there first" -- there's no per-cursor identity tracked across edits
// to make the latter meaningful anyway.
func (d *document) collapseCursors() {
	d.ExtraCursors = nil
}

// selectAll collapses to a single cursor selecting the whole document
// -- Ctrl/Cmd+A's effect. Matches most editors in discarding any
// additional cursors rather than selecting everything under each one.
func (d *document) selectAll() {
	d.Anchor, d.Cursor = 0, d.Len()
	d.ExtraCursors = nil
}

// moveCursor sets the *primary* cursor to idx (clamped in range),
// extending its selection instead of collapsing it when extend is
// true. Used only for mouse-drag selection, which can only ever
// follow one pointer path and so only ever extends the primary
// regardless of how many other cursors are active -- everywhere else
// (arrow keys, word jumps, Home/End) uses moveCursors instead, to move
// every live cursor at once.
func (d *document) moveCursor(idx int, extend bool) {
	idx = clamp(idx, 0, d.Len())
	d.Cursor = idx
	if !extend {
		d.Anchor = idx
	}
}

// moveCursors moves every live cursor to newPos(currentPos), extending
// each one's own selection instead of collapsing it when extend is
// true -- moveCursor's single-cursor rule, applied independently to
// every cursor. No buffer mutation happens here, so (unlike insert/
// backspace/delete) cursors never need processing in any particular
// order relative to each other.
func (d *document) moveCursors(newPos func(pos int) int, extend bool) {
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

// forEachCursorDescending calls edit once per live cursor, in
// descending order of max(Pos, Anchor) -- so applying edit (which may
// insert/delete characters) at one cursor never needs to look at a
// not-yet-processed cursor's own Pos/Anchor to know where to edit,
// since by construction every not-yet-processed cursor's positions are
// still strictly less than the current one's edit range. edit receives
// that cursor's current (pos, anchor) and returns its new (pos,
// anchor) after the edit.
//
// The reverse direction still needs handling explicitly, though: every
// *already*-processed cursor (the ones with a higher original
// position, run earlier in this descending loop) sits *after* the
// current edit, so if that edit changed the document's length, every
// already-processed cursor's already-computed new position must shift
// by the same delta -- e.g. three cursors typing "X" simultaneously at
// increasing positions must each still land right after their own X,
// not at increasingly stale offsets that ignore the X's typed after
// them.
//
// Known limitation: this assumes cursors' selections don't overlap
// each other. Two cursors with overlapping ranges (not something
// Alt+Click or the built-in movement commands can produce, since
// there's no "select next occurrence" command yet to create one) can
// produce surprising results -- the same edge case most editors handle
// by merging overlapping cursors on creation, not implemented here.
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

// insert inserts text at every live cursor (replacing each one's own
// selection first, if any), formatted like the character immediately
// before each insertion point (or unformatted, at the very start of
// the document).
func (d *document) insert(text string) {
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

// backspace deletes, at every live cursor, that cursor's own selection
// -- or the character before it if it has none.
func (d *document) backspace() {
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

// delete deletes, at every live cursor, that cursor's own selection --
// or the character after it if it has none.
func (d *document) delete() {
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

// toggleSelectionFormatAllCursors flips Bold/Italic (per kind) across
// every live cursor's own selection (a no-op for any cursor with
// none) -- the toolbar Bold/Italic buttons' effect.
func (d *document) toggleSelectionFormatAllCursors(kind formatKind) {
	for _, c := range d.cursors() {
		lo, hi := c.Pos, c.Anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		d.toggleSelectionFormat(lo, hi, kind)
	}
}

// transformCaseRange upper/lowercases buf[lo:hi) in place.
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

// transformSelectionCaseAllCursors upper/lowercases every live
// cursor's own selection in place (a no-op for any cursor with none).
func (d *document) transformSelectionCaseAllCursors(upper bool) {
	for _, c := range d.cursors() {
		lo, hi := c.Pos, c.Anchor
		if lo > hi {
			lo, hi = hi, lo
		}
		d.transformCaseRange(lo, hi, upper)
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
// that change still loads correctly). Cursors (beyond the primary,
// which the caller resets on load anyway -- see loadDocument) aren't
// persisted; a reload always starts back at a single cursor.
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
