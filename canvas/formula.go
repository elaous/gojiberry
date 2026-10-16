package canvas

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Table formulas. A cell whose text starts with "=" is a formula over the other cells of the same table:
//
//	=A1+B1*2        =SUM(B2:B9)        =ROUND(AVERAGE(C1:C4), 1)        =IF(D2>=10, "ok", "low")
//
// Cell names are a column letter (A, B, ... Z, AA, ...) and a row number from 1, as in a spreadsheet. The text stays
// in the cell, so it is saved and edited as it is; what a reader sees (the canvas, the exports, the reading copy) is
// the value, computed by Values. A formula that cannot be computed shows an error code in its place (#REF!, #DIV/0!,
// #VALUE!, #NAME?, #CYCLE!, #ERR!) instead of stopping anything. Functions: SUM, AVERAGE, MIN, MAX, COUNT, ROUND,
// ABS, IF. Operators: + - * / ^ and comparisons = <> < > <= >=.

// Values returns the cells of a grid as a reader sees them: formulas computed, everything else as written.
func Values(cells [][]string) [][]string {
	out := make([][]string, len(cells))
	ev := &formulaEval{cells: cells, done: map[[2]int]fval{}, busy: map[[2]int]bool{}}
	for r := range cells {
		out[r] = make([]string, len(cells[r]))
		for c, text := range cells[r] {
			if isFormula(text) {
				out[r][c] = ev.cell(r, c).show()
			} else {
				out[r][c] = text
			}
		}
	}
	return out
}

// Values is the table's cells as a reader sees them (see Values).
func (t *Table) Values() [][]string { return Values(t.Cells) }

// Display is one cell as a reader sees it: the value of a formula, else the text.
func (t *Table) Display(row, col int) string {
	text := t.Cell(row, col)
	if !isFormula(text) {
		return text
	}
	ev := &formulaEval{cells: t.Cells, done: map[[2]int]fval{}, busy: map[[2]int]bool{}}
	return ev.cell(row, col).show()
}

func isFormula(s string) bool { return strings.HasPrefix(strings.TrimSpace(s), "=") }

type fval struct {
	n   float64
	s   string
	str bool
	err string
}

func (v fval) show() string {
	switch {
	case v.err != "":
		return v.err
	case v.str:
		return v.s
	}
	return strconv.FormatFloat(v.n, 'g', 10, 64)
}

func fnum(n float64) fval  { return fval{n: n} }
func ferr(e string) fval   { return fval{err: e} }
func fbool(b bool) fval    { return fnum(map[bool]float64{true: 1, false: 0}[b]) }
func fstr(s string) fval   { return fval{s: s, str: true} }
func (v fval) isErr() bool { return v.err != "" }
func (v fval) number() fval { // a value used as a number
	if v.err != "" || !v.str {
		return v
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(v.s), 64); err == nil {
		return fnum(f)
	}
	if strings.TrimSpace(v.s) == "" {
		return fnum(0)
	}
	return ferr("#VALUE!")
}

type formulaEval struct {
	cells [][]string
	done  map[[2]int]fval
	busy  map[[2]int]bool
}

// cell is the value of one cell: a formula is computed (once), anything else is a number when it reads as one.
func (e *formulaEval) cell(r, c int) fval {
	if r < 0 || r >= len(e.cells) || c < 0 || c >= len(e.cells[r]) {
		return ferr("#REF!")
	}
	key := [2]int{r, c}
	if v, ok := e.done[key]; ok {
		return v
	}
	text := e.cells[r][c]
	if !isFormula(text) {
		return fstr(text)
	}
	if e.busy[key] {
		return ferr("#CYCLE!")
	}
	e.busy[key] = true
	p := &formulaParser{e: e, toks: tokenize(strings.TrimSpace(text)[1:])}
	v := p.parse()
	delete(e.busy, key)
	e.done[key] = v
	return v
}

// --- tokens ---------------------------------------------------------

type ftok struct {
	kind byte // n number, s string, i identifier, o operator or punctuation, x bad
	text string
}

