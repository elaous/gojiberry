package canvas

import (
	"strconv"
	"strings"
)

// Busy marks an inline object somebody is changing: it is drawn with an outline in that person's colour, and, when the
// person is somebody else, a small rectangle at its top right corner with that colour as its background and the word
// "busy" in it. (A screen reader is told in words by the host; colour alone is not enough.)
type Busy struct {
	Name  string // who
	Color string // their colour, a CSS hex colour ("#2563eb"); anything else is drawn in grey
	Mine  bool   // the person is this one: the outline only
}

// BusyLabel is what the badge says.
const BusyLabel = "busy"

func (f *TextFlow) drawBusy(o PlacedObject) {
	b, ok := f.ObjectBusy[o.Object]
	if !ok {
		return
	}
	col := safeColor(b.Color)
	p := f.Painter
	p.SetStrokeStyle(col)
	p.SetLineWidth(2)
	p.StrokeRect(o.X+1, o.Y+1, o.W-2, o.H-2)
	if b.Mine {
		return
	}
	p.SetFont("600 11px sans-serif")
	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	w := p.MeasureTextWidth(BusyLabel) + 10
	h := 16.0
	x := o.X + o.W - w
	p.SetFillStyle(col)
	p.FillRect(x, o.Y, w, h)
	p.SetFillStyle(readableOn(col))
	p.FillText(BusyLabel, x+5, o.Y+h/2)
	p.SetTextBaseline("alphabetic")
}

// safeColor is c when it is a hex colour, grey otherwise (the colour comes from another person's presence).
func safeColor(c string) string {
	if len(c) == 4 || len(c) == 7 {
		if _, err := strconv.ParseUint(c[1:], 16, 32); err == nil && c[0] == '#' {
			return c
		}
	}
	return "#6b7280"
}

// readableOn is white or black, whichever reads on the hex colour.
func readableOn(c string) string {
	h := strings.TrimPrefix(c, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return "#ffffff"
	}
	r, g, b := float64(n>>16&0xff), float64(n>>8&0xff), float64(n&0xff)
	if 0.299*r+0.587*g+0.114*b > 150 {
		return "#000000"
	}
	return "#ffffff"
}
