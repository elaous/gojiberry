package export

import (
	"archive/zip"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/htmlimport"
	"github.com/elaous/gojiberry/transfer"
)

func sampleDoc() docfile.Document {
	bold := docfile.Format{FontWeight: 700}
	return docfile.Document{
		Title: "Quarterly report",
		Content: []docfile.Item{
			{Chars: &docfile.Chars{Text: "Intro", Format: docfile.Format{Scale: 2}}},
			{Chars: &docfile.Chars{Text: "\n"}},
			{Chars: &docfile.Chars{Text: "Some "}},
			{Chars: &docfile.Chars{Text: "bold", Format: bold}},
			{Chars: &docfile.Chars{Text: " and "}},
			{Chars: &docfile.Chars{Text: "linked", Format: docfile.Format{Link: "https://example.com"}}},
			{Footnote: &docfile.FootnoteMark{ID: "n1"}},
			{Chars: &docfile.Chars{Text: "\n"}},
			{Para: &docfile.Para{List: docfile.List{Kind: 2}}},
			{Chars: &docfile.Chars{Text: "first\n"}},
			{Para: &docfile.Para{List: docfile.List{Kind: 2}}},
			{Chars: &docfile.Chars{Text: "second\n"}},
			{Para: &docfile.Para{List: docfile.List{Kind: 1}}},
			{Chars: &docfile.Chars{Text: "a bullet\n"}},
			{Table: &docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"a", "b"}}, {Cells: []string{"c", "d"}}}}},
			{Chars: &docfile.Chars{Text: "after the table"}},
		},
		Footnotes: []docfile.Footnote{{ID: "n1", Text: "A note."}},
	}
}

func TestFromDocumentBuildsBlocks(t *testing.T) {
	snap := FromDocument(sampleDoc())
	var kinds []BlockKind
	for _, b := range snap.Blocks {
		kinds = append(kinds, b.Kind)
	}
	want := []BlockKind{Heading1, Paragraph, NumberedItem, NumberedItem, BulletItem, TableCell, TableCell, TableCell, TableCell, Paragraph, Heading2, NumberedItem}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("block %d: %v, want %v (all: %v)", i, kinds[i], want[i], kinds)
		}
	}
	if snap.Blocks[3].NumberedIndex != 2 {
		t.Errorf("second item numbered %d", snap.Blocks[3].NumberedIndex)
	}
	para := snap.Blocks[1]
	if !para.Segments[1].Format.Bold || para.Segments[3].Format.Href != "https://example.com" {
		t.Errorf("segments %+v", para.Segments)
	}
}

// Every format turns the same document into something that holds its text.
func TestEveryFormatHoldsTheText(t *testing.T) {
	snap := FromDocument(sampleDoc())
	for name, e := range map[string]Exporter{"html": HTML{}, "rtf": RTF{}, "epub": EPUB{}, "pdf": PDF{}, "docx": DOCX{}} {
		b, err := e.Export(snap)
		if err != nil || len(b) < 100 {
			t.Errorf("%s: %d bytes, %v", name, len(b), err)
		}
		switch name {
		case "html":
			if !strings.Contains(string(b), "<h1>") || !strings.Contains(string(b), "<strong>bold</strong>") || !strings.Contains(string(b), `href="https://example.com"`) {
				t.Errorf("html:\n%s", b)
			}
		case "rtf":
			if !strings.HasPrefix(string(b), "{\\rtf1") || !strings.Contains(string(b), "bold") {
				t.Errorf("rtf: %.200s", b)
			}
		case "pdf":
			if !strings.HasPrefix(string(b), "%PDF-") {
				t.Errorf("pdf header %.10s", b)
			}
		case "epub", "docx":
			if !strings.HasPrefix(string(b), "PK") {
				t.Errorf("%s is not a zip", name)
			}
		}
	}
}

