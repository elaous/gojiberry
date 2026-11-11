package main

type lineRange struct {
	Start, End int
}

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

func lineContaining(lines []lineRange, idx int) int {
	for i, l := range lines {
		if idx >= l.Start && idx <= l.End {
			return i
		}
	}
	return len(lines) - 1
}

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

	if onA {
		d.Cursor = a.Start + len(bChars) + 1 + col
	} else {
		d.Cursor = a.Start + col
	}
	d.Anchor = d.Cursor
}
