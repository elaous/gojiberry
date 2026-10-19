package canvas

// TypedRange is text another person typed a moment ago: stream offsets [From, To) shown in that person's colour (the
// colour of their cursor, so the flow's PeerPalette applies) until the host takes the range away. The host does that
// after a short delay, which is how typing in the text shows who is writing and then goes back to normal.
type TypedRange struct {
	From, To    int
	Name, Color string
}

// typedAt returns the range covering stream offset o, if any.
func (f *TextFlow) typedAt(o int) (TypedRange, bool) {
	for _, t := range f.TypedRanges {
		if o >= t.From && o < t.To {
			return t, true
		}
	}
	return TypedRange{}, false
}

// typedOverlaps says whether any typed range touches the stream offsets [from, to).
func (f *TextFlow) typedOverlaps(from, to int) bool {
	for _, t := range f.TypedRanges {
		if t.From < to && t.To > from {
			return true
		}
	}
	return false
}

// typedColour is the colour of the character at index i of the run when somebody just typed it, and whether somebody did.
// A run whose text does not match its offsets is never tinted.
func (f *TextFlow) typedColour(r Run, rs []rune, i int) (string, bool) {
	if len(f.TypedRanges) == 0 || len(rs) != r.End-r.Start {
		return "", false
	}
	if t, ok := f.typedAt(r.Start + i); ok {
		return f.peerColour(RemoteCursor{Name: t.Name, Color: t.Color}), true
	}
	return "", false
}

// fillRunTyped draws a run, in pieces when part of it was just typed by somebody else, each piece in its own colour: left
// to right, right to left (the first characters are at the right end of the run) or, in vertical text, along the line.
func (f *TextFlow) fillRunTyped(r Run, ln Line, x, y float64) {
	rs := []rune(r.Text)
	if len(f.TypedRanges) == 0 || len(rs) != r.End-r.Start || !f.typedOverlaps(r.Start, r.End) {
		f.fillRun(r, ln, x, y)
		return
	}
	if f.Vertical { // fillRun colours the characters itself
		f.fillRun(r, ln, x, y)
		f.Painter.SetFillStyle(r.Color)
		return
	}
	p := f.Painter
	rtl := r.Level%2 == 1
	for i := 0; i < len(rs); {
		c0, tinted := f.typedColour(r, rs, i)
		t0, _ := f.typedAt(r.Start + i)
		j := i + 1
		for j < len(rs) {
			u, in := f.typedAt(r.Start + j)
			if in != tinted || (in && u != t0) {
				break
			}
			j++
		}
		if tinted {
			p.SetFillStyle(c0)
		} else {
			p.SetFillStyle(r.Color)
		}
		seg := string(rs[i:j])
		at := x + p.MeasureTextWidth(string(rs[:i]))
		if rtl { // the characters before i are to the right of this piece
			at = x + r.W - p.MeasureTextWidth(string(rs[:j]))
		}
		p.FillText(seg, at, y)
		i = j
	}
	p.SetFillStyle(r.Color)
}
