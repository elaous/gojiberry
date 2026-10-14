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

// ColourBlindPalette is a set of colours people with the common kinds of colour blindness can tell apart
// (Paul Tol's "bright" qualitative scheme, without its two palest colours, which a white name label cannot be
// read on). Set TextFlow.PeerPalette to it.
var ColourBlindPalette = []string{"#4477AA", "#EE6677", "#228833", "#AA3377", "#BB5500", "#006D77"}

// peerColour is the colour a remote cursor is drawn in: its own, or, when the flow has a PeerPalette, the
// palette's colour for the cursor's place among the names of the people on show (so each person keeps one colour
// while the same people are there, and no two of them share one until the palette runs out).
func (f *TextFlow) peerColour(c RemoteCursor) string {
	if len(f.PeerPalette) == 0 {
		return peerColor(c)
	}
	rank := 0
	seen := map[string]bool{}
	for _, o := range f.RemoteCursors {
		if o.Name < c.Name && !seen[o.Name] {
			seen[o.Name] = true
			rank++
		}
	}
	return f.PeerPalette[rank%len(f.PeerPalette)]
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
		p.SetFillStyle(f.peerColour(c))
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
		col := f.peerColour(c)
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
