package canvas

// Remote cursors: other people's cursors when several edit a document.

// RemoteCursor is another person's cursor, as stream offsets.
type RemoteCursor struct {
	Name, Color   string
	Anchor, Caret int
}

// DefaultPeerColor colours cursors with no colour of their own.
const DefaultPeerColor = "#7c5cff"

func peerColor(c RemoteCursor) string {
	if c.Color == "" {
		return DefaultPeerColor
	}
	return c.Color
}

// ColourBlindPalette is colours that people with colour blindness can tell apart.
var ColourBlindPalette = []string{"#4477AA", "#EE6677", "#228833", "#AA3377", "#BB5500", "#006D77"}

// peerColour returns the cursor colour: its own, else from PeerPalette.
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
			for _, r := range f.lineRects(ln, s, e) {
				p.FillRect(r[0], ln.Y, r[1]-r[0], ln.H)
			}
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
		if f.skipCaretOnOtherPage(c.Caret) {
			continue
		}
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
