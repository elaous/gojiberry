package export

import (
	"encoding/xml"
	"strings"
)

// OMML writes a formula as Office Math.
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

type mmlNode struct {
	name    string
	kids    []*mmlNode
	text    string
	aligned bool
}

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
			for _, a := range t.Attr {
				if a.Name.Local == "columnalign" {
					n.aligned = true
				}
			}
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
		if len(n.kids) == 3 && n.kids[1].name == "mtable" && n.kids[0].name == "mo" && n.kids[2].name == "mo" {
			return `<m:d><m:e>` + ommlOf(n.kids[1]) + `</m:e></m:d>` // a matrix between its brackets
		}
		return ommlRow(n)
	case "mtable":
		var sb strings.Builder
		if n.aligned {
			sb.WriteString(`<m:eqArr>`)
			for _, r := range n.kids {
				sb.WriteString(`<m:e>`)
				for i, c := range r.kids {
					if i > 0 {
						sb.WriteString(ommlRun("&", false))
					}
					sb.WriteString(ommlRow(mmlKid(c, 0)))
				}
				sb.WriteString(`</m:e>`)
			}
			sb.WriteString(`</m:eqArr>`)
			return sb.String()
		}
		sb.WriteString(`<m:m>`)
		for _, r := range n.kids {
			sb.WriteString(`<m:mr>`)
			for _, c := range r.kids {
				sb.WriteString(`<m:e>` + ommlRow(mmlKid(c, 0)) + `</m:e>`)
			}
			sb.WriteString(`</m:mr>`)
		}
		sb.WriteString(`</m:m>`)
		return sb.String()
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
