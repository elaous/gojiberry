//go:build js && wasm

// linewise.go adds VSCode-style commands on top of model.go's flat
// character buffer: move/duplicate the *logical* line containing the
// cursor, transform a selection's case, and jump/select by word
// boundary. "Logical line" here means a run of characters delimited
// by '\n' -- deliberately not layout.go's charLayout.Line, which
// counts word-*wrapped* visual rows instead (a single logical line
// wrapped across three rows is still one line to Alt+Up/Down or
// Shift+Alt+Down, the same distinction a real editor with soft wrap
// makes).
package main

import "unicode"

// lineRange is one logical line's character span in
// document.Chars/Fmt: [Start, End), not including its own trailing
// '\n' (there is none for the last line).
type lineRange struct {
	Start, End int
}

// logicalLines splits d.Chars on '\n' into lineRanges.
func (d *document) logicalLines() []lineRange {
	lines := make([]lineRange, 0, 1)
	start := 0
	for i, ch := range d.Chars {
		if ch == '\n' {
			lines = append(lines, lineRange{Start: start, End: i})
			start = i + 1
		}
	}
	return append(lines, lineRange{Start: start, End: len(d.Chars)})
}

// lineContaining returns the index into lines of the line containing
// character position idx (idx == a line's End -- the position right
// before its '\n', or end of document -- still belongs to that line,
// not the next one).
func lineContaining(lines []lineRange, idx int) int {
	for i, l := range lines {
		if idx >= l.Start && idx <= l.End {
			return i
		}
	}
	return len(lines) - 1
}

// moveLineUp swaps the logical line containing the cursor with the
// one immediately above it (VSCode's Alt+Up), keeping the cursor at
// the same column within its line's content. No-op on the first line.
func (d *document) moveLineUp() {
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	if li == 0 {
		return
	}
	d.swapLines(lines[li-1], lines[li])
}

// moveLineDown is moveLineUp's mirror (VSCode's Alt+Down). No-op on
// the last line.
func (d *document) moveLineDown() {
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	if li >= len(lines)-1 {
		return
	}
	d.swapLines(lines[li], lines[li+1])
}

// swapLines exchanges two adjacent lineRanges (a immediately above b)
// in place, moving the cursor along with whichever line it was on.
func (d *document) swapLines(a, b lineRange) {
	col := d.Cursor - a.Start
	onA := d.Cursor <= a.End
	if !onA {
		col = d.Cursor - b.Start
	}

	aText := append([]rune{}, d.Chars[a.Start:a.End]...)
	bText := append([]rune{}, d.Chars[b.Start:b.End]...)
	aFmt := append([]charFormat{}, d.Fmt[a.Start:a.End]...)
	bFmt := append([]charFormat{}, d.Fmt[b.Start:b.End]...)

	newChars := make([]rune, 0, len(d.Chars))
	newFmt := make([]charFormat, 0, len(d.Fmt))
	newChars = append(newChars, d.Chars[:a.Start]...)
	newFmt = append(newFmt, d.Fmt[:a.Start]...)
	newChars = append(newChars, bText...)
	newFmt = append(newFmt, bFmt...)
	newChars = append(newChars, '\n')
	newFmt = append(newFmt, charFormat{})
	newChars = append(newChars, aText...)
	newFmt = append(newFmt, aFmt...)
	newChars = append(newChars, d.Chars[b.End:]...)
	newFmt = append(newFmt, d.Fmt[b.End:]...)
	d.Chars = newChars
	d.Fmt = newFmt

	// b's content now sits first (at a.Start), a's content second, right
	// after b's (possibly different) length plus the separator.
	if onA {
		d.Cursor = a.Start + len(bText) + 1 + col
	} else {
		d.Cursor = a.Start + col
	}
	d.Anchor = d.Cursor
}

// duplicateLine inserts a copy of the logical line containing the
// cursor directly below itself (VSCode's Shift+Alt+Down), leaving the
// cursor at the same column in the new copy.
func (d *document) duplicateLine() {
	lines := d.logicalLines()
	l := lines[lineContaining(lines, d.Cursor)]
	col := d.Cursor - l.Start

	lineChars := append([]rune{}, d.Chars[l.Start:l.End]...)
	lineFmt := append([]charFormat{}, d.Fmt[l.Start:l.End]...)

	newChars := make([]rune, 0, len(d.Chars)+len(lineChars)+1)
	newFmt := make([]charFormat, 0, len(d.Fmt)+len(lineFmt)+1)
	newChars = append(newChars, d.Chars[:l.End]...)
	newFmt = append(newFmt, d.Fmt[:l.End]...)
	newChars = append(newChars, '\n')
	newFmt = append(newFmt, charFormat{})
	newChars = append(newChars, lineChars...)
	newFmt = append(newFmt, lineFmt...)
	newChars = append(newChars, d.Chars[l.End:]...)
	newFmt = append(newFmt, d.Fmt[l.End:]...)
	d.Chars = newChars
	d.Fmt = newFmt

	d.Cursor = l.End + 1 + col
	d.Anchor = d.Cursor
}

// transformSelectionCase upper/lowercases the current selection in
// place. No-op with no selection -- unlike VSCode (which falls back to
// the word under the cursor), this demo requires an explicit
// selection, kept simple on purpose.
func (d *document) transformSelectionCase(upper bool) {
	lo, hi := d.selection()
	for i := lo; i < hi; i++ {
		if upper {
			d.Chars[i] = unicode.ToUpper(d.Chars[i])
		} else {
			d.Chars[i] = unicode.ToLower(d.Chars[i])
		}
	}
}

// isWordChar reports whether r counts as part of a "word" for
// word-boundary movement/selection -- letters, digits, and
// underscore, the same class \w matches in most regex flavors.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// wordBoundaryForward returns the index just past the end of the next
// word after idx (skipping any non-word run first) -- Ctrl+Right's
// target.
func wordBoundaryForward(chars []rune, idx int) int {
	n := len(chars)
	i := idx
	for i < n && !isWordChar(chars[i]) {
		i++
	}
	for i < n && isWordChar(chars[i]) {
		i++
	}
	return i
}

// wordBoundaryBackward is wordBoundaryForward's mirror for Ctrl+Left.
func wordBoundaryBackward(chars []rune, idx int) int {
	i := idx
	for i > 0 && !isWordChar(chars[i-1]) {
		i--
	}
	for i > 0 && isWordChar(chars[i-1]) {
		i--
	}
	return i
}
