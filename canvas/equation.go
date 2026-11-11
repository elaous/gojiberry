package canvas

import "strings"

type EquationObject struct {
	Formula string
	Alt     string
	// Inline makes the formula part of its line of text.
	Inline bool
	Size   float64

	w, h    float64
	descent float64
}

const equationPad = 8.0

func (e *EquationObject) size() float64 {
	switch {
	case e.Size > 0:
		return e.Size
	case e.Inline:
		return 16
	}
	return 17
}

// Measure implements InlineObject.
func (e *EquationObject) Measure(p Painter, maxW, _ float64) (float64, float64) {
	f := strings.TrimSpace(e.Formula)
	var b mbox
	if f != "" {
		tree := parseMath(f)
		if !e.Inline {
			markLimits(tree)
		}
		b = typeset(p, tree, e.size())
	} else {
		p.SetFont("italic " + itoa(int(e.size())) + "px serif")
		b = mbox{w: p.MeasureTextWidth(e.label()), asc: 0.72 * e.size(), desc: 0.22 * e.size()}
	}
	if e.Inline {
		e.w, e.h, e.descent = b.w+4, b.asc+b.desc+2, b.desc+1
		return e.w, e.h
	}
	e.w, e.h, e.descent = maxW, max(34, b.asc+b.desc+16), 0
	if e.w <= 0 {
		e.w = b.w + 2*equationPad
	}
	return e.w, e.h
}

func (e *EquationObject) Descent() float64 { return e.descent }

func (e *EquationObject) label() string {
	if f := strings.TrimSpace(e.Formula); f != "" {
		return f
	}
	return "(empty equation)"
}

// DrawAt implements InlineObject.
func (e *EquationObject) DrawAt(p Painter, x, y float64) {
	size := e.size()
	if !e.Inline {
		p.SetFillStyle("#f3f4f6")
		p.FillRect(x, y+2, e.w, e.h-4)
	}
	p.SetFillStyle("#1f2328")
	p.SetStrokeStyle("#1f2328")
	f := strings.TrimSpace(e.Formula)
	if f == "" {
		p.SetFont("italic " + itoa(int(size)) + "px serif")
		p.SetTextAlign("left")
		p.SetTextBaseline("middle")
		p.FillText(e.label(), x+equationPad, y+e.h/2)
		return
	}
	b := typeset(p, parseMath(f), size)
	if e.Inline {
		b.draw(p, x+2, y+e.h-e.descent)
		return
	}
	b.draw(p, x+(e.w-b.w)/2, y+(e.h-(b.asc+b.desc))/2+b.asc)
}
