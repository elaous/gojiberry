package export

import (
	"encoding/xml"
	"strings"
)

// OMML writes the formula as Office Math (the <m:oMath> element Word and LibreOffice typeset as an equation),
// converted from the MathML of the same notation, so that the two always agree. Text is escaped; a formula the
// notation cannot read comes out as the text it has. The fraction, the root, the scripts and the symbols of the
// notation have their Office Math forms; a function name is upright text.
func OMML(formula string) string {
	root := parseMathML(MathML(formula, ""))
	var sb strings.Builder
	sb.WriteString(`<m:oMath>`)
	for _, k := range mathBody(root) {
		sb.WriteString(ommlOf(k))
	}
	sb.WriteString(`</m:oMath>`)
	return sb.String()
}

// mmlNode is a MathML element: its name, its children and, for a token, its text.
type mmlNode struct {
	name string
	kids []*mmlNode
	text string
}

// parseMathML reads the well-formed MathML MathML() writes into a tree.
func parseMathML(s string) *mmlNode {
	dec := xml.NewDecoder(strings.NewReader(s))
	root := &mmlNode{name: "root"}
	stack := []*mmlNode{root}
	for {
		tok, err := dec.Token()
		if err != nil {
			return root
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &mmlNode{name: t.Name.Local}
			top := stack[len(stack)-1]
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			stack[len(stack)-1].text += string(t)
		}
	}
}

// mathBody is the children of the formula's row: <math><semantics><mrow>…</mrow><annotation/></semantics></math>.
func mathBody(root *mmlNode) []*mmlNode {
	n := root
	for _, name := range []string{"math", "semantics", "mrow"} {
		var next *mmlNode
		for _, k := range n.kids {
			if k.name == name {
				next = k
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n.kids
}

func ommlRun(text string, upright bool) string {
	if text == "" {
		return ""
	}
	rpr := ""
	if upright {
		rpr = `<m:rPr><m:sty m:val="p"/></m:rPr>`
	}
	return `<m:r>` + rpr + `<m:t xml:space="preserve">` + esc(text) + `</m:t></m:r>`
}

// ommlRow is the children of a row, or the one element.
func ommlRow(n *mmlNode) string {
	if n == nil {
		return ""
	}
	if n.name == "mrow" {
		var sb strings.Builder
		for _, k := range n.kids {
			sb.WriteString(ommlOf(k))
		}
		return sb.String()
	}
	return ommlOf(n)
}

func mmlKid(n *mmlNode, i int) *mmlNode {
	if i < len(n.kids) {
		return n.kids[i]
	}
	return nil
}

func ommlOf(n *mmlNode) string {
	switch n.name {
	case "mi":
		return ommlRun(n.text, mathFuncs[n.text])
	case "mn", "mo":
		return ommlRun(n.text, n.name == "mn")
	case "mrow":
		return ommlRow(n)
	case "msup":
		return `<m:sSup><m:e>` + ommlRow(mmlKid(n, 0)) + `</m:e><m:sup>` + ommlRow(mmlKid(n, 1)) + `</m:sup></m:sSup>`
	case "msub":
		return `<m:sSub><m:e>` + ommlRow(mmlKid(n, 0)) + `</m:e><m:sub>` + ommlRow(mmlKid(n, 1)) + `</m:sub></m:sSub>`
	case "msubsup":
		return `<m:sSubSup><m:e>` + ommlRow(mmlKid(n, 0)) + `</m:e><m:sub>` + ommlRow(mmlKid(n, 1)) + `</m:sub><m:sup>` + ommlRow(mmlKid(n, 2)) + `</m:sup></m:sSubSup>`
	case "mfrac":
		return `<m:f><m:num>` + ommlRow(mmlKid(n, 0)) + `</m:num><m:den>` + ommlRow(mmlKid(n, 1)) + `</m:den></m:f>`
	case "msqrt":
		var inner strings.Builder
		for _, k := range n.kids {
			inner.WriteString(ommlOf(k))
		}
		return `<m:rad><m:radPr><m:degHide m:val="1"/></m:radPr><m:deg/><m:e>` + inner.String() + `</m:e></m:rad>`
	}
	var sb strings.Builder
	for _, k := range n.kids {
		sb.WriteString(ommlOf(k))
	}
	return sb.String()
}
