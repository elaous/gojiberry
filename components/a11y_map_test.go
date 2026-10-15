package components

import "testing"

func TestCaretMapsToTheBlockThatHoldsItAndBack(t *testing.T) {
	blocks := []A11yBlock{
		{Kind: A11yHeading, Level: 1, Text: "Title", Mapped: true, Start: 0},                                                       // 0..5, break at 5
		{Kind: A11yListItem, Text: "☐ todo", Mapped: true, Start: 6, Lead: 2},                                                      // 6..10, break at 10
		{Kind: A11yParagraph, Text: "ab cd", Mapped: true, Start: 11, Runs: []A11yRun{{Text: "ab "}, {Text: "cd", Change: "del"}}}, // 11..16
		{Kind: A11yParagraph, Text: "Image: x", Mapped: true, Start: 17, Object: true},                                             // the object is one rune
		{Kind: A11yTableRow, Cells: []string{"a"}, Mapped: true, Start: 19, Object: true},
		{Kind: A11yTableRow, Cells: []string{"b"}, Mapped: true, Start: 19, Object: true},
		{Kind: A11yParagraph, Text: "no map"},
	}
	for _, c := range []struct{ caret, want int }{
		{0, 0}, {3, 0}, {5, 0}, // inside and at the end of the heading
		{6, 1}, {10, 1},
		{12, 2}, {16, 2},
		{17, 3}, {18, 3}, // on the picture and just after it
		{19, 4}, {20, 4}, // the table is found at its first row
		{40, -1},
	} {
		if got := A11yBlockAt(blocks, c.caret); got != c.want {
			t.Errorf("caret %d: block %d, want %d", c.caret, got, c.want)
		}
	}
	m := MarkCaret(blocks, 12)
	for i, b := range m {
		if b.Caret != (i == 2) {
			t.Errorf("block %d Caret=%v", i, b.Caret)
		}
	}
	if blocks[2].Caret {
		t.Error("MarkCaret must not change its argument")
	}

	// the heading: "Tit|le" is 3 into it
	if got := blocks[0].StreamOffset("Tit"); got != 3 {
		t.Errorf("heading offset %d", got)
	}
	// the check-list item: the box is read but is not in the stream, so after "☐ to" is 2 into the stream text
	if got := blocks[1].StreamOffset("☐ to"); got != 6+2 {
		t.Errorf("list item offset %d", got)
	}
	if got := blocks[1].StreamOffset("☐"); got != 6 {
		t.Errorf("a position inside the box is at the start of the text: %d", got)
	}
	if got := blocks[1].OffsetInBlock(8); got != 4 {
		t.Errorf("and back: %d runes of the copy come before offset 8, want 4 (the box and 2 letters)", got)
	}
	// a suggested removal is still in the stream
	if got := blocks[2].StreamLen(); got != 5 {
		t.Errorf("a block with a suggested removal takes %d runes, want 5", got)
	}
	if got := blocks[2].StreamOffset("ab c"); got != 11+4 {
		t.Errorf("inside the struck text: %d", got)
	}
	// an object is one rune, wherever the reader's cursor is in its words
	if got := blocks[3].StreamOffset("Image"); got != 17 {
		t.Errorf("object offset %d", got)
	}
	if got := blocks[0].StreamOffset("a much longer text than the block"); got != 5 {
		t.Errorf("a position past the end is the end: %d", got)
	}
	// every offset the reader's cursor can have inside the text maps back to the same place
	for off := 0; off <= 5; off++ {
		before := string([]rune("Title")[:blocks[0].OffsetInBlock(off)])
		if got := blocks[0].StreamOffset(before); got != off {
			t.Errorf("offset %d went to %d and back to %d", off, blocks[0].OffsetInBlock(off), got)
		}
	}
}

// A piece with a Stream takes that many positions of the stream, whatever its text is.
func TestStreamPieceCountsAsItsStream(t *testing.T) {
	b := A11yBlock{Mapped: true, Start: 10, Runs: []A11yRun{{Text: "ab"}, {Text: "12", Stream: 1}, {Text: "cd"}}}
	if got := b.StreamLen(); got != 5 {
		t.Errorf("StreamLen = %d, want 5", got)
	}
	if got := b.StreamOffsetRunes(3); got != 13 { // after "ab" and the mark's one position
		t.Errorf("after the mark: %d", got)
	}
	if got := b.StreamOffsetRunes(99); got != 15 {
		t.Errorf("past the end is the end: %d", got)
	}
}