func TestRegisterAddsTheFormats(t *testing.T) {
	r := transfer.Default()
	Register(r)
	for _, name := range []string{"html", "rtf", "epub", "pdf", "docx"} {
		file, data, mime, err := r.Export(name, sampleDoc())
		if err != nil || len(data) == 0 || !strings.HasPrefix(file, "Quarterly report.") || mime == "" {
			t.Errorf("%s: %q %d %q %v", name, file, len(data), mime, err)
		}
	}
}

func TestRegisterAddsTheImporters(t *testing.T) {
	r := transfer.Default()
	Register(r)
	_, data, _, err := r.Export("docx", sampleDoc())
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.Import("", "report.docx", data)
	if err != nil || d.Title != "Quarterly report" {
		t.Fatalf("docx import: %+v %v", d.Title, err)
	}
	_, pdf, _, _ := r.Export("pdf", sampleDoc())
	d, err = r.Import("", "report.PDF", pdf)
	if err != nil || len(d.Content) == 0 {
		t.Fatalf("pdf import: %d items, %v", len(d.Content), err)
	}
	if !strings.Contains(r.Accept(), ".docx") || !strings.Contains(r.Accept(), ".pdf") {
		t.Errorf("accept %q", r.Accept())
	}
}

func TestMergedCellsReachTheFormats(t *testing.T) {
	d := docfile.Document{Content: []docfile.Item{{Table: &docfile.TableItem{
		Rows:   []docfile.TableRow{{Cells: []string{"wide", "", "c"}}, {Cells: []string{"tall", "e", "f"}}, {Cells: []string{"", "h", "i"}}},
		Merges: []docfile.TableMerge{{Row: 0, Col: 0, Rows: 1, Cols: 2}, {Row: 1, Col: 0, Rows: 2, Cols: 1}},
		Styles: []docfile.TableCellStyle{{Row: 0, Col: 0, Bold: true}},
	}}}}
	snap := FromDocument(d)
	var spans []string
	for _, b := range snap.Blocks {
		if b.Kind == TableCell && (b.TableColSpan > 1 || b.TableRowSpan > 1 || b.TableCovered) {
			spans = append(spans, strings.TrimSpace(strings.Join([]string{itoa(b.TableRow), itoa(b.TableCol), itoa(b.TableRowSpan) + "x" + itoa(b.TableColSpan)}, ",")))
		}
	}
	if got := strings.Join(spans, " "); got != "0,0,1x2 0,1,1x1 1,0,2x1 2,0,1x1" {
		t.Errorf("spans %q", got)
	}
	if !snap.Blocks[0].Segments[0].Format.Bold {
		t.Error("a bold cell is bold in the snapshot")
	}
	html, err := HTML{}.Export(snap)
	if err != nil || !strings.Contains(string(html), `colspan="2"`) || !strings.Contains(string(html), `rowspan="2"`) {
		t.Errorf("html %v:\n%s", err, html)
	}
	rtf, err := RTF{}.Export(snap)
	if err != nil || !strings.Contains(string(rtf), `\clmgf`) || !strings.Contains(string(rtf), `\clvmgf`) {
		t.Errorf("rtf %v:\n%s", err, rtf)
	}
	if _, err := (PDF{}).Export(snap); err != nil {
		t.Error(err)
	}
	if _, err := (EPUB{}).Export(snap); err != nil {
		t.Error(err)
	}
}

func TestDocxKeepsCellsThatAreNotAWholeTable(t *testing.T) {
	snap := Snapshot{Blocks: []Block{{Kind: TableCell, TableRow: 0, TableCol: 0, TableRows: 2, TableCols: 2, TableColSpan: 1, TableRowSpan: 1, Segments: []Segment{{Text: "lonely"}}}}}
	data, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unzipEntry(t, data, "word/document.xml")), "lonely") {
		t.Error("the cell's text was dropped")
	}
}

func unzipEntry(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			defer func() { _ = rc.Close() }()
			b, _ := io.ReadAll(rc)
			return b
		}
	}
	t.Fatalf("no %s in the file", name)
	return nil
}

