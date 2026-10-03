package canvas

// Other people's cursors, when several people edit the same document (see
// the collab package): each is drawn as a tinted selection, a thin caret in
// the person's color and a small name flag above it.

// RemoteCursor is another person's cursor, as stream offsets in the flow.
type RemoteCursor struct {
	Name, Color   string
	Anchor, Caret int
}

// DefaultPeerColor is used for a cursor with no color of its own.
const DefaultPeerColor = "#7c5cff"

func peerColor(c RemoteCursor) string {
	if c.Color == "" {
		return DefaultPeerColor
	}
	return c.Color
}

func (f *TextFlow) drawPeerSelections(lay Layout) {
	p := f.Painter
	alpha, _ := p.(interface{ SetGlobalAlpha(float64) })
	setAlpha := func(a float64) {
		if alpha != nil {
			alpha.SetGlobalAlpha(a)
		}
	}
	for _, c := range f.RemoteCursors {
		from, to := min(c.Anchor, c.Caret), max(c.Anchor, c.Caret)
		if from == to {
			continue
		}
		p.SetFillStyle(peerColor(c))
		setAlpha(0.25)
		for _, ln := range lay.Lines {
			s, e := max(from, ln.Start), min(to, ln.End)
			if s >= e {
				continue
			}
			x0 := ln.X + f.data.measure(ln.Start, s)
			x1 := ln.X + f.data.measure(ln.Start, e)
			p.FillRect(x0, ln.Y, x1-x0, ln.H)
		}
		setAlpha(1)
	}
}

func (f *TextFlow) drawPeerCarets() {
	if f.Painter == nil {
		return
	}
	p := f.Painter
	for _, c := range f.RemoteCursors {
		x, y, h, ok := f.CaretRect(c.Caret)
		if !ok {
			continue
		}
		col := peerColor(c)
		p.SetFillStyle(col)
		p.FillRect(x, y+1, 2, h-2)
		if c.Name == "" {
			continue
		}
		p.SetFont("bold 10px sans-serif")
		p.SetTextAlign("left")
		p.SetTextBaseline("alphabetic")
		w := p.MeasureTextWidth(c.Name) + 8
		fy := y - 13
		p.SetFillStyle(col)
		p.FillRect(x, fy, w, 13)
		p.SetFillStyle("#ffffff")
		p.FillText(c.Name, x+4, fy+10)
	}
}
