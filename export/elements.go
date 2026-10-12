package export

import (
	"strings"
	"unicode"
)

// Equations, code blocks and footnotes in the exports.
//
// An equation is a Paragraph block with Block.Equation set to the formula in a small linear notation
// and its Segments set to a text alternative (what a person would read aloud). Every format shows the
// text alternative as the paragraph; the HTML export also writes MathML built from the formula, with the
// alternative as its alttext and as a text/plain annotation, so a screen reader without MathML support
// still reads the words.
//
// The notation: numbers, letters, the operators + - * / = < > ( ) [ ] , ; : |, ^ for a superscript and _ for
// a subscript (each takes the next number, letter or {group}), {...} to group, sqrt{x} for a square
// root, frac{a}{b} for a fraction, and the names pi alpha beta gamma delta theta lambda mu sigma omega
// infty sum int for their symbols. A run of letters that is a function name (sin cos tan log ln exp lim)
// is one identifier, any other run is one identifier per letter.

var mathNames = map[string]string{
	"pi": "π", "alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "theta": "θ", "lambda": "λ", "mu": "μ",
	"sigma": "σ", "omega": "ω", "infty": "∞", "sum": "∑", "int": "∫",
}

var mathFuncs = map[string]bool{"sin": true, "cos": true, "tan": true, "log": true, "ln": true, "exp": true, "lim": true}

// EquationBlock is a block for a formula with its text alternative. When alt is empty the formula itself
// is the text, so that no export shows an equation as an empty paragraph.
func EquationBlock(formula, alt string) Block {
	if strings.TrimSpace(alt) == "" {
		alt = formula
	}
	return Block{Kind: Paragraph, Equation: formula, Segments: []Segment{{Text: alt}}}
}

// MathML writes the formula as MathML (a <math> element in the MathML namespace). Text in the formula is
// escaped; a formula with unbalanced braces is closed leniently, so the result is always well formed.
func MathML(formula, alt string) string {
	alt = strings.Map(func(r rune) rune {
		if xmlChar(r) {
			return r
		}
		return -1
	}, alt)
	p := &mathParser{src: []rune(formula)}
	body := p.row(false)
	var sb strings.Builder
	sb.WriteString(`<math xmlns="http://www.w3.org/1998/Math/MathML" display="block" alttext="` + escapeHTMLAttr(alt) + `"><semantics><mrow>`)
	sb.WriteString(body)
	sb.WriteString(`</mrow><annotation encoding="text/plain">` + escapeHTMLText(alt) + `</annotation></semantics></math>`)
	return sb.String()
}

type mathParser struct {
	src []rune
	pos int
}

func (p *mathParser) more() bool { return p.pos < len(p.src) }

// row reads atoms up to the end, or up to the closing brace when inGroup.
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

// script reads the ^ and _ after an atom.
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

// operand is the single atom or {group} a script or a function takes.
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
		// Not a name: one identifier per letter, the last one being the one a script applies to.
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

// cleanCodeLang makes a language name safe for a class name or a code fence: lower-case letters, digits
// and the characters + - #, at most 20 of them; anything else makes it empty.
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

// xmlChar says XML 1.0 can carry r.
func xmlChar(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF
}
