package docximport

import "strings"

// Office Math (<m:oMath>) is read back into the editor's linear notation (frac{a}{b}, sqrt{x}, x^2, x_i, pi, sum, ...):
// the fraction, root, scripts, delimiters, n-ary operators (sum and integral), functions and plain runs. Anything
// else (matrices, accents, bars, limits above and below) keeps the text it has, without its layout.

var ommlSymbols = map[rune]string{
	'π': "pi", 'α': "alpha", 'β': "beta", 'γ': "gamma", 'δ': "delta", 'θ': "theta", 'λ': "lambda", 'μ': "mu",
	'σ': "sigma", 'ω': "omega", '∞': "infty", '∑': "sum", '∫': "int",
}

// linearFormula is the formula of an oMath or oMathPara node in the linear notation.
func linearFormula(n *node) string {
	var f strings.Builder
	var walk func(n *node, out *strings.Builder)
	group := func(n *node) string {
		var sb strings.Builder
		walk(n, &sb)
		s := strings.TrimSpace(sb.String())
		if len([]rune(s)) <= 1 || isSymbolName(s) {
			return " " + s + " " // one thing: a letter, a digit or a name like pi or sum
		}
		return "{" + s + "}"
	}
	braced := func(n *node) string {
		var sb strings.Builder
		walk(n, &sb)
		return "{" + sb.String() + "}"
	}
	walk = func(n *node, out *strings.Builder) {
		if n == nil {
			return
		}
		switch n.name {
		case "t":
			// a run of letters that spells a name of the notation (pi, frac, ...) is letters, not the name
			if rs := []rune(n.text); len(rs) > 1 && notationWord(n.text) {
				out.WriteString(" " + strings.Join(strings.Split(n.text, ""), " ") + " ")
				return
			}
			for _, r := range n.text {
				switch {
				case ommlSymbols[r] != "":
					out.WriteString(" " + ommlSymbols[r] + " ")
				case r == '{' || r == '}' || r == '^' || r == '_':
					out.WriteRune(' ')
				default:
					out.WriteRune(r)
				}
			}
			return
		case "sSup", "sSub", "sSubSup":
			out.WriteString(group(n.child("e")))
			if s := n.child("sub"); s != nil {
				out.WriteString("_" + group(s))
			}
			if s := n.child("sup"); s != nil {
				out.WriteString("^" + group(s))
			}
			return
		case "f":
			out.WriteString("frac" + braced(n.child("num")) + braced(n.child("den")))
			return
		case "rad":
			out.WriteString("sqrt" + braced(n.child("e")))
			return
		case "d":
			out.WriteString("(")
			for i, e := range n.children("e") {
				if i > 0 {
					out.WriteString(",")
				}
				walk(e, out)
			}
			out.WriteString(")")
			return
		case "nary":
			op := "sum"
			if pr := n.child("naryPr"); pr != nil {
				if c := pr.child("chr"); c != nil && strings.HasPrefix(c.attr("val"), "∫") {
					op = "int"
				}
			}
			out.WriteString(" " + op + " ")
			if s := n.child("sub"); s != nil {
				out.WriteString("_" + group(s))
			}
			if s := n.child("sup"); s != nil {
				out.WriteString("^" + group(s))
			}
			walk(n.child("e"), out)
			return
		case "oMathPara", "oMath", "e", "num", "den", "sub", "sup", "deg", "fName", "func", "r", "lim", "limLow", "limUpp", "acc", "bar", "box", "groupChr", "phant", "m", "mr", "eqArr", "sPre", "sSupPr":
		}
		for _, k := range n.kids {
			switch k.name {
			case "rPr", "dPr", "fPr", "radPr", "naryPr", "sSupPr", "sSubPr", "ctrlPr", "funcPr", "accPr", "barPr":
				continue // properties, not content
			}
			walk(k, out)
		}
	}
	walk(n, &f)
	return strings.Join(strings.Fields(f.String()), " ")
}

// isSymbolName says s is one of the notation's symbol names (pi, sum, infty, ...).
func isSymbolName(s string) bool {
	for _, v := range ommlSymbols {
		if v == s {
			return true
		}
	}
	return false
}

// notationWord says s, as letters, would be read as a name or a keyword of the notation rather than as separate
// identifiers; function names (sin, cos, ...) are meant as names and are not in this set.
func notationWord(s string) bool {
	return s == "frac" || s == "sqrt" || isSymbolName(s)
}
