package canvas

// Positions on a line of bidirectional text. In a line with only left-to-right text the x of a caret is the
// width of the text before it; in a Bidi line the runs are not in the order of the text, so a position is found
// through the run it is in: measured from the left edge of a left-to-right run, and from the right edge of a
// right-to-left one.

// piece is a run or an inline object of a line, with where it is (relative to the line's X).
type piece struct {
	start, end int
	x, w       float64
	level      uint8
}

func (p piece) rtl() bool { return p.level%2 == 1 }

// pieces lists the line's runs and objects in the order of the text.
func (ln Line) pieces() []piece {
	var out []piece
	base := uint8(0)
	if ln.RTL {
		base = 1
	}
	for _, r := range ln.Runs {
		out = append(out, piece{start: r.Start, end: r.End, x: r.X, w: r.W, level: r.Level})
	}
	for _, o := range ln.Objects {
		out = append(out, piece{start: o.Offset, end: o.Offset + 1, x: o.X - ln.X, w: o.W, level: base})
	}
	// insertion sort by start: a line has few pieces
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].start < out[j-1].start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// at is the x (relative to the line) of offset o, which is between p.start and p.end.
func (f *TextFlow) pieceX(p piece, o int) float64 {
	var w float64
	switch {
	case o <= p.start:
		w = 0
	case o >= p.end:
		w = p.w
	case p.end-p.start == 1:
		w = 0
	default:
		w = f.data.measure(p.start, o)
	}
	if p.rtl() {
		return p.x + p.w - w
	}
	return p.x + w
}

// lineX is the x of a caret at offset on the line.
func (f *TextFlow) lineX(ln Line, offset int) float64 {
	if !ln.Bidi {
		return ln.X + f.data.measure(ln.Start, min(max(offset, ln.Start), ln.End))
	}
	ps := ln.pieces()
	if len(ps) == 0 {
		return ln.X
	}
	first, last := ps[0], ps[len(ps)-1]
	switch {
	case offset <= first.start:
		return ln.X + f.pieceX(first, first.start)
	case offset >= last.end:
		return ln.X + f.pieceX(last, last.end)
	}
	var before, after *piece
	for i := range ps {
		p := &ps[i]
		switch {
		case p.start < offset && offset < p.end:
			return ln.X + f.pieceX(*p, offset)
		case p.end == offset:
			before = p
		case p.start == offset:
			after = p
		}
	}
	switch {
	case before == nil && after == nil:
		return ln.X
	case before == nil:
		return ln.X + f.pieceX(*after, offset)
	case after == nil:
		return ln.X + f.pieceX(*before, offset)
	}
	// Between two pieces: the caret goes at the piece that has the direction of the paragraph (where the text
	// being typed would continue); two of the other direction: the one before.
	if before.rtl() != ln.RTL && after.rtl() == ln.RTL {
		return ln.X + f.pieceX(*after, offset)
	}
	return ln.X + f.pieceX(*before, offset)
}

// lineOffsetAt is the offset on the line whose caret is nearest to x.
func (f *TextFlow) lineOffsetAt(ln Line, x float64) int {
	ps := ln.pieces()
	best, bestD := ln.Start, 1e18
	rel := x - ln.X
	for _, p := range ps {
		for o := p.start; o <= p.end; o++ {
			if d := abs(f.pieceX(p, o) - rel); d < bestD-1e-9 {
				best, bestD = o, d
			}
		}
	}
	return min(max(best, ln.Start), ln.End)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// lineRects are the x ranges (absolute, left edge first) that the offsets s..e of the line cover. In a line of
// one direction that is one range; in a Bidi line a selection can be several, with unselected text between.
func (f *TextFlow) lineRects(ln Line, s, e int) [][2]float64 {
	s, e = max(s, ln.Start), min(e, ln.End)
	if s >= e {
		return nil
	}
	if !ln.Bidi {
		return [][2]float64{{ln.X + f.data.measure(ln.Start, s), ln.X + f.data.measure(ln.Start, e)}}
	}
	var out [][2]float64
	for _, p := range ln.pieces() {
		a, b := max(s, p.start), min(e, p.end)
		if a >= b {
			continue
		}
		x0, x1 := f.pieceX(p, a), f.pieceX(p, b)
		if x0 > x1 {
			x0, x1 = x1, x0
		}
		x0, x1 = ln.X+x0, ln.X+x1
		// join with a neighbour it touches
		merged := false
		for i := range out {
			if abs(out[i][1]-x0) < 0.01 {
				out[i][1], merged = x1, true
				break
			}
			if abs(out[i][0]-x1) < 0.01 {
				out[i][0], merged = x0, true
				break
			}
		}
		if !merged {
			out = append(out, [2]float64{x0, x1})
		}
	}
	return out
}

// visualStep is the offset a caret moves to when the person presses the right (or left) arrow: the nearest
// place on the line that is to the right (left) of it on the screen. In text of one direction that is the
// next (or previous) character; in right-to-left text it is the other way round, and at the edge of the line
// the caret goes on to the previous or the next line.
func (f *TextFlow) visualStep(caret int, right bool) int {
	lines := f.Layout().Lines
	idx := -1
	for i, ln := range lines {
		if caret <= ln.End {
			idx = i
			break
		}
	}
	if idx < 0 {
		if right {
			return caret + 1
		}
		return caret - 1
	}
	ln := lines[idx]
	// The step by the text, which is the step by the screen in a line of one direction.
	forward := right != ln.RTL // the arrow goes towards the end of the text
	if ln.Bidi {
		x := f.lineX(ln, caret)
		best, bestD := -1, 1e18
		for o := ln.Start; o <= ln.End; o++ {
			d := f.lineX(ln, o) - x
			if !right {
				d = -d
			}
			if d > 0.01 && d < bestD-1e-9 {
				best, bestD = o, d
			}
		}
		if best >= 0 {
			return best
		}
		// The edge of the line: on to the line that follows in the text if the arrow points to the end of
		// the paragraph's direction, else back to the one before.
		switch {
		case forward && idx+1 < len(lines):
			return lines[idx+1].Start
		case !forward && idx > 0:
			return lines[idx-1].End
		}
		return caret
	}
	if forward {
		return caret + 1
	}
	return caret - 1
}
