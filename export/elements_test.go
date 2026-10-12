package export

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
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
			// The mark links to its note ...
			if !regexp.MustCompile(`<sup id="fnref-` + n + `"><a href="#fn-` + n + `" aria-label="Footnote ` + n + `">` + n + `</a></sup>`).MatchString(page) {
				t.Errorf("%s: the mark of note %s is not a link to it: %s", name, n, page)
			}
			// ... the note has the id the mark points at, and a link back to the mark.
			if !regexp.MustCompile(`<li id="fn-` + n + `">[^<]*<a href="#fnref-` + n + `" aria-label="Back to the text of footnote ` + n + `">`).MatchString(page) {
				t.Errorf("%s: note %s does not link back: %s", name, n, page)
			}
		}
		if strings.Count(page, `id="fnref-`) != 2 || strings.Count(page, `id="fn-`) != 2 {
			t.Errorf("%s: every id is there once", name)
		}
	}
	// The other formats keep the plain numbered notes.
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
