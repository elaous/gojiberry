package export

import (
	"bytes"
	"encoding/xml"
	"html"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
)

func wellFormed(t *testing.T, s string) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := d.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("not well formed XML (%v): %s", err, s)
		}
	}
}

func TestMathMLFromTheLinearNotation(t *testing.T) {
	for _, tc := range []struct {
		formula string
		want    []string
	}{
		{"x^2+y^2=z^2", []string{"<msup><mi>x</mi><mn>2</mn></msup>", "<mo>+</mo>", "<mo>=</mo>", "<msup><mi>z</mi><mn>2</mn></msup>"}},
		{"frac{a+b}{2}", []string{"<mfrac><mrow><mi>a</mi><mo>+</mo><mi>b</mi></mrow><mrow><mn>2</mn></mrow></mfrac>"}},
		{"sqrt{x_1^2}", []string{"<msqrt><mrow><msubsup><mi>x</mi><mn>1</mn><mn>2</mn></msubsup></mrow></msqrt>"}},
		{"sin x + 3.5pi", []string{"<mi>sin</mi>", "<mi>x</mi>", "<mn>3.5</mn>", "<mi>π</mi>"}},
		{"sum_{i=1}^{n} i", []string{"<mo>∑</mo>"}},
		{"ab^2", []string{"<mi>a</mi><msup><mi>b</mi><mn>2</mn></msup>"}}, // the script belongs to the last letter
		{"a < b & c > \"d\"", []string{"<mo>&lt;</mo>", "<mo>&amp;</mo>", "<mo>&gt;</mo>", `<mo>"</mo>`}},
	} {
		got := MathML(tc.formula, "alt")
		wellFormed(t, got)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("MathML(%q) lacks %s:\n%s", tc.formula, w, got)
			}
		}
		if !strings.Contains(got, `alttext="alt"`) || !strings.Contains(got, `<annotation encoding="text/plain">alt</annotation>`) {
			t.Errorf("MathML(%q) lacks its text alternative: %s", tc.formula, got)
		}
	}
}

func TestMathMLIsWellFormedWhateverTheFormula(t *testing.T) {
	for _, formula := range []string{"", "{", "}", "{{x", "x}}", "^", "_", "x^", "x^{", "frac", "frac{a}", "sqrt", "a^b^c_d_e", "<script>", "\x00\x01", "é^ü", "{}{}^{}"} {
		wellFormed(t, MathML(formula, `a "quoted" <alt> & more`))
	}
}

func TestEquationHasATextAlternativeInEveryExport(t *testing.T) {
	snap := Snapshot{Title: "Maths", Lang: "en", PageWidth: 600, PageHeight: 800, MarginLeft: 60, MarginTop: 60, MarginRight: 60, MarginBottom: 60,
		Blocks: []Block{
			{Kind: Paragraph, Segments: []Segment{{Text: "Before."}}},
			EquationBlock("x^2+y^2=z^2", "x squared plus y squared equals z squared"),
			EquationBlock("a/b", ""), // no description: the formula is the text
		}}
	html, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `alttext="x squared plus y squared equals z squared"`) || !strings.Contains(string(html), "<msup>") {
		t.Errorf("HTML lacks the MathML and its alternative: %s", html)
	}
	if !strings.Contains(string(html), `alttext="a/b"`) {
		t.Error("an equation with no description falls back to its formula")
	}
	xhtml := renderHTMLDocument(snap, true, "")
	wellFormed(t, xhtml)
	for name, exp := range map[string]Exporter{"text": PlainText{}, "markdown": Markdown{}, "rtf": RTF{}} {
		out, err := exp.Export(snap)
		if err != nil {
			t.Fatal(name, err)
		}
		if !strings.Contains(string(out), "x squared plus y squared equals z squared") || !strings.Contains(string(out), "a/b") {
			t.Errorf("%s lacks the text alternative: %s", name, out)
		}
	}
	docx, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(zipTexts(t, docx), "\n"), "x squared plus y squared equals z squared") {
		t.Error("Word lacks the text alternative")
	}
	pdf, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if txt := plainTextOf(t, pdf); !strings.Contains(txt, "x squared plus y squared") {
		t.Errorf("PDF lacks the text alternative: %q", txt)
	}
}

func zipTexts(t *testing.T, data []byte) []string {
	t.Helper()
	var out []string
	for _, body := range docxParts(t, data) {
		out = append(out, body)
	}
	return out
}

func notedDocument() docfile.Document {
	return docfile.Document{
		Title: "Notes",
		Content: []docfile.Item{
			{Chars: &docfile.Chars{Text: "First claim"}},
			{Footnote: &docfile.FootnoteMark{ID: "a"}},
			{Chars: &docfile.Chars{Text: " and a second claim"}},
			{Footnote: &docfile.FootnoteMark{ID: "b"}},
			{Chars: &docfile.Chars{Text: "."}},
		},
		Footnotes: []docfile.Footnote{{ID: "a", Text: "Source of the first."}, {ID: "b", Text: "Source of the second."}},
	}
}

