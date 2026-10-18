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

// fillRunTyped draws a run, in pieces when part of it was just typed by somebody else, each piece in its own colour. A
// run whose text does not match its offsets (or that is right to left, or vertical) is drawn whole.
func (f *TextFlow) fillRunTyped(r Run, ln Line, x, y float64) {
	rs := []rune(r.Text)
	if len(f.TypedRanges) == 0 || f.Vertical || r.Level%2 == 1 || len(rs) != r.End-r.Start || !f.typedOverlaps(r.Start, r.End) {
		f.fillRun(r, ln, x, y)
		return
	}
	p := f.Painter
	for i := 0; i < len(rs); {
		t, tinted := f.typedAt(r.Start + i)
		j := i + 1
		for j < len(rs) {
			u, in := f.typedAt(r.Start + j)
			if in != tinted || (in && u != t) {
				break
			}
			j++
		}
		if tinted {
			p.SetFillStyle(f.peerColour(RemoteCursor{Name: t.Name, Color: t.Color}))
		} else {
			p.SetFillStyle(r.Color)
		}
		seg := string(rs[i:j])
		p.FillText(seg, x+p.MeasureTextWidth(string(rs[:i])), y)
		i = j
	}
	p.SetFillStyle(r.Color)
}
