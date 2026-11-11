package canvas

import "slices"

// DefaultSquiggleColor is the colour of SetSquiggles underlines.
const DefaultSquiggleColor = "#d93025"

type squiggleSet struct {
	spans []Match
	color string
}

type squiggleState struct{ sets map[string]squiggleSet }

func (f *TextFlow) SetSquiggles(spans []Match, color string) { f.SetSquigglesAs("", spans, color) }

// SetSquigglesAs sets the named squiggle set; no spans removes it.
func (f *TextFlow) SetSquigglesAs(name string, spans []Match, color string) {
	if len(spans) == 0 {
		delete(f.squiggles.sets, name)
		return
	}
	if f.squiggles.sets == nil {
		f.squiggles.sets = map[string]squiggleSet{}
	}
	f.squiggles.sets[name] = squiggleSet{spans: slices.Clone(spans), color: color}
}

// Squiggles returns the ranges of the set SetSquiggles made.
func (f *TextFlow) Squiggles() []Match { return slices.Clone(f.squiggles.sets[""].spans) }

func (f *TextFlow) drawSquiggles(lay Layout) {
	names := make([]string, 0, len(f.squiggles.sets))
	for name := range f.squiggles.sets {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		f.drawSquiggleSet(lay, f.squiggles.sets[name])
	}
}

func (f *TextFlow) drawSquiggleSet(lay Layout, set squiggleSet) {
	p := f.Painter
	color := set.color
	if color == "" {
		color = DefaultSquiggleColor
	}
	p.SetStrokeStyle(color)
	p.SetLineWidth(1)
	for _, m := range set.spans {
		for _, ln := range lay.Lines {
			s, e := max(m.From, ln.Start), min(m.To, ln.End)
			for _, r := range f.lineRects(ln, s, e) {
				x0, x1 := r[0], r[1]
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
}
