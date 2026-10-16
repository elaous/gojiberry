package canvas

import (
	"strconv"
	"strings"
	"unicode"
)

// The typesetter of the equation object. It reads the linear notation of package export (see its elements.go:
// numbers, letters, + - * / = < > ( ) [ ] , ; : |, ^ and _ for scripts, {...} groups, sqrt{x}, frac{a}{b}, and the
// names pi alpha beta gamma delta theta lambda mu sigma omega infty sum int) and draws it the way mathematics is
// written: fractions over a rule, roots under a radical sign, raised and lowered scripts, letters in italics, numbers
// and function names upright. The notation is read the same way as the MathML export reads it; a test keeps the
// two on the same corpus.

type mkind int

const (
	mRow    mkind = iota // kids in a line
	mIdent               // a variable: italic
	mNum                 // a number: upright
	mOp                  // an operator or a bracket
	mFunc                // a function name: upright
	mBig                 // sum, integral
	mSqrt                // kids[0] under a radical
	mFrac                // kids[0] over kids[1]
	mScript              // kids[0] with kids[1] below and kids[2] above (either may be nil)
)

type mnode struct {
	kind mkind
	text string
	kids []*mnode
}

var mathGreek = map[string]string{
	"pi": "π", "alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "theta": "θ", "lambda": "λ", "mu": "μ",
	"sigma": "σ", "omega": "ω", "infty": "∞",
}

var mathFuncNames = map[string]bool{"sin": true, "cos": true, "tan": true, "log": true, "ln": true, "exp": true, "lim": true}

type mparser struct {
	src []rune
	pos int
}

// parseMath reads a formula; a formula with unbalanced braces is closed leniently, so any text gives a tree.
func parseMath(formula string) *mnode {
	p := &mparser{src: []rune(formula)}
	return p.row(false)
}

func (p *mparser) more() bool { return p.pos < len(p.src) }

func (p *mparser) row(inGroup bool) *mnode {
	r := &mnode{kind: mRow}
	for p.more() {
		if p.src[p.pos] == '}' {
			p.pos++
			if inGroup {
				return r
			}
			continue // a stray closing brace
		}
		if a := p.script(p.atom()); a != nil {
			r.kids = append(r.kids, a)
		}
	}
	return r
}

func (p *mparser) script(base *mnode) *mnode {
	if base == nil {
		base = &mnode{kind: mRow}
	}
	var sub, sup *mnode
	for p.more() && (p.src[p.pos] == '^' || p.src[p.pos] == '_') {
		op := p.src[p.pos]
		p.pos++
		arg := p.operand()
		if op == '^' {
			sup = arg
		} else {
			sub = arg
		}
	}
	if sub == nil && sup == nil {
		return base
	}
	return &mnode{kind: mScript, kids: []*mnode{base, sub, sup}}
}

func (p *mparser) operand() *mnode {
	if !p.more() {
		return &mnode{kind: mRow}
	}
	if p.src[p.pos] == '{' {
		p.pos++
		return p.row(true)
	}
	if a := p.atom(); a != nil {
		return a
	}
	return &mnode{kind: mRow}
}

