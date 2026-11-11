package canvas

// TypedRange: peer-typed stream offsets [From, To) shown in their colour.
type TypedRange struct {
	From, To    int
	Name, Color string
}

func (f *TextFlow) typedAt(o int) (TypedRange, bool) {
	for _, t := range f.TypedRanges {
		if o >= t.From && o < t.To {
			return t, true
		}
	}
	return TypedRange{}, false
}

func (f *TextFlow) typedOverlaps(from, to int) bool {
	for _, t := range f.TypedRanges {
		if t.From < to && t.To > from {
			return true
		}
	}
	return false
}

// a run whose text does not match its offsets is never tinted
func (f *TextFlow) typedColour(r Run, rs []rune, i int) (string, bool) {
	if len(f.TypedRanges) == 0 || len(rs) != r.End-r.Start {
		return "", false
	}
	if t, ok := f.typedAt(r.Start + i); ok {
		return f.peerColour(RemoteCursor{Name: t.Name, Color: t.Color}), true
	}
	return "", false
}

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
		if rtl {
			at = x + r.W - p.MeasureTextWidth(string(rs[:j]))
		}
		p.FillText(seg, at, y)
		i = j
	}
	p.SetFillStyle(r.Color)
}

func (f *TextFlow) drawTypedObject(o PlacedObject) {
	t, ok := f.typedAt(o.Offset)
	if !ok {
		return
	}
	p := f.Painter
	p.SetStrokeStyle(safeColor(f.peerColour(RemoteCursor{Name: t.Name, Color: t.Color})))
	p.SetLineWidth(2)
	p.StrokeRect(o.X+1, o.Y+1, o.W-2, o.H-2)
}
