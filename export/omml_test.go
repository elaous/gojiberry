package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
)

func TestOMMLHasTheOfficeMathFormOfEachPart(t *testing.T) {
	for _, c := range []struct{ formula, want string }{
		{"x^2", `<m:sSup><m:e><m:r><m:t xml:space="preserve">x</m:t></m:r></m:e><m:sup><m:r><m:rPr><m:sty m:val="p"/></m:rPr><m:t xml:space="preserve">2</m:t></m:r></m:sup></m:sSup>`},
		{"a_i", `<m:sSub>`},
		{"x_i^2", `<m:sSubSup>`},
		{"frac{1}{2}", `<m:f><m:num>`},
		{"sqrt{x+1}", `<m:rad><m:radPr><m:degHide m:val="1"/></m:radPr><m:deg/><m:e>`},
		{"pi r^2", `π`},
		{"sin x", `<m:rPr><m:sty m:val="p"/></m:rPr><m:t xml:space="preserve">sin</m:t>`},
		{"a<b", `&lt;`},
	} {
		got := OMML(c.formula)
		if !strings.HasPrefix(got, "<m:oMath>") || !strings.HasSuffix(got, "</m:oMath>") || !strings.Contains(got, c.want) {
			t.Errorf("%q gave %s, want it to hold %s", c.formula, got, c.want)
		}
	}
	// whatever the notation is given, the result is well formed
	for _, f := range []string{"", "{{", "}}", "^", "_2", "frac{1}", "sqrt", "a^{b^{c}}", "\x00x"} {
		if got := OMML(f); !strings.HasPrefix(got, "<m:oMath>") || strings.Count(got, "<m:oMath>") != 1 {
			t.Errorf("%q: %s", f, got)
		}
	}
}

// An equation goes into the Word file as Office Math with its words as hidden text, and comes back from it as the
// same formula with the same alternative.
func TestEquationRoundTripsThroughWord(t *testing.T) {
	for _, f := range []string{"x^2+y^2=z^2", "frac{a+b}{2}", "sqrt{x_i}", "sum_{i=1}^n i", "int_0^1 x dx"} {
		doc := docfile.Document{Content: []docfile.Item{
			{Chars: &docfile.Chars{Text: "before\n"}},
			{Equation: &docfile.EquationItem{Formula: f, Alt: "the words for " + f}},
			{Chars: &docfile.Chars{Text: "after\n"}},
		}}
		data, err := DOCX{}.Export(FromDocument(doc))
		if err != nil {
			t.Fatal(err)
		}
		xml := docxParts(t, data)["word/document.xml"]
		if !strings.Contains(xml, "<m:oMathPara><m:oMath>") || !strings.Contains(xml, "<w:vanish/>") || !strings.Contains(xml, "the words for") {
			t.Fatalf("%q: not Office Math with its words: %s", f, xml)
		}
		back, err := docximport.Import(data)
		if err != nil {
			t.Fatal(err)
		}
		var eq *docfile.EquationItem
		for _, it := range back.Content {
			if it.Equation != nil {
				eq = it.Equation
			}
		}
		if eq == nil {
			t.Fatalf("%q: the equation was lost: %+v", f, back.Content)
		}
		if strings.ReplaceAll(eq.Formula, " ", "") != strings.ReplaceAll(f, " ", "") || eq.Alt != "the words for "+f {
			t.Errorf("%q came back as %+v", f, eq)
		}
		// no stray empty line or visible alt text around it
		text := ""
		for _, it := range back.Content {
			if it.Chars != nil {
				text += it.Chars.Text
			}
		}
		if strings.Contains(text, "the words for") || strings.Contains(text, "\n\n") {
			t.Errorf("%q: the text around it is %q", f, text)
		}
	}
}
