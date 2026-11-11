package components

import "unicode/utf8"

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

func (b A11yBlock) StreamOffset(before string) int {
	return b.StreamOffsetRunes(utf8.RuneCountInString(before))
}

func (b A11yBlock) StreamOffsetRunes(before int) int {
	if b.Object {
		return b.Start
	}
	return b.Start + min(max(before-b.Lead, 0), b.StreamLen())
}

func A11yBlockAt(blocks []A11yBlock, offset int) int {
	at := -1
	for i, b := range blocks {
		if b.Mapped && b.Start <= offset && offset <= b.Start+b.StreamLen() && (at < 0 || b.Start > blocks[at].Start) {
			at = i
		}
	}
	return at
}

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

func (b A11yBlock) OffsetInBlock(offset int) int {
	if b.Object {
		return 0
	}
	return b.Lead + min(max(offset-b.Start, 0), b.StreamLen())
}
