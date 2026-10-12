package canvas

import "strings"

// EquationObject is a formula in the text, a block InlineObject. Formula is in the linear notation of
// package export (x^2 + y_1, frac{a}{b}, sqrt{x}); Alt is its text alternative, what a person would read
// aloud ("x squared plus y sub 1"), which every export writes and the checker requires. The editor shows
// the formula as typed: it has no typesetter, the exports write MathML.
type EquationObject struct {
	Formula string
	Alt     string
	w, h    float64
}

// equationPad is the space around the formula in its box.
const equationPad = 8.0

// Measure implements InlineObject.
func (e *EquationObject) Measure(p Painter, maxW, _ float64) (float64, float64) {
	e.w, e.h = maxW, 34
	if e.w <= 0 {
		p.SetFont("italic 17px serif")
		e.w = p.MeasureTextWidth(e.label()) + 2*equationPad
	}
	return e.w, e.h
}

// label is the text drawn: the formula, or a note that it is empty.
func (e *EquationObject) label() string {
	if f := strings.TrimSpace(e.Formula); f != "" {
		return f
	}
	return "(empty equation)"
}

// DrawAt implements InlineObject.
func (e *EquationObject) DrawAt(p Painter, x, y float64) {
	p.SetFillStyle("#f3f4f6")
	p.FillRect(x, y+2, e.w, e.h-4)
	p.SetFillStyle("#1f2328")
	p.SetFont("italic 17px serif")
	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	p.FillText(e.label(), x+equationPad, y+e.h/2)
}
