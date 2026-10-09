// linewise.go adds VSCode-style commands on top of model.go's
// character buffer: move/duplicate the *logical* line containing the
// cursor, transform a selection's case, and jump/select by word
// boundary. "Logical line" here means a run of characters delimited
// by '\n' -- deliberately not layout.go's charLayout.Line, which
// counts word-*wrapped* visual rows instead (a single logical line
// wrapped across three rows is still one line to Alt+Up/Down or
// Shift+Alt+Down, the same distinction a real editor with soft wrap
// makes).
package main

// lineRange is one logical line's character span in document: [Start,
// End), not including its own trailing '\n' (there is none for the
// last line).
type lineRange struct {
	Start, End int
}

// logicalLines splits d's characters on '\n' into lineRanges.
func (d *document) logicalLines() []lineRange {
	lines := make([]lineRange, 0, 1)
	start := 0
	n := d.Len()
	for i := 0; i < n; i++ {
		if d.runeAt(i) == '\n' {
			lines = append(lines, lineRange{Start: start, End: i})
			start = i + 1
		}
	}
	return append(lines, lineRange{Start: start, End: n})
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

// moveLineUp swaps the logical line containing the primary cursor with
// the one immediately above it (VSCode's Alt+Up), keeping the cursor
// at the same column within its line's content. No-op on the first
// line.
//
// Multi-cursor support isn't implemented for this (or moveLineDown/
// duplicateLine below): swapping lines while other cursors sit
// somewhere in the affected span would leave their Pos/Anchor
// silently pointing at the wrong place, since -- unlike insert/
// backspace/delete, which fan out across every cursor deliberately --
// this mutates the buffer via a single, primary-cursor-specific path.
// Rather than leave that corruption possible, these three collapse to
// just the primary first: using one of them while multiple cursors
// are active drops back to single-cursor mode, a clearly-visible
// effect instead of a silent bug.
func (d *document) moveLineUp() { d.edit(d.moveLineUpNow) }

func (d *document) moveLineUpNow() {
	d.collapseCursors()
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	if li == 0 {
		return
	}
	d.swapLines(lines[li-1], lines[li])
}

// swapLines exchanges two adjacent lineRanges (a immediately above b)
// in place, moving the cursor along with whichever line it was on.
func (d *document) swapLines(a, b lineRange) {
	col := d.Cursor - a.Start
	onA := d.Cursor <= a.End
	if !onA {
		col = d.Cursor - b.Start
	}

	aChars := d.buf.Slice(a.Start, a.End)
	bChars := d.buf.Slice(b.Start, b.End)

	d.buf.DeleteAt(a.Start, b.End-a.Start)
	replacement := make([]char, 0, len(aChars)+len(bChars)+1)
	replacement = append(replacement, bChars...)
	replacement = append(replacement, char{Rune: '\n'})
	replacement = append(replacement, aChars...)
	d.buf.InsertAt(a.Start, replacement...)

	// b's content now sits first (at a.Start), a's content second, right
	// after b's (possibly different) length plus the separator.
	if onA {
		d.Cursor = a.Start + len(bChars) + 1 + col
	} else {
		d.Cursor = a.Start + col
	}
	d.Anchor = d.Cursor
}