func (p *mparser) atom() *mnode {
	for p.more() && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
	if !p.more() {
		return nil
	}
	r := p.src[p.pos]
	if unicode.IsControl(r) {
		p.pos++
		return nil
	}
	switch {
	case r == '{':
		p.pos++
		return p.row(true)
	case r == '^' || r == '_':
		return &mnode{kind: mRow} // a script with nothing before it
	case unicode.IsDigit(r):
		start := p.pos
		for p.more() && (unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '.') {
			p.pos++
		}
		return &mnode{kind: mNum, text: string(p.src[start:p.pos])}
	case unicode.IsLetter(r):
		start := p.pos
		for p.more() && unicode.IsLetter(p.src[p.pos]) {
			p.pos++
		}
		word := string(p.src[start:p.pos])
		switch {
		case word == "sqrt":
			return &mnode{kind: mSqrt, kids: []*mnode{p.operand()}}
		case word == "frac":
			num := p.operand()
			return &mnode{kind: mFrac, kids: []*mnode{num, p.operand()}}
		case word == "sum":
			return &mnode{kind: mBig, text: "∑"}
		case word == "int":
			return &mnode{kind: mBig, text: "∫"}
		case mathGreek[word] != "":
			return &mnode{kind: mIdent, text: mathGreek[word]}
		case mathFuncNames[word]:
			return &mnode{kind: mFunc, text: word}
		}
		letters := []rune(word)
		if len(letters) > 1 { // one identifier per letter; a script applies to the last one
			p.pos--
			row := &mnode{kind: mRow}
			for _, l := range letters[:len(letters)-1] {
				row.kids = append(row.kids, &mnode{kind: mIdent, text: string(l)})
			}
			return row
		}
		return &mnode{kind: mIdent, text: word}
	}
	p.pos++
	return &mnode{kind: mOp, text: string(r)}
}

// mbox is a typeset piece: its width, how far it reaches above and below the baseline, and how to draw it with its
// left edge at x and its baseline at y.
type mbox struct {
	w, asc, desc float64
	draw         func(p Painter, x, y float64)
}

// typeset lays n out at the font size (pixels) with the painter's text metrics.
func typeset(p Painter, n *mnode, size float64) mbox {
	switch n.kind {
	case mIdent, mNum, mFunc:
		font := strconv.FormatFloat(size, 'f', -1, 64) + "px serif"
		if n.kind == mIdent {
			font = "italic " + font
		}
		p.SetFont(font)
		w := p.MeasureTextWidth(n.text)
		if n.kind == mIdent {
			w += 0.06 * size // italic letters lean into the next one
		}
		return mbox{w: w, asc: 0.72 * size, desc: 0.22 * size, draw: func(p Painter, x, y float64) {
			p.SetFont(font)
			p.SetTextAlign("left")
			p.SetTextBaseline("alphabetic")
			p.FillText(n.text, x, y)
		}}
	case mOp:
		return typesetOp(p, n.text, size)
	case mBig:
		big := size * 1.45
		font := strconv.FormatFloat(big, 'f', -1, 64) + "px serif"
		p.SetFont(font)
		w := p.MeasureTextWidth(n.text) + 0.1*size
		return mbox{w: w, asc: 0.8 * big, desc: 0.3 * big, draw: func(p Painter, x, y float64) {
			p.SetFont(font)
			p.SetTextAlign("left")
			p.SetTextBaseline("alphabetic")
			p.FillText(n.text, x+0.05*size, y+0.12*big)
		}}
	case mScript:
		return typesetScript(p, n, size)
	case mFrac:
		return typesetFrac(p, n, size)
	case mSqrt:
		return typesetSqrt(p, n, size)
	}
	return typesetRow(p, n, size)
}

func typesetOp(p Painter, op string, size float64) mbox {
	shown := op
	switch op {
	case "-":
		shown = "−"
	case "*":
		shown = "·"
	}
	font := strconv.FormatFloat(size, 'f', -1, 64) + "px serif"
	p.SetFont(font)
	w := p.MeasureTextWidth(shown)
	return mbox{w: w, asc: 0.72 * size, desc: 0.22 * size, draw: func(p Painter, x, y float64) {
		p.SetFont(font)
		p.SetTextAlign("left")
		p.SetTextBaseline("alphabetic")
		p.FillText(shown, x, y)
	}}
}

// binary says a character is an operator with space around it when it stands between two things.
func binary(op string) bool { return strings.Contains("+-*=<>", op) }