// A run with a language is written in HTML as a span with a lang attribute.
func TestHTMLWritesTheLanguageOfARun(t *testing.T) {
	got := wrapHTMLRun("monde", Format{Lang: `fr"x`})
	if got != `<span lang="fr&#34;x">monde</span>` && got != `<span lang="fr&quot;x">monde</span>` {
		t.Errorf("got %s", got)
	}
	if got := wrapHTMLRun("monde", Format{Lang: "fr"}); got != `<span lang="fr">monde</span>` {
		t.Errorf("got %s", got)
	}
}

// An image's description is its alt text in HTML and Markdown; one marked decorative has an empty alt.
func TestImageAltTextIsExported(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Image: &docfile.ImageItem{URL: "a.png", Alt: `A "graph" of sales`}},
		{Image: &docfile.ImageItem{URL: "b.png", Decorative: true}},
		{Image: &docfile.ImageItem{URL: "c.png"}},
	}}
	snap := FromDocument(doc)
	html, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`src="a.png" alt="A &#34;graph&#34; of sales"`, `src="b.png" alt=""`, `src="c.png" alt=""`} {
		if !strings.Contains(string(html), want) && !strings.Contains(strings.ReplaceAll(string(html), "&quot;", "&#34;"), want) {
			t.Errorf("html lacks %s:\n%s", want, html)
		}
	}
	md, _ := Markdown{}.Export(snap)
	if !strings.Contains(string(md), "![A \"graph\" of sales](a.png)") && !strings.Contains(string(md), `![A "graph" of sales]`) {
		t.Errorf("markdown:\n%s", md)
	}
	txt, _ := PlainText{}.Export(snap)
	if !strings.Contains(string(txt), `[Image: A "graph" of sales]`) {
		t.Errorf("text:\n%s", txt)
	}
	if !snap.Blocks[1].ImageDecorative || snap.Blocks[0].ImageAlt == "" {
		t.Errorf("snapshot: %+v", snap.Blocks)
	}
}

// The document's language is written where the format can say so: <html lang>, the EPUB's dc:language and
// its pages, Word's default run language and core properties; and the title is in the PDF's information.
func TestExportsCarryTheDocumentLanguageAndTitle(t *testing.T) {
	doc := docfile.Document{Title: "Rapport"}
	doc.Content = []docfile.Item{{Chars: &docfile.Chars{Text: "Bonjour"}}}
	doc.SetLanguage("fr-CA")
	if doc.Language() != "fr-CA" {
		t.Fatalf("language %q", doc.Language())
	}
	snap := FromDocument(doc)
	if snap.Lang != "fr-CA" {
		t.Fatalf("snapshot language %q", snap.Lang)
	}
	html, _ := HTML{}.Export(snap)
	if !strings.Contains(string(html), `<html lang="fr-CA">`) {
		t.Errorf("html:\n%s", html)
	}
	epub, err := EPUB{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	part := func(data []byte, name string) string { return string(unzipEntry(t, data, name)) }
	if !strings.Contains(part(epub, "OEBPS/content.opf"), "<dc:language>fr-CA</dc:language>") || !strings.Contains(part(epub, "OEBPS/content.xhtml"), `lang="fr-CA" xml:lang="fr-CA"`) || !strings.Contains(part(epub, "OEBPS/nav.xhtml"), `xml:lang="fr-CA"`) {
		t.Errorf("epub lacks the language: opf %v content %v nav %v", strings.Contains(part(epub, "OEBPS/content.opf"), "fr-CA"), strings.Contains(part(epub, "OEBPS/content.xhtml"), "fr-CA"), strings.Contains(part(epub, "OEBPS/nav.xhtml"), "fr-CA"))
	}
	docx, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(part(docx, "word/styles.xml"), `<w:lang w:val="fr-CA"/>`) || !strings.Contains(part(docx, "docProps/core.xml"), "<dc:language>fr-CA</dc:language>") {
		t.Errorf("docx styles/core lack the language")
	}
	pdf, err := PDF{}.Export(snap)
	if err != nil || !strings.Contains(string(pdf), "/Title <FEFF0052006100700070006F00720074>") { // "Rapport" in UTF-16
		t.Errorf("the PDF information has no title: %v", err)
	}
	// none set: as before
	plain := FromDocument(docfile.Document{Title: "T"})
	if h, _ := (HTML{}).Export(plain); strings.Contains(string(h), "lang=") {
		t.Errorf("a document with no language gained one:\n%s", h)
	}
	if e, _ := (EPUB{}).Export(plain); !strings.Contains(part(e, "OEBPS/content.opf"), "<dc:language>en</dc:language>") {
		t.Error("the EPUB default language changed")
	}
	// an invalid tag is not kept
	doc.SetLanguage("not a tag")
	if doc.Language() != "fr-CA" {
		t.Errorf("an invalid tag replaced the language: %q", doc.Language())
	}
	doc.SetLanguage("")
	if doc.Language() != "" {
		t.Errorf("not cleared: %q", doc.Language())
	}
}

// A formula in the middle of a sentence is inline MathML in the HTML export and the words that stand for it
// everywhere else.
func TestInlineFormulaIsInlineMathMLInHTML(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "Area is "}},
		{Equation: &docfile.EquationItem{Formula: "pi r^2", Alt: "pi r squared", Inline: true}},
		{Chars: &docfile.Chars{Text: " here."}},
	}}
	snap := FromDocument(doc)
	html, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `display="inline"`) || !strings.Contains(string(html), "<p>Area is <math") {
		t.Errorf("html:\n%s", html)
	}
	txt, _ := PlainText{}.Export(snap)
	if !strings.Contains(string(txt), "Area is pi r squared here.") {
		t.Errorf("text:\n%s", txt)
	}
}

