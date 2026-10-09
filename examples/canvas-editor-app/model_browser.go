//go:build js && wasm

package main

// model_browser.go holds the document operations that only the browser entry point
// (main.xg) calls: the single-cursor mouse move, move/duplicate line down, the word
// jumps and the save/load serialization. They live in a js/wasm-only file so the
// native build, which has no caller for them, carries no dead code.

import (
	"encoding/json"
	"unicode"
)

// clearHistory forgets every step (the document's starting text is not undoable).
func (d *document) clearHistory() { d.hist = history{} }

// moveLineDown is moveLineUp's mirror (VSCode's Alt+Down). No-op on
// the last line. See moveLineUp's doc comment for why this collapses
// to the primary cursor first.
func (d *document) moveLineDown() { d.edit(d.moveLineDownNow) }

func (d *document) moveLineDownNow() {
	d.collapseCursors()
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	if li >= len(lines)-1 {
		return
	}
	d.swapLines(lines[li], lines[li+1])
}

// duplicateLine inserts a copy of the logical line containing the
// primary cursor directly below itself (VSCode's Shift+Alt+Down),
// leaving the cursor at the same column in the new copy. See
// moveLineUp's doc comment for why this collapses to the primary
// cursor first.
func (d *document) duplicateLine() { d.edit(d.duplicateLineNow) }

func (d *document) duplicateLineNow() {
	d.collapseCursors()
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	l := lines[li]
	col := d.Cursor - l.Start

	lineChars := d.buf.Slice(l.Start, l.End)
	insert := make([]char, 0, len(lineChars)+1)
	insert = append(insert, char{Rune: '\n'})
	insert = append(insert, lineChars...)
	d.buf.InsertAt(l.End, insert...)

	d.Cursor = l.End + 1 + col
	d.Anchor = d.Cursor
}

// isWordChar reports whether r counts as part of a "word" for
// word-boundary movement/selection -- letters, digits, and
// underscore, the same class \w matches in most regex flavors.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// wordBoundaryForward returns the index just past the end of the next
// word in d after idx (skipping any non-word run first) -- Ctrl+
// Right's target.
func wordBoundaryForward(d *document, idx int) int {
	n := d.Len()
	i := idx
	for i < n && !isWordChar(d.runeAt(i)) {
		i++
	}
	for i < n && isWordChar(d.runeAt(i)) {
		i++
	}
	return i
}

// wordBoundaryBackward is wordBoundaryForward's mirror for Ctrl+Left.
func wordBoundaryBackward(d *document, idx int) int {
	i := idx
	for i > 0 && !isWordChar(d.runeAt(i-1)) {
		i--
	}
	for i > 0 && isWordChar(d.runeAt(i-1)) {
		i--
	}
	return i
}

// isNewline reports whether character i is a line break.
func (d *document) isNewline(i int) bool { return i >= 0 && i < d.Len() && d.runeAt(i) == '\n' }

// moveCursor sets the *primary* cursor to idx (clamped in range),
// extending its selection instead of collapsing it when extend is
// true. Used only for mouse-drag selection, which can only ever
// follow one pointer path and so only ever extends the primary
// regardless of how many other cursors are active -- everywhere else
// (arrow keys, word jumps, Home/End) uses moveCursors instead, to move
// every live cursor at once.
func (d *document) moveCursor(idx int, extend bool) {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	idx = clamp(idx, 0, d.Len())
	d.Cursor = idx
	if !extend {
		d.Anchor = idx
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
