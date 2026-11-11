package main

import "math"

const (
	lineHeight = 22.0
	topMargin  = 24.0
	ascent     = 16.0
)

type charLayout struct {
	X, Y float64
	W    float64 // the character's width (0 for a newline)
	Line int
}

type layout struct {
	Chars []charLayout
	Lines int
}

func hitTest(lay *layout, clickX, clickY float64) int {
	idx, _ := hitTestEnd(lay, clickX, clickY, nil)
	return idx
}

func hitTestEnd(lay *layout, clickX, clickY float64, isNewline func(int) bool) (idx int, atRowEnd bool) {
	targetLine := int(math.Floor((clickY - topMargin + ascent) / lineHeight))
	if targetLine < 0 {
		targetLine = 0
	}
	best, bestDist := 0, 1e18
	for i, cl := range lay.Chars {
		if cl.Line != targetLine {
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

func snapToRowEnd(lay *layout, pos int, clickX float64, isNewline func(int) bool) (int, bool) {
	n := len(lay.Chars) - 1
	if pos >= n {
		return pos, false
	}
	last, ok := rowLast(lay, pos, isNewline)
	if ok && pos == last && clickX > lay.Chars[last].X+lay.Chars[last].W/2 {
		return last + 1, true
	}
	return pos, false
}

func rowLast(lay *layout, idx int, isNewline func(int) bool) (last int, softWrapped bool) {
	n := len(lay.Chars) - 1
	line := lay.Chars[idx].Line
	last = idx
	for last+1 < n && lay.Chars[last+1].Line == line {
		last++
	}
	return last, last+1 < n && !isNewline(last)
}

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
