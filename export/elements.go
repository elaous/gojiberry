package export

import (
	"strings"
	"unicode"
)

var mathNames = map[string]string{
	"pi": "π", "alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "theta": "θ", "lambda": "λ", "mu": "μ",
	"sigma": "σ", "omega": "ω", "infty": "∞", "sum": "∑", "int": "∫",
}

var mathFuncs = map[string]bool{"sin": true, "cos": true, "tan": true, "log": true, "ln": true, "exp": true, "lim": true}

// EquationBlock is a formula block with its text alternative.
func EquationBlock(formula, alt string) Block {
	if strings.TrimSpace(alt) == "" {
		alt = formula
	}
	return Block{Kind: Paragraph, Equation: formula, Segments: []Segment{{Text: alt}}}
}

// MathML writes a formula as a MathML math element.
func MathML(formula, alt string) string { return mathML(formula, alt, "block") }

// MathMLInline is MathML for a formula inside a line.
func MathMLInline(formula, alt string) string { return mathML(formula, alt, "inline") }

func mathML(formula, alt, display string) string {
	alt = strings.Map(func(r rune) rune {
		if xmlChar(r) {
			return r
		}
		return -1
	}, alt)
	p := &mathParser{src: []rune(formula)}
	body := p.row(false)
	var sb strings.Builder
	sb.WriteString(`<math xmlns="http://www.w3.org/1998/Math/MathML" display="` + display + `" alttext="` + escapeHTMLAttr(alt) + `"><semantics><mrow>`)
	sb.WriteString(body)
	sb.WriteString(`</mrow><annotation encoding="text/plain">` + escapeHTMLText(alt) + `</annotation></semantics></math>`)
	return sb.String()
}

type mathParser struct {
	src []rune
	pos int
}

func (p *mathParser) more() bool { return p.pos < len(p.src) }

func (p *mathParser) nextIs(r rune) bool {
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

func (p *mathParser) table(kind string) string {
	var rows, cells strings.Builder
	var cell strings.Builder
	endCell := func() { cells.WriteString("<mtd><mrow>" + cell.String() + "</mrow></mtd>"); cell.Reset() }
	endRow := func() { endCell(); rows.WriteString("<mtr>" + cells.String() + "</mtr>"); cells.Reset() }
	finish := func() string {
		attr := ""
		if kind == "aligned" {
			attr = ` columnalign="right left" columnspacing="0"`
			return "<mtable" + attr + ">" + rows.String() + "</mtable>"
		}
		return "<mrow><mo>(</mo><mtable>" + rows.String() + "</mtable><mo>)</mo></mrow>"
	}
	for p.more() {
		if unicode.IsSpace(p.src[p.pos]) {
			p.pos++
			continue
		}
		switch c := p.src[p.pos]; {
		case c == '}':
			p.pos++
			endRow()
			return finish()
		case c == '&':
			p.pos++
			endCell()
		case c == '\\' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '\\':
			p.pos += 2
			endRow()
		default:
			cell.WriteString(p.script(p.atom()))
		}
	}
	endRow()
	return finish()
}

func (p *mathParser) row(inGroup bool) string {
	var sb strings.Builder
	for p.more() {
		r := p.src[p.pos]
		if r == '}' {
			p.pos++
			if inGroup {
				return sb.String()
			}
			continue // a stray closing brace
		}
		sb.WriteString(p.script(p.atom()))
	}
	return sb.String()
}

func (p *mathParser) script(base string) string {
	var sub, sup string
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
	switch {
	case sub != "" && sup != "":
		return "<msubsup>" + base + sub + sup + "</msubsup>"
	case sup != "":
		return "<msup>" + base + sup + "</msup>"
	case sub != "":
		return "<msub>" + base + sub + "</msub>"
	}
	return base
}

func (p *mathParser) operand() string {
	if !p.more() {
		return "<mrow></mrow>"
	}
	if p.src[p.pos] == '{' {
		p.pos++
		return "<mrow>" + p.row(true) + "</mrow>"
	}
	return p.atom()
}

func (p *mathParser) atom() string {
	for p.more() && unicode.IsSpace(p.src[p.pos]) {
		p.pos++
	}
	if !p.more() {
		return ""
	}
	r := p.src[p.pos]
	if !xmlChar(r) || unicode.IsControl(r) {
		p.pos++ // not a character XML can carry
		return ""
	}
	switch {
	case r == '{':
		return p.operand()
	case r == '^' || r == '_':
		return "<mrow></mrow>" // a script with nothing before it
	case unicode.IsDigit(r):
		start := p.pos
		for p.more() && (unicode.IsDigit(p.src[p.pos]) || p.src[p.pos] == '.') {
			p.pos++
		}
		return "<mn>" + escapeHTMLText(string(p.src[start:p.pos])) + "</mn>"
	case unicode.IsLetter(r):
		start := p.pos
		for p.more() && unicode.IsLetter(p.src[p.pos]) {
			p.pos++
		}
		word := string(p.src[start:p.pos])
		switch {
		case (word == "matrix" || word == "aligned") && p.nextIs('{'):
			p.pos++
			return p.table(word)
		case word == "sqrt":
			return "<msqrt>" + p.operand() + "</msqrt>"
		case word == "frac":
			num := p.operand()
			return "<mfrac>" + num + p.operand() + "</mfrac>"
		case mathNames[word] != "":
			if word == "sum" || word == "int" {
				return "<mo>" + mathNames[word] + "</mo>"
			}
			return "<mi>" + mathNames[word] + "</mi>"
		case mathFuncs[word]:
			return "<mi>" + word + "</mi>"
		}
		letters := []rune(word)
		if len(letters) > 1 {
			p.pos -= 1
			return p.identifiers(letters[:len(letters)-1])
		}
		return "<mi>" + escapeHTMLText(word) + "</mi>"
	}
	p.pos++
	return "<mo>" + escapeHTMLText(string(r)) + "</mo>"
}

func (p *mathParser) identifiers(letters []rune) string {
	var sb strings.Builder
	for _, l := range letters {
		sb.WriteString("<mi>" + escapeHTMLText(string(l)) + "</mi>")
	}
	return sb.String()
}

// CodeLang is lang as a code language name, or empty.
func CodeLang(lang string) string { return cleanCodeLang(lang) }

func cleanCodeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" || len(lang) > 20 {
		return ""
	}
	for _, r := range lang {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '+' && r != '-' && r != '#' {
			return ""
		}
	}
	return lang
}

func xmlChar(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF
}
