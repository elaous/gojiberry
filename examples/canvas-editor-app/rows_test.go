package main

import "testing"

// build lays out text in rows of at most cols characters (8px each), with
// '\n' ending a row, the way layoutDocument does.
func build(text string, cols int) (*layout, func(int) bool) {
	rs := []rune(text)
	lay := &layout{Chars: make([]charLayout, len(rs)+1)}
	x, y, line, col := leftMarginForTest, topMargin, 0, 0
	for i, r := range rs {
		if r == '\n' {
			lay.Chars[i] = charLayout{X: x, Y: y, Line: line}
			x, y, line, col = leftMarginForTest, y+lineHeight, line+1, 0
			continue
		}
		if col == cols {
			x, y, line, col = leftMarginForTest, y+lineHeight, line+1, 0
		}
		lay.Chars[i] = charLayout{X: x, Y: y, W: 8, Line: line}
		x += 8
		col++
	}
	lay.Chars[len(rs)] = charLayout{X: x, Y: y, Line: line}
	lay.Lines = line + 1
	return lay, func(i int) bool { return i >= 0 && i < len(rs) && rs[i] == '\n' }
}

const leftMarginForTest = 12.0

func TestHitTestUsesTheGlyphBandOfALine(t *testing.T) {
	lay, _ := build("abcd\nefgh", 10)
	// Line 1's baseline is topMargin+lineHeight; its glyphs reach 16px above it.
	baseline1 := topMargin + lineHeight
	for _, y := range []float64{baseline1 - 14, baseline1 - 1, baseline1 + 3} {
		if idx := hitTest(lay, leftMarginForTest+1, y); idx != 5 {
			t.Errorf("click at y=%v landed on %d, want the start of line 1 (5)", y, idx)
		}
	}
	if idx := hitTest(lay, leftMarginForTest+1, topMargin-10); idx != 0 {
		t.Errorf("a click in line 0's glyphs gave %d", idx)
	}
}

func TestEndOnASoftWrappedRowIsAfterItsLastCharacter(t *testing.T) {
	lay, nl := build("abcdefghij", 4) // rows: abcd | efgh | ij
	pos, wrapped := lineEnd(lay, 1, nl)
	if pos != 4 || !wrapped {
		t.Fatalf("End on row 0: pos %d wrapped %v, want 4 (after d) wrapped", pos, wrapped)
	}
	// So Backspace there deletes 'd', the row's last character.
	if pos, wrapped = lineEnd(lay, 5, nl); pos != 8 || !wrapped {
		t.Fatalf("End on row 1: %d %v", pos, wrapped)
	}
	// The last row ends at the end of the document.
	if pos, wrapped = lineEnd(lay, 8, nl); pos != 10 || wrapped {
		t.Fatalf("End on the last row: %d %v", pos, wrapped)
	}
}

func TestEndBeforeAHardLineBreakStaysBeforeIt(t *testing.T) {
	lay, nl := build("ab\ncd", 10)
	if pos, wrapped := lineEnd(lay, 0, nl); pos != 2 || wrapped {
		t.Fatalf("End before a newline: %d %v, want 2 (before the break)", pos, wrapped)
	}
	if pos, wrapped := lineEnd(lay, 3, nl); pos != 5 || wrapped {
		t.Fatalf("End on the last line: %d %v", pos, wrapped)
	}
}

func TestClickPastTheEndOfAWrappedRowSnapsAfterItsLastCharacter(t *testing.T) {
	lay, nl := build("abcdefghij", 4)
	y := topMargin
	pastEnd := leftMarginForTest + 4*8 + 20
	idx, atEnd := hitTestEnd(lay, pastEnd, y, nl)
	if idx != 4 || !atEnd {
		t.Fatalf("click right of row 0: idx %d atEnd %v, want 4 and true", idx, atEnd)
	}
	if idx, atEnd = hitTestEnd(lay, leftMarginForTest+2*8+1, y, nl); idx != 2 || atEnd {
		t.Fatalf("a click inside the row: %d %v", idx, atEnd)
	}
	// Past a row that ends in a hard break: before the break, not snapped.
	lay2, nl2 := build("ab\ncd", 10)
	if idx, atEnd = hitTestEnd(lay2, leftMarginForTest+200, y, nl2); idx != 2 || atEnd {
		t.Fatalf("right of a hard-broken line: %d %v", idx, atEnd)
	}
}
