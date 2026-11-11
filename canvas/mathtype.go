package canvas

import (
	"strconv"
	"strings"
	"unicode"
)

type mkind int

const (
	mRow   mkind = iota // kids in a line
	mIdent              // a variable: italic
	mNum                // a number: upright
	mOp                 // an operator or a bracket
	mFunc               // a function name: upright
	mBig                // sum, integral
	mSqrt               // kids[0] under a radical
	mFrac               // kids[0] over kids[1]
	mScript
	mTable
)

type mnode struct {
	kind   mkind
	text   string
	kids   []*mnode
	limits bool
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

// unbalanced braces are closed leniently: any text gives a tree
func parseMath(formula string) *mnode {
	p := &mparser{src: []rune(formula)}
	return p.row(false)
}

func (p *mparser) more() bool { return p.pos < len(p.src) }

func (p *mparser) nextIs(r rune) bool {
	i := p.pos
	for i < len(p.src) && unicode.IsSpace(p.src[i]) {
		i++
	}
	if i < len(p.src) && p.src[i] == r {
		p.pos = i
		return true
	}
	return false
}

func (p *mparser) table(kind string) *mnode {
	t := &mnode{kind: mTable, text: kind}
	row, cell := &mnode{kind: mRow}, &mnode{kind: mRow}
	endCell := func() { row.kids = append(row.kids, cell); cell = &mnode{kind: mRow} }
	endRow := func() { endCell(); t.kids = append(t.kids, row); row = &mnode{kind: mRow} }
	for p.more() {
		if unicode.IsSpace(p.src[p.pos]) {
			p.pos++
			continue
		}
		switch c := p.src[p.pos]; {
		case c == '}':
			p.pos++
			endRow()
			return t
		case c == '&':
			p.pos++
			endCell()
		case c == '\\' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '\\':
			p.pos += 2
			endRow()
		default:
			if a := p.script(p.atom()); a != nil {
				cell.kids = append(cell.kids, a)
			}
		}
	}
	endRow()
	return t
}

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
		case (word == "matrix" || word == "aligned") && p.nextIs('{'):
			p.pos++
			return p.table(word)
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
		if len(letters) > 1 {
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

type mbox struct {
	w, asc, desc float64
	draw         func(p Painter, x, y float64)
}

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
	case mTable:
		return typesetTable(p, n, size)
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

func binary(op string) bool { return strings.Contains("+-*=<>", op) }

func typesetRow(p Painter, n *mnode, size float64) mbox {
	type placed struct {
		b mbox
		x float64
	}
	var parts []placed
	x := 0.0
	prevOperand := false
	boxes := make([]mbox, len(n.kids))
	for i, k := range n.kids {
		boxes[i] = typeset(p, k, size)
	}
	growBrackets(n.kids, boxes, size)
	for ki, k := range n.kids {
		b := boxes[ki]
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
	if n.limits {
		return typesetLimits(p, n, base, small)
	}
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

// MathSymbols lists the symbols of a formula in reading order.
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
		matrix := n.kind == mTable && n.text == "matrix"
		if matrix {
			out = append(out, "(")
		}
		for _, k := range n.kids {
			walk(k)
		}
		if matrix {
			out = append(out, ")")
		}
	}
	walk(parseMath(formula))
	return out
}

func markLimits(n *mnode) {
	if n == nil {
		return
	}
	if n.kind == mScript && n.kids[0] != nil && (n.kids[0].kind == mBig && n.kids[0].text == "∑" || n.kids[0].kind == mFunc && n.kids[0].text == "lim") {
		n.limits = true
	}
	for _, k := range n.kids {
		markLimits(k)
	}
}

func typesetLimits(p Painter, n *mnode, base mbox, small float64) mbox {
	var sub, sup *mbox
	if n.kids[1] != nil {
		b := typeset(p, n.kids[1], small)
		sub = &b
	}
	if n.kids[2] != nil {
		b := typeset(p, n.kids[2], small)
		sup = &b
	}
	gap := 0.12 * small
	w := base.w
	out := mbox{asc: base.asc, desc: base.desc}
	if sup != nil {
		w = max(w, sup.w)
		out.asc += gap + sup.asc + sup.desc
	}
	if sub != nil {
		w = max(w, sub.w)
		out.desc += gap + sub.asc + sub.desc
	}
	out.w = w
	supY, subY := -(base.asc + gap + 0.5*small), base.desc+gap+0.75*small
	if sup != nil {
		supY = -(base.asc + gap + sup.desc)
	}
	if sub != nil {
		subY = base.desc + gap + sub.asc
	}
	out.draw = func(p Painter, x, y float64) {
		base.draw(p, x+(w-base.w)/2, y)
		if sup != nil {
			sup.draw(p, x+(w-sup.w)/2, y+supY)
		}
		if sub != nil {
			sub.draw(p, x+(w-sub.w)/2, y+subY)
		}
	}
	return out
}

func typesetTable(p Painter, n *mnode, size float64) mbox {
	cols := 0
	for _, r := range n.kids {
		cols = max(cols, len(r.kids))
	}
	cells := make([][]mbox, len(n.kids))
	colW := make([]float64, cols)
	rowAsc := make([]float64, len(n.kids))
	rowDesc := make([]float64, len(n.kids))
	for i, r := range n.kids {
		for j, c := range r.kids {
			b := typeset(p, c, size)
			cells[i] = append(cells[i], b)
			colW[j] = max(colW[j], b.w)
			rowAsc[i], rowDesc[i] = max(rowAsc[i], b.asc), max(rowDesc[i], b.desc)
		}
	}
	colGap, rowGap := 0.9*size, 0.25*size
	if n.text == "aligned" {
		colGap = 0.1 * size
	}
	w := 0.0
	colX := make([]float64, cols)
	for j := range colW {
		colX[j] = w
		w += colW[j] + colGap
	}
	w = max(w-colGap, 0)
	rowY := make([]float64, len(n.kids)) // each row's baseline from the table's top
	h := 0.0
	for i := range n.kids {
		h += rowAsc[i]
		rowY[i] = h
		h += rowDesc[i] + rowGap
	}
	h = max(h-rowGap, 0)
	axis := 0.28 * size
	asc := h/2 + axis // the table is centred on the math axis
	desc := h - asc
	brackets := n.text == "matrix"
	pad := 0.0
	if brackets {
		pad = 0.45 * size
	}
	out := mbox{w: w + 2*pad, asc: asc, desc: desc}
	out.draw = func(p Painter, x, y float64) {
		top := y - asc
		for i, r := range cells {
			for j, b := range r {
				cx := x + pad + colX[j] + (colW[j]-b.w)/2
				switch {
				case n.text == "aligned" && j%2 == 0:
					cx = x + pad + colX[j] + colW[j] - b.w
				case n.text == "aligned":
					cx = x + pad + colX[j]
				}
				b.draw(p, cx, top+rowY[i])
			}
		}
		if brackets {
			drawBracket(p, "(", x, top, h, size)
			drawBracket(p, ")", x+out.w, top, h, size)
		}
	}
	return out
}

func drawBracket(p Painter, kind string, x, top, h, size float64) {
	d := 0.28 * size
	dir := 1.0
	if kind == ")" || kind == "]" {
		dir = -1
	}
	p.SetLineWidth(1.2)
	p.BeginPath()
	switch kind {
	case "[", "]":
		p.MoveTo(x+dir*d, top)
		p.LineTo(x, top)
		p.LineTo(x, top+h)
		p.LineTo(x+dir*d, top+h)
	case "|":
		p.MoveTo(x, top)
		p.LineTo(x, top+h)
	default:
		const steps = 12
		for i := 0; i <= steps; i++ {
			t := float64(i)/steps*2 - 1
			px, py := x+dir*d*t*t, top+float64(i)/steps*h
			if i == 0 {
				p.MoveTo(px, py)
			} else {
				p.LineTo(px, py)
			}
		}
	}
	p.Stroke()
}

func growBrackets(kids []*mnode, boxes []mbox, size float64) {
	var open []int
	for i, k := range kids {
		if k.kind != mOp {
			continue
		}
		switch k.text {
		case "(", "[":
			open = append(open, i)
		case ")", "]":
			if len(open) == 0 {
				continue
			}
			o := open[len(open)-1]
			open = open[:len(open)-1]
			if (kids[o].text == "(") != (k.text == ")") {
				continue
			}
			var asc, desc float64
			for _, b := range boxes[o+1 : i] {
				asc, desc = max(asc, b.asc), max(desc, b.desc)
			}
			if asc+desc <= 1.5*size {
				continue
			}
			h := asc + desc + 0.1*size
			for _, at := range [2]int{o, i} {
				kind := kids[at].text
				w := 0.4 * size
				left := at == o
				boxes[at] = mbox{w: w, asc: asc + 0.05*size, desc: desc + 0.05*size, draw: func(p Painter, x, y float64) {
					edge := x + 0.05*size
					if !left {
						edge = x + w - 0.05*size
					}
					drawBracket(p, kind, edge, y-asc-0.05*size, h, size)
				}}
			}
		}
	}
}