func TestFootnotesLinkBothWaysInHTML(t *testing.T) {
	snap := FromDocument(notedDocument())
	for name, page := range map[string]string{"html": renderHTMLDocument(snap, false, ""), "xhtml": renderHTMLDocument(snap, true, "")} {
		if name == "xhtml" {
			wellFormed(t, page)
		}
		for _, n := range []string{"1", "2"} {
			if !regexp.MustCompile(`<sup id="fnref-` + n + `"><a href="#fn-` + n + `" aria-label="Footnote ` + n + `">` + n + `</a></sup>`).MatchString(page) {
				t.Errorf("%s: the mark of note %s is not a link to it: %s", name, n, page)
			}
			if !regexp.MustCompile(`<li id="fn-` + n + `">[^<]*<a href="#fnref-` + n + `" aria-label="Back to the text of footnote ` + n + `">`).MatchString(page) {
				t.Errorf("%s: note %s does not link back: %s", name, n, page)
			}
		}
		if strings.Count(page, `id="fnref-`) != 2 || strings.Count(page, `id="fn-`) != 2 {
			t.Errorf("%s: every id is there once", name)
		}
	}
	text, _ := PlainText{}.Export(snap)
	if !strings.Contains(string(text), "Source of the second.") {
		t.Error("the note text is in the plain text")
	}
}

func TestCodeBlockLanguage(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Code, CodeLang: "go", Segments: []Segment{{Text: "fmt.Println(1 < 2)"}}},
		{Kind: Code, CodeLang: "c++", Segments: []Segment{{Text: "int x;"}}},
		{Kind: Code, CodeLang: `go"><script>`, Segments: []Segment{{Text: "evil"}}},
		{Kind: Code, Segments: []Segment{{Text: "plain"}}},
	}}
	html, _ := HTML{}.Export(snap)
	for _, want := range []string{`<pre><code class="language-go">fmt.Println(1 &lt; 2)</code></pre>`, `<code class="language-c++">`, `<pre><code>evil</code></pre>`, `<pre><code>plain</code></pre>`} {
		if !bytes.Contains(html, []byte(want)) {
			t.Errorf("HTML lacks %s: %s", want, html)
		}
	}
	md, _ := Markdown{}.Export(snap)
	if !strings.Contains(string(md), "```go\nfmt.Println(1 < 2)\n```") || !strings.Contains(string(md), "```\nevil\n```") {
		t.Errorf("Markdown fences: %s", md)
	}
}

func TestTypesetterAndMathMLReadTheSameSymbols(t *testing.T) {
	re := regexp.MustCompile(`<(mi|mn|mo)>(.*?)</(?:mi|mn|mo)>`)
	for _, f := range []string{
		"x^2 + y_1", "frac{a}{b}", "sqrt{x+1}", "sum_{i=1}^{n} i", "int_0^1 f(x) dx", "ab^2", "sin x + cos y", "pi r^2",
		"a < b > c", "2.5 * 3 = 7.5", "alpha_beta", "{a+b}^2", "x^", "sqrt", "frac{a", "}} x {{", "(a|b)[c];d:e,f",
		`matrix{a & b \\ c & d}`, `aligned{x &= 1 \\ y &= 2}`, "matrix{1 & 2", "matrix x",
	} {
		var want []string
		for _, m := range re.FindAllStringSubmatch(MathML(f, "alt"), -1) {
			want = append(want, html.UnescapeString(m[2]))
		}
		got := canvas.MathSymbols(f)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%q:\n typesetter %q\n MathML     %q", f, got, want)
		}
	}
}

func TestMatrixAndAlignedEquationsInMathMLAndOfficeMath(t *testing.T) {
	ml := MathML(`matrix{a & b \\ c & d}`, "alt")
	for _, want := range []string{"<mo>(</mo><mtable><mtr><mtd><mrow><mi>a</mi></mrow></mtd><mtd><mrow><mi>b</mi></mrow></mtd></mtr>", "</mtable><mo>)</mo>"} {
		if !strings.Contains(ml, want) {
			t.Errorf("matrix MathML lacks %s:\n%s", want, ml)
		}
	}
	if !strings.Contains(MathML(`aligned{x &= 1 \\ y &= 2}`, ""), `<mtable columnalign="right left"`) {
		t.Error("aligned columns")
	}
	om := OMML(`matrix{a & b \\ c & d}`)
	if !strings.Contains(om, "<m:d><m:e><m:m><m:mr><m:e>") || strings.Count(om, "<m:mr>") != 2 {
		t.Errorf("OMML matrix: %s", om)
	}
	if !strings.Contains(OMML(`aligned{x &= 1 \\ y &= 2}`), "<m:eqArr><m:e>") {
		t.Error("OMML aligned")
	}
	if got := MathML("matrix x", ""); strings.Contains(got, "mtable") {
		t.Errorf("matrix without braces is letters: %s", got)
	}
}

func TestOfficeMathMatricesAndAlignedEquationsReadBack(t *testing.T) {
	for _, formula := range []string{`matrix{a & b \\ c & d}`, `aligned{x &= 1 \\ y &= 2}`, `matrix{1 & 0 \\ 0 & 1}`} {
		doc := docfile.Document{Content: []docfile.Item{{Equation: &docfile.EquationItem{Formula: formula, Alt: "alt"}}}}
		data, err := DOCX{}.Export(FromDocument(doc))
		if err != nil {
			t.Fatal(err)
		}
		back, err := docximport.Import(data)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, it := range back.Content {
			if it.Equation != nil {
				got = it.Equation.Formula
			}
		}
		if got != formula && !strings.HasPrefix(formula, "aligned") { // aligned: spacing round the & may differ
			t.Errorf("%s read back as %q", formula, got)
		}
		if MathML(got, "") != MathML(formula, "") {
			t.Errorf("%s: the MathML of what was read back differs", formula)
		}
	}
}