func tokenize(s string) []ftok {
	var out []ftok
	rs := []rune(s)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case unicode.IsDigit(c) || c == '.':
			j := i
			for j < len(rs) && (unicode.IsDigit(rs[j]) || rs[j] == '.') {
				j++
			}
			out = append(out, ftok{'n', string(rs[i:j])})
			i = j
		case c == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			if j >= len(rs) {
				return append(out, ftok{'x', ""})
			}
			out = append(out, ftok{'s', string(rs[i+1 : j])})
			i = j + 1
		case unicode.IsLetter(c) || c == '$':
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '$' || rs[j] == '_') {
				j++
			}
			out = append(out, ftok{'i', strings.ReplaceAll(string(rs[i:j]), "$", "")})
			i = j
		case strings.ContainsRune("+-*/^(),:%", c):
			out = append(out, ftok{'o', string(c)})
			i++
		case c == '<' || c == '>' || c == '=':
			t := string(c)
			if i+1 < len(rs) && (rs[i+1] == '=' || (c == '<' && rs[i+1] == '>')) {
				t += string(rs[i+1])
				i++
			}
			out = append(out, ftok{'o', t})
			i++
		default:
			return append(out, ftok{'x', string(c)})
		}
	}
	return out
}

// --- parser and evaluator (recursive descent, evaluating as it goes) -----

type formulaParser struct {
	e    *formulaEval
	toks []ftok
	pos  int
}

func (p *formulaParser) peek() ftok {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return ftok{}
}

func (p *formulaParser) is(op string) bool { t := p.peek(); return t.kind == 'o' && t.text == op }

func (p *formulaParser) parse() fval {
	v := p.compare()
	if p.pos < len(p.toks) && !v.isErr() {
		return ferr("#ERR!") // something left over
	}
	return v
}

func (p *formulaParser) compare() fval {
	l := p.sum()
	for {
		t := p.peek()
		if t.kind != 'o' {
			return l
		}
		switch t.text {
		case "=", "<>", "<", ">", "<=", ">=":
		default:
			return l
		}
		p.pos++
		r := p.sum()
		if l.isErr() {
			return l
		}
		if r.isErr() {
			return r
		}
		var cmp int
		if l.str && r.str {
			cmp = strings.Compare(strings.ToLower(l.s), strings.ToLower(r.s))
		} else {
			a, b := l.number(), r.number()
			if a.isErr() {
				return a
			}
			if b.isErr() {
				return b
			}
			switch {
			case a.n < b.n:
				cmp = -1
			case a.n > b.n:
				cmp = 1
			}
		}
		res := map[string]bool{"=": cmp == 0, "<>": cmp != 0, "<": cmp < 0, ">": cmp > 0, "<=": cmp <= 0, ">=": cmp >= 0}[t.text]
		l = fbool(res)
	}
}

func (p *formulaParser) sum() fval {
	l := p.term()
	for p.is("+") || p.is("-") {
		op := p.peek().text
		p.pos++
		r := p.term()
		a, b := l.number(), r.number()
		if a.isErr() {
			l = a
			continue
		}
		if b.isErr() {
			l = b
			continue
		}
		if op == "+" {
			l = fnum(a.n + b.n)
		} else {
			l = fnum(a.n - b.n)
		}
	}
	return l
}

func (p *formulaParser) term() fval {
	l := p.power()
	for p.is("*") || p.is("/") {
		op := p.peek().text
		p.pos++
		r := p.power()
		a, b := l.number(), r.number()
		switch {
		case a.isErr():
			l = a
		case b.isErr():
			l = b
		case op == "*":
			l = fnum(a.n * b.n)
		case b.n == 0:
			l = ferr("#DIV/0!")
		default:
			l = fnum(a.n / b.n)
		}
	}
	return l
}

func (p *formulaParser) power() fval {
	l := p.unary()
	if p.is("^") {
		p.pos++
		r := p.power() // right to left
		a, b := l.number(), r.number()
		switch {
		case a.isErr():
			return a
		case b.isErr():
			return b
		}
		v := math.Pow(a.n, b.n)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ferr("#VALUE!")
		}
		return fnum(v)
	}
	return l
}

func (p *formulaParser) unary() fval {
	if p.is("-") {
		p.pos++
		v := p.unary().number()
		if v.isErr() {
			return v
		}
		return fnum(-v.n)
	}
	if p.is("+") {
		p.pos++
		return p.unary()
	}
	v := p.primary()
	for p.is("%") { // 5% is 0.05
		p.pos++
		n := v.number()
		if n.isErr() {
			return n
		}
		v = fnum(n.n / 100)
	}
	return v
}

