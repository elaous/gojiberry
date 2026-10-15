package components

import "unicode/utf8"

// The mirror of the canvas is only as good as its map back to the canvas: a screen reader's cursor in the
// mirror is a position in some block's text, and the editor's caret is a rune offset in its text stream.
// A mapped block (A11yBlock.Mapped) says where it starts in the stream; these functions turn a position
// in a block into a stream offset and a stream offset into the block that holds it.

// StreamLen is how many runes the block takes in the editor's text stream: its text, whatever pieces it is
// made of (a suggested removal is still in the stream), less the Lead that is only in the copy; an
// object (a picture, a table) is one rune.
func (b A11yBlock) StreamLen() int {
	if b.Object {
		return 1
	}
	n := 0
	if len(b.Runs) > 0 {
		for _, r := range b.Runs {
			if r.Stream > 0 {
				n += r.Stream
			} else {
				n += utf8.RuneCountInString(r.Text)
			}
		}
	} else {
		n = utf8.RuneCountInString(b.Text)
	}
	return max(n-b.Lead, 0)
}

// StreamOffset is the stream offset of the position in a mapped block after `before` (the text of the
// block that comes before the position, as the copy has it, without the words only a reader hears).
// Inside an object it is the object itself.
func (b A11yBlock) StreamOffset(before string) int {
	return b.StreamOffsetRunes(utf8.RuneCountInString(before))
}

// StreamOffsetRunes is StreamOffset for a host that has counted the stream runes before the position itself
// (the pieces with a Stream count as that many, not as their text), the Lead included.
func (b A11yBlock) StreamOffsetRunes(before int) int {
	if b.Object {
		return b.Start
	}
	return b.Start + min(max(before-b.Lead, 0), b.StreamLen())
}

// A11yBlockAt returns the index of the mapped block that holds the stream offset (the caret), -1 if none
// does (the offset is on a paragraph break that belongs to no block, or the blocks are not mapped). The
// end of a block's text is still in it.
func A11yBlockAt(blocks []A11yBlock, offset int) int {
	at := -1
	for i, b := range blocks {
		if b.Mapped && b.Start <= offset && offset <= b.Start+b.StreamLen() && (at < 0 || b.Start > blocks[at].Start) {
			at = i
		}
	}
	return at
}

// MarkCaret returns the blocks with Caret set on the one that holds the stream offset and on no other
// (the argument is not changed).
func MarkCaret(blocks []A11yBlock, offset int) []A11yBlock {
	out := make([]A11yBlock, len(blocks))
	copy(out, blocks)
	for i := range out {
		out[i].Caret = false
	}
	if at := A11yBlockAt(out, offset); at >= 0 {
		out[at].Caret = true
	}
	return out
}

// OffsetInBlock is the reverse of StreamOffset: how many runes of the block's text in the copy come
// before the stream offset (what a host needs to put the reader's cursor where the caret is).
func (b A11yBlock) OffsetInBlock(offset int) int {
	if b.Object {
		return 0
	}
	return b.Lead + min(max(offset-b.Start, 0), b.StreamLen())
}