// A form field is an HTML control in the HTML export, with its label, its value and its checks, and the words
// "label: value" in the formats that have no controls.
func TestFormFieldsAreHTMLControlsAndWordsElsewhere(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "Order "}},
		{Form: &docfile.FormFieldItem{Kind: "text", Name: "order", Label: `Order "no."`, Placeholder: "AB123", Pattern: `[A-Z]{2}\d{3}`, Required: true, Value: `AB123<b>`}},
		{Chars: &docfile.Chars{Text: " "}},
		{Form: &docfile.FormFieldItem{Kind: "checkbox", Name: "ok", Label: "I agree", Value: "true"}},
		{Chars: &docfile.Chars{Text: " "}},
		{Form: &docfile.FormFieldItem{Kind: "select", Name: "size", Label: "Size", Options: []string{"S", "M<"}, Value: "M<"}},
		{Chars: &docfile.Chars{Text: " "}},
		{Form: &docfile.FormFieldItem{Kind: "textarea", Label: "Note", Value: "two\nlines"}},
	}}
	snap := FromDocument(doc)
	html, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(html)
	for _, want := range []string{
		`<label>Order "no." <input type="text" name="order" required placeholder="AB123" pattern="[A-Z]{2}\d{3}" value="AB123&lt;b&gt;"></label>`,
		`<label><input type="checkbox" name="ok" checked> I agree</label>`,
		`<option value="M&lt;" selected>M&lt;</option>`,
		`<textarea>two`,
	} {
		if !strings.Contains(strings.ReplaceAll(s, "&quot;", "&#34;"), want) {
			t.Errorf("html lacks %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, "<b>") {
		t.Error("a value is escaped, never markup")
	}
	txt, _ := PlainText{}.Export(snap)
	if !strings.Contains(string(txt), `Order "no.": AB123<b> [x] I agree`) {
		t.Errorf("text:\n%s", txt)
	}
}

// With an address for the answers the form fields sit in a form with a send button; only https and mailto addresses
// are accepted, and a document without form fields gets no form.
func TestHTMLSubmitToWrapsFormFieldsInAForm(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Form: &docfile.FormFieldItem{Kind: "text", Name: "a", Label: "A"}},
	}}
	snap := FromDocument(doc)
	out, err := HTML{SubmitTo: "https://example.org/in?x=1&y=2", SubmitLabel: "Go <now>"}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`<form action="https://example.org/in?x=1&amp;y=2" method="post">`, `<button type="submit">Go &lt;now&gt;</button>`, "</form>"} {
		if !strings.Contains(s, want) {
			t.Errorf("html lacks %s:\n%s", want, s)
		}
	}
	out, _ = HTML{SubmitTo: "mailto:a@example.org"}.Export(snap)
	if !strings.Contains(string(out), `enctype="text/plain"`) || !strings.Contains(string(out), ">Send</button>") {
		t.Errorf("mailto form:\n%s", out)
	}
	for _, bad := range []string{"http://example.org", "javascript:alert(1)", `https://x"onfocus="y`, "https://a b", "mailto:"} {
		if _, err := (HTML{SubmitTo: bad}).Export(snap); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	plain, _ := HTML{SubmitTo: "https://example.org/in"}.Export(FromDocument(docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "Hello"}}}}))
	if strings.Contains(string(plain), "<form") {
		t.Error("no form without form fields")
	}
	if strings.Contains(s, "<form") == false || strings.Contains(string(mustHTML(t, snap)), "<form") {
		t.Error("the form appears only when an address is set")
	}
}