func typesetRow(p Painter, n *mnode, size float64) mbox {
	type placed struct {
		b mbox
		x float64
	}
	var parts []placed
	x := 0.0
	prevOperand := false // something that can be the left side of a binary operator came before
	for _, k := range n.kids {
		b := typeset(p, k, size)
		pad := 0.0
		if k.kind == mOp && binary(k.text) && prevOperand {
			pad = 0.22 * size
		}
		x += pad
		parts = append(parts, placed{b, x})
		x += b.w + pad
		prevOperand = k.kind != mOp || k.text == ")" || k.text == "]" || k.text == "|"
	}
	out := mbox{w: x}
	for _, pl := range parts {
		out.asc, out.desc = max(out.asc, pl.b.asc), max(out.desc, pl.b.desc)
	}
	out.draw = func(p Painter, ox, y float64) {
		for _, pl := range parts {
			pl.b.draw(p, ox+pl.x, y)
		}
	}
	return out
}

func typesetScript(p Painter, n *mnode, size float64) mbox {
	base := typeset(p, n.kids[0], size)
	small := size * 0.7
	var sub, sup *mbox
	if n.kids[1] != nil {
		b := typeset(p, n.kids[1], small)
		sub = &b
	}
	if n.kids[2] != nil {
		b := typeset(p, n.kids[2], small)
		sup = &b
	}
	supUp := max(0.4*size, base.asc-0.5*small)
	subDown := max(0.2*size, base.desc*0.8)
	sw := 0.0
	out := mbox{w: base.w, asc: base.asc, desc: base.desc}
	if sup != nil {
		sw = max(sw, sup.w)
		out.asc = max(out.asc, supUp+sup.asc)
	}
	if sub != nil {
		sw = max(sw, sub.w)
		out.desc = max(out.desc, subDown+sub.desc)
	}
	out.w += sw + 0.04*size
	out.draw = func(p Painter, x, y float64) {
		base.draw(p, x, y)
		if sup != nil {
			sup.draw(p, x+base.w+0.02*size, y-supUp)
		}
		if sub != nil {
			sub.draw(p, x+base.w+0.02*size, y+subDown)
		}
	}
	return out
}

func typesetFrac(p Painter, n *mnode, size float64) mbox {
	num, den := typeset(p, n.kids[0], size*0.9), typeset(p, n.kids[1], size*0.9)
	axis, gap := 0.28*size, 0.14*size
	pad := 0.12 * size
	w := max(num.w, den.w) + 2*pad
	numUp := axis + gap + num.desc
	denDown := gap + den.asc - axis
	return mbox{
		w: w, asc: numUp + num.asc, desc: denDown + den.desc,
		draw: func(p Painter, x, y float64) {
			num.draw(p, x+(w-num.w)/2, y-numUp)
			den.draw(p, x+(w-den.w)/2, y+denDown)
			p.FillRect(x+pad/2, y-axis-0.5, w-pad, 1)
		},
	}
}

func typesetSqrt(p Painter, n *mnode, size float64) mbox {
	body := typeset(p, n.kids[0], size)
	rw := 0.62 * size
	top := body.asc + 0.18*size
	w := rw + body.w + 0.1*size
	return mbox{
		w: w, asc: top + 0.1*size, desc: body.desc,
		draw: func(p Painter, x, y float64) {
			p.SetLineWidth(1.2)
			p.BeginPath()
			p.MoveTo(x+0.04*size, y-0.28*size)
			p.LineTo(x+0.2*size, y-0.34*size)
			p.LineTo(x+0.36*size, y+body.desc)
			p.LineTo(x+rw-0.04*size, y-top)
			p.LineTo(x+w, y-top)
			p.Stroke()
			body.draw(p, x+rw, y)
		},
	}
}

// MathSymbols lists the symbols of a formula in the order they are read (numbers, identifiers, operators, function
// names, sum and integral signs), as the typesetter and the MathML export both see them.
func MathSymbols(formula string) []string {
	var out []string
	var walk func(n *mnode)
	walk = func(n *mnode) {
		if n == nil {
			return
		}
		switch n.kind {
		case mIdent, mNum, mOp, mFunc, mBig:
			out = append(out, n.text)
		}
		for _, k := range n.kids {
			walk(k)
		}
	}
	walk(parseMath(formula))
	return out
}
