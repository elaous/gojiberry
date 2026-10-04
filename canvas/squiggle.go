package canvas

import "slices"

// DefaultSquiggleColor is the colour of the wavy underline SetSquiggles draws.
const DefaultSquiggleColor = "#d93025"

type squiggleState struct {
	spans []Match
	color string
}

// SetSquiggles underlines ranges of the text with a wavy line, the way a spell
// checker marks a word (see package spell). color "" means DefaultSquiggleColor.
// The ranges are rune offsets of the text stream (as Find returns them); they
// are drawn on the next Draw, and are not part of the document.
func (f *TextFlow) SetSquiggles(spans []Match, color string) {
	f.squiggles = squiggleState{spans: slices.Clone(spans), color: color}
}

// Squiggles returns the ranges set by SetSquiggles.
func (f *TextFlow) Squiggles() []Match { return slices.Clone(f.squiggles.spans) }

func (f *TextFlow) drawSquiggles(lay Layout) {
	if len(f.squiggles.spans) == 0 {
		return
	}
	p := f.Painter
	color := f.squiggles.color
	if color == "" {
		color = DefaultSquiggleColor
	}
	p.SetStrokeStyle(color)
	p.SetLineWidth(1)
	for _, m := range f.squiggles.spans {
		for _, ln := range lay.Lines {
			s, e := max(m.From, ln.Start), min(m.To, ln.End)
			if s >= e {
				continue
			}
			x0 := ln.X + f.data.measure(ln.Start, s)
			x1 := ln.X + f.data.measure(ln.Start, e)
			y := ln.Baseline + 2
			p.BeginPath()
			p.MoveTo(x0, y)
			up := true
			for x := x0 + 2; x < x1; x += 2 {
				if up {
					p.LineTo(x, y-1.5)
				} else {
					p.LineTo(x, y+1)
				}
				up = !up
			}
			p.LineTo(x1, y)
			p.Stroke()
		}
	}
}
