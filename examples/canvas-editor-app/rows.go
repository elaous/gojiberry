package main

import "math"

// What the editor knows about where characters sit, independent of how they
// were measured (layout.go does that against a canvas): the pure geometry --
// hit-testing a click, Home/End -- lives here so it builds and tests natively.

const (
	lineHeight = 22.0
	topMargin  = 24.0
	// ascent is how far above its baseline a line's text reaches: line k is
	// drawn with its baseline at topMargin+k*lineHeight, so the clickable band
	// of line k starts ascent above that.
	ascent = 16.0
)

// charLayout is where character i is drawn: X is its left edge, Y its
// baseline, and Line the index into layout's Lines.
type charLayout struct {
	X, Y float64
	W    float64 // the character's width (0 for a newline)
	Line int
}

// layout is the computed position of every character in a document,
// word-wrapped to maxWidth. Line breaks happen on '\n' (an explicit
// Enter) or, failing to fit a whole word, mid-word -- this is a
// character-based wrap, not word-based, so very long unbroken words
// can still overflow a line; a real editor would look ahead to the
// next space before committing to a break.
type layout struct {
	Chars []charLayout // parallel to document's characters, plus one extra trailing entry for the end-of-document caret position
	Lines int
}

// hitTest returns the index of the character position closest to
// (clickX, clickY) -- the same index space as document.Cursor, so its
// result can be assigned directly to Cursor/Anchor on a click.
func hitTest(lay *layout, clickX, clickY float64) int {
	idx, _ := hitTestEnd(lay, clickX, clickY, nil)
	return idx
}

// hitTestEnd is hitTest that also reports a click past the end of a
// soft-wrapped row: the position is then the first character of the next
// row (the only index just after the row's last character), and atRowEnd
// says the caret belongs at the end of the row, not the start of the next.
// isNewline says whether character i is a line break (nil: none is).
func hitTestEnd(lay *layout, clickX, clickY float64, isNewline func(int) bool) (idx int, atRowEnd bool) {
	targetLine := int(math.Floor((clickY - topMargin + ascent) / lineHeight))
	if targetLine < 0 {
		targetLine = 0
	}
	best, bestDist := 0, 1e18
	for i, cl := range lay.Chars {
		if cl.Line != targetLine {
			// Distance in line-space dominates: prefer any position on
			// the clicked line over the nearest x on the wrong line.
			lineDist := float64(cl.Line-targetLine) * 1e6
			if lineDist < 0 {
				lineDist = -lineDist
			}
			if lineDist < bestDist {
				best, bestDist = i, lineDist
			}
			continue
		}
		dist := cl.X - clickX
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			best, bestDist = i, dist
		}
	}
	if isNewline != nil {
		best, atRowEnd = snapToRowEnd(lay, best, clickX, isNewline)
	}
	return best, atRowEnd
}

// snapToRowEnd moves a position found on a soft-wrapped row to the row's end
// when the click was right of the last character's middle: the nearest left
// edge would otherwise put the caret before the row's last character.
func snapToRowEnd(lay *layout, pos int, clickX float64, isNewline func(int) bool) (int, bool) {
	n := len(lay.Chars) - 1 // characters in the document; the last entry is the end position
	if pos >= n {
		return pos, false
	}
	last, ok := rowLast(lay, pos, isNewline)
	if ok && pos == last && clickX > lay.Chars[last].X+lay.Chars[last].W/2 {
		return last + 1, true
	}
	return pos, false
}

// rowLast returns the index of the last character of idx's row and whether
// the row ends by soft wrap (a following row starts right after it, with no
// line break between).
func rowLast(lay *layout, idx int, isNewline func(int) bool) (last int, softWrapped bool) {
	n := len(lay.Chars) - 1
	line := lay.Chars[idx].Line
	last = idx
	for last+1 < n && lay.Chars[last+1].Line == line {
		last++
	}
	return last, last+1 < n && !isNewline(last)
}

// lineEnd is End: the position at the end of idx's row. On a row that ends
// by soft wrap that is the first character of the next row (the only index
// just after the row's last character) and wrapped says the caret is drawn
// at the end of the row; on a hard line break it is before the break, and on
// the last row, the end of the document.
func lineEnd(lay *layout, idx int, isNewline func(int) bool) (pos int, wrapped bool) {
	if idx >= len(lay.Chars)-1 {
		return len(lay.Chars) - 1, false
	}
	last, soft := rowLast(lay, idx, isNewline)
	if soft {
		return last + 1, true
	}
	if last+1 >= len(lay.Chars)-1 && !isNewline(last) {
		return len(lay.Chars) - 1, false // after the document's last character
	}
	return last, false
}