func mustHTML(t *testing.T, snap Snapshot) []byte {
	t.Helper()
	b, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A form page written by the HTML export reads back as the same fields, with what was filled in.
func TestHTMLFormFieldsReadBackFromTheirPage(t *testing.T) {
	want := []docfile.FormFieldItem{
		{Kind: "text", Name: "order", Label: "Order no.", Placeholder: "AB123", Pattern: `[A-Z]{2}\d{3}`, Required: true, Value: `AB123<b>`},
		{Kind: "email", Name: "mail", Label: "Mail", Value: "a@example.org"},
		{Kind: "checkbox", Name: "ok", Label: "I agree", Value: "true"},
		{Kind: "select", Name: "size", Label: "Size", Options: []string{"S", "M<"}, Value: "M<"},
		{Kind: "textarea", Name: "note", Label: "Note", Value: "two\nlines"},
	}
	var items []docfile.Item
	for i := range want {
		items = append(items, docfile.Item{Form: &want[i]}, docfile.Item{Chars: &docfile.Chars{Text: " "}})
	}
	page, err := HTML{SubmitTo: "https://example.org/in"}.Export(FromDocument(docfile.Document{Content: items}))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := htmlimport.Import(page)
	if err != nil {
		t.Fatal(err)
	}
	var got []docfile.FormFieldItem
	for _, it := range doc.Content {
		if it.Form != nil {
			got = append(got, *it.Form)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back:\n got %+v\nwant %+v", got, want)
	}
	if _, s, _ := htmlimport.ImportReport(page); s.Forms != 1 { // the send button
		t.Errorf("skipped %+v", s)
	}
}

// A formula in a sentence is Office Math in the line in Word, with its words as hidden text, and reads back as an
// equation in the line (not on a line of its own) with those words as its alternative.
func TestInlineFormulaIsOfficeMathInTheLineInWord(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "The area is "}},
		{Equation: &docfile.EquationItem{Formula: "pi r^2", Alt: "pi r squared", Inline: true}},
		{Chars: &docfile.Chars{Text: " here."}},
	}}
	data, err := DOCX{}.Export(FromDocument(doc))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var xml string
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			xml = string(b)
		}
	}
	if !strings.Contains(xml, "<m:oMath>") || strings.Contains(xml, "<m:oMathPara>") || !strings.Contains(xml, "<w:vanish/>") {
		t.Errorf("document.xml:\n%s", xml)
	}
	back, err := docximport.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	var eq *docfile.EquationItem
	var text strings.Builder
	for _, it := range back.Content {
		if it.Equation != nil {
			eq = it.Equation
		}
		if it.Chars != nil {
			text.WriteString(it.Chars.Text)
		}
	}
	if eq == nil || !eq.Inline || eq.Alt != "pi r squared" || strings.Contains(text.String(), "\n") || !strings.Contains(text.String(), "The area is") {
		t.Errorf("read back: %+v, text %q", eq, text.String())
	}
}
