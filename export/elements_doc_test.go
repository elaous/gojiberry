package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func elementsDoc() docfile.Document {
	return docOf(
		plain("Before\n"),
		docfile.Item{Equation: &docfile.EquationItem{Formula: "x^2 + y^2 = z^2", Alt: "x squared plus y squared equals z squared"}},
		paraItem(docfile.Para{Style: "Code", CodeLang: "Go"}), plain("x := 1\n"),
		paraItem(docfile.Para{Style: "Code", CodeLang: "Go"}), plain("y := 2\n"),
		paraItem(docfile.Para{}), plain("Between\n"),
		docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40, Alt: "Flow chart", LongDesc: "Three boxes: input, process and output."}},
		plain("After\n"),
	)
}

func TestSavedEquationIsMathMLInHTMLAndWordsEverywhereElse(t *testing.T) {
	r := registry()
	d := elementsDoc()
	for _, format := range []string{"html", "epub"} {
		out := string(exportOne(t, r, format, d))
		if !strings.Contains(out, "<math") || !strings.Contains(out, `alttext="x squared plus y squared equals z squared"`) {
			t.Errorf("%s: no MathML with its alternative:\n%s", format, out)
		}
	}
	for _, format := range []string{"docx", "rtf", "markdown", "text", "pdf"} {
		out := string(exportOne(t, r, format, d))
		if format == "pdf" { // the PDF text is not plain bytes: the words are found by reading it back
			out = textOf(importBack(t, r, "pdf", mustExport(t, r, format, d)))
		}
		if !strings.Contains(out, "x squared plus y squared equals z squared") {
			t.Errorf("%s: the text alternative of the equation is not in the file", format)
		}
	}
	bare := docOf(plain("a\n"), docfile.Item{Equation: &docfile.EquationItem{Formula: "x^2"}}, plain("b\n"))
	for _, format := range []string{"docx", "markdown", "text", "html"} {
		if !strings.Contains(strings.ReplaceAll(string(exportOne(t, r, format, bare)), `\`, ""), "x^2") { // Markdown escapes the caret
			t.Errorf("%s: an equation with no alternative is shown as nothing", format)
		}
	}
}

func mustExport(t *testing.T, r interface {
	Export(string, docfile.Document) (string, []byte, string, error)
}, format string, d docfile.Document) []byte {
	t.Helper()
	_, data, _, err := r.Export(format, d)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func textOf(d docfile.Document) string {
	var sb strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			sb.WriteString(it.Chars.Text)
		}
	}
	return sb.String()
}

func TestCodeParagraphsAreOneBlockWithTheirLanguage(t *testing.T) {
	snap := FromDocument(elementsDoc())
	var code []Block
	for _, b := range snap.Blocks {
		if b.Kind == Code {
			code = append(code, b)
		}
	}
	if len(code) != 1 || code[0].CodeLang != "Go" || rawCodeText(code[0].Segments) != "x := 1\ny := 2" {
		t.Fatalf("code blocks = %+v", code)
	}
	out, err := HTML{}.Export(snap)
	if err != nil || !strings.Contains(string(out), `<pre><code class="language-go">x := 1`+"\n"+`y := 2</code></pre>`) {
		t.Errorf("HTML: %v\n%s", err, out)
	}
	md, _ := Markdown{}.Export(snap)
	if !strings.Contains(string(md), "```go\nx := 1\ny := 2\n```") {
		t.Errorf("Markdown fence:\n%s", md)
	}
	d := docOf(paraItem(docfile.Para{Style: "Code", CodeLang: `"><script>`}), plain("x\n"))
	out, _ = HTML{}.Export(FromDocument(d))
	if strings.Contains(string(out), "script") || !strings.Contains(string(out), "<pre><code>x</code></pre>") {
		t.Errorf("unsafe language: %s", out)
	}
}

func TestDiagramLongDescriptionFollowsThePictureAndIsLinkedInHTML(t *testing.T) {
	r := registry()
	d := elementsDoc()
	html := string(exportOne(t, r, "html", d))
	i := strings.Index(html, `aria-describedby="longdesc-`)
	if i < 0 {
		t.Fatalf("the picture does not point to its long description:\n%s", html)
	}
	id := html[i+len(`aria-describedby="`) : i+strings.Index(html[i+len(`aria-describedby="`):], `"`)+len(`aria-describedby="`)]
	if !strings.Contains(html, `<p class="longdesc" id="`+id+`">Three boxes: input, process and output.</p>`) {
		t.Errorf("no paragraph with the id %s holds the description:\n%s", id, html)
	}
	if strings.Index(html, "<img") > strings.Index(html, "Three boxes") {
		t.Error("the description comes before the picture")
	}
	for _, format := range []string{"docx", "rtf", "epub"} {
		if !strings.Contains(string(exportOne(t, r, format, d)), "Three boxes: input, process and output.") {
			t.Errorf("%s: the long description is not in the file", format)
		}
	}
	d.Content[len(d.Content)-2].Image.LongDesc = ""
	if out := string(exportOne(t, r, "html", d)); strings.Contains(out, "describedby") || strings.Contains(out, "longdesc") {
		t.Errorf("a picture with no long description points to one:\n%s", out)
	}
}

func notesDoc() docfile.Document {
	d := docOf(plain("see"), docfile.Item{Footnote: &docfile.FootnoteMark{ID: "n1"}}, plain(" here and"), docfile.Item{Footnote: &docfile.FootnoteMark{ID: "n2"}}, plain(" there\n"))
	d.Footnotes = []docfile.Footnote{{ID: "n1", Text: "The first note."}, {ID: "n2", Text: "The second note."}}
	return d
}

func TestFootnotesLinkBothWaysInWordAndMarkdown(t *testing.T) {
	r := registry()
	word := string(exportOne(t, r, "docx", notesDoc()))
	for _, want := range []string{
		`w:name="_fnref1"`, `w:anchor="_fn1"`, // the mark is a link to the note
		`w:name="_fn1"`, `w:anchor="_fnref1"`, // the note has a link back
		`w:name="_fnref2"`, `w:anchor="_fn2"`, `w:name="_fn2"`, `w:anchor="_fnref2"`,
	} {
		if !strings.Contains(word, want) {
			t.Errorf("Word: %s is missing", want)
		}
	}
	ids := map[string]bool{}
	for _, part := range strings.Split(word, "<w:bookmarkStart ")[1:] {
		id := part[strings.Index(part, `w:id="`)+6:]
		id = id[:strings.Index(id, `"`)]
		if ids[id] {
			t.Errorf("bookmark id %s is used twice", id)
		}
		ids[id] = true
	}
	md, err := Markdown{}.Export(FromDocument(notesDoc()))
	if err != nil {
		t.Fatal(err)
	}
	want := "see[^1] here and[^2] there\n\n[^1]: The first note.\n\n[^2]: The second note.\n"
	if string(md) != want {
		t.Errorf("Markdown = %q, want %q", md, want)
	}
}

func TestFootnotesLinkBothWaysInRTF(t *testing.T) {
	rtf := string(exportOne(t, registry(), "rtf", notesDoc()))
	for _, want := range []string{
		`{\*\bkmkstart _fnref1}`, `HYPERLINK \\l _fn1`, // the mark is a link to the note
		`{\*\bkmkstart _fn1}`, `HYPERLINK \\l _fnref1`, // the note has a link back
		`{\*\bkmkstart _fnref2}`, `HYPERLINK \\l _fn2`, `{\*\bkmkstart _fn2}`, `HYPERLINK \\l _fnref2`,
	} {
		if !strings.Contains(rtf, want) {
			t.Errorf("RTF: %s is missing:\n%s", want, rtf)
		}
	}
	for _, part := range strings.Split(rtf, `HYPERLINK \\l `)[1:] {
		name := part[:strings.Index(part, "}")]
		if !strings.Contains(rtf, `{\*\bkmkstart `+name+`}`) {
			t.Errorf("a link to %q, which is not a bookmark", name)
		}
	}
}
