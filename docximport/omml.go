package docximport

import "strings"

var ommlSymbols = map[rune]string{
	'π': "pi", 'α': "alpha", 'β': "beta", 'γ': "gamma", 'δ': "delta", 'θ': "theta", 'λ': "lambda", 'μ': "mu",
	'σ': "sigma", 'ω': "omega", '∞': "infty", '∑': "sum", '∫': "int",
}

func linearFormula(n *node) string {
	var f strings.Builder
	var walk func(n *node, out *strings.Builder)
	group := func(n *node) string {
		var sb strings.Builder
		walk(n, &sb)
		s := strings.TrimSpace(sb.String())
		if len([]rune(s)) <= 1 || isSymbolName(s) {
			return " " + s + " "
		}
		return "{" + s + "}"
	}
	braced := func(n *node) string {
		var sb strings.Builder
		walk(n, &sb)
		return "{" + sb.String() + "}"
	}
	rows := func(rs []*node, cells func(*node) []*node, sep string) string {
		var lines []string
		for _, r := range rs {
			var cs []string
			for _, c := range cells(r) {
				var sb strings.Builder
				walk(c, &sb)
				cs = append(cs, strings.Join(strings.Fields(sb.String()), " "))
			}
			lines = append(lines, strings.Join(cs, sep))
		}
		return strings.Join(lines, ` \\ `)
	}
	walk = func(n *node, out *strings.Builder) {
		if n == nil {
			return
		}
		switch n.name {
		case "t":
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
		case "m":
			out.WriteString("matrix{" + rows(n.children("mr"), func(mr *node) []*node { return mr.children("e") }, " & ") + "}")
			return
		case "eqArr":
			var lines []string
			for _, e := range n.children("e") {
				var sb strings.Builder
				walk(e, &sb)
				lines = append(lines, strings.Join(strings.Fields(strings.ReplaceAll(sb.String(), "&", " & ")), " "))
			}
			out.WriteString("aligned{" + strings.Join(lines, ` \\ `) + "}")
			return
		case "d":
			if es := n.children("e"); len(es) == 1 && es[0].child("m") != nil && len(es[0].kids) == 1 {
				walk(es[0], out)
				return
			}
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
		case "oMathPara", "oMath", "e", "num", "den", "sub", "sup", "deg", "fName", "func", "r", "lim", "limLow", "limUpp", "acc", "bar", "box", "groupChr", "phant", "mr", "sPre", "sSupPr":
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

func isSymbolName(s string) bool {
	for _, v := range ommlSymbols {
		if v == s {
			return true
		}
	}
	return false
}

func notationWord(s string) bool {
	return s == "frac" || s == "sqrt" || isSymbolName(s)
}