func (p *formulaParser) primary() fval {
	t := p.peek()
	switch {
	case t.kind == 'n':
		p.pos++
		f, err := strconv.ParseFloat(t.text, 64)
		if err != nil {
			return ferr("#ERR!")
		}
		return fnum(f)
	case t.kind == 's':
		p.pos++
		return fstr(t.text)
	case t.kind == 'o' && t.text == "(":
		p.pos++
		v := p.compare()
		if !p.is(")") {
			return ferr("#ERR!")
		}
		p.pos++
		return v
	case t.kind == 'i':
		p.pos++
		if p.is("(") {
			return p.call(strings.ToUpper(t.text))
		}
		r, c, ok := cellName(t.text)
		if !ok {
			return ferr("#NAME?")
		}
		if p.is(":") { // a range outside a function
			return ferr("#VALUE!")
		}
		return p.e.cell(r, c).number()
	}
	return ferr("#ERR!")
}

// call evaluates NAME(args); the opening parenthesis is next.
func (p *formulaParser) call(name string) fval {
	p.pos++ // (
	var nums []float64
	var args []fval
	for !p.is(")") {
		if p.pos >= len(p.toks) {
			return ferr("#ERR!")
		}
		if t := p.peek(); t.kind == 'i' && p.pos+2 < len(p.toks) && p.toks[p.pos+1].text == ":" && p.toks[p.pos+2].kind == 'i' {
			r1, c1, ok1 := cellName(t.text)
			r2, c2, ok2 := cellName(p.toks[p.pos+2].text)
			if !ok1 || !ok2 {
				return ferr("#NAME?")
			}
			p.pos += 3
			for r := min(r1, r2); r <= max(r1, r2); r++ {
				for c := min(c1, c2); c <= max(c1, c2); c++ {
					v := p.e.cell(r, c)
					if v.isErr() {
						return v
					}
					if f := v.number(); !f.isErr() && (!v.str || strings.TrimSpace(v.s) != "") {
						nums = append(nums, f.n)
					} // text and empty cells in a range are left out
				}
			}
			args = append(args, fval{n: float64(len(nums))})
		} else {
			v := p.compare()
			if v.isErr() {
				return v
			}
			args = append(args, v)
			if f := v.number(); !f.isErr() {
				nums = append(nums, f.n)
			}
		}
		if p.is(",") {
			p.pos++
		} else if !p.is(")") {
			return ferr("#ERR!")
		}
	}
	p.pos++ // )
	switch name {
	case "SUM":
		s := 0.0
		for _, n := range nums {
			s += n
		}
		return fnum(s)
	case "COUNT":
		return fnum(float64(len(nums)))
	case "AVERAGE":
		if len(nums) == 0 {
			return ferr("#DIV/0!")
		}
		s := 0.0
		for _, n := range nums {
			s += n
		}
		return fnum(s / float64(len(nums)))
	case "MIN", "MAX":
		if len(nums) == 0 {
			return fnum(0)
		}
		m := nums[0]
		for _, n := range nums[1:] {
			if (name == "MIN" && n < m) || (name == "MAX" && n > m) {
				m = n
			}
		}
		return fnum(m)
	case "ABS":
		if len(args) != 1 {
			return ferr("#ERR!")
		}
		v := args[0].number()
		if v.isErr() {
			return v
		}
		return fnum(math.Abs(v.n))
	case "ROUND":
		if len(args) < 1 || len(args) > 2 {
			return ferr("#ERR!")
		}
		v := args[0].number()
		if v.isErr() {
			return v
		}
		digits := 0.0
		if len(args) == 2 {
			d := args[1].number()
			if d.isErr() {
				return d
			}
			digits = d.n
		}
		k := math.Pow(10, digits)
		return fnum(math.Round(v.n*k) / k)
	case "IF":
		if len(args) != 3 {
			return ferr("#ERR!")
		}
		c := args[0].number()
		if c.isErr() {
			return c
		}
		if c.n != 0 {
			return args[1]
		}
		return args[2]
	}
	return ferr("#NAME?")
}

// cellName reads "B7" as a row and a column (0-based).
func cellName(s string) (row, col int, ok bool) {
	i := 0
	for i < len(s) && (s[i]|0x20) >= 'a' && (s[i]|0x20) <= 'z' {
		col = col*26 + int(s[i]|0x20-'a') + 1
		i++
	}
	if i == 0 || i == len(s) || i > 3 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil || n < 1 {
		return 0, 0, false
	}
	return n - 1, col - 1, true
}
