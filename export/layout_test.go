package export

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/pdftext"
	"github.com/elaous/gojiberry/pdfua"
)

func longSnapshot(headings int) Snapshot {
	var blocks []Block
	for i := 1; i <= headings; i++ {
		blocks = append(blocks, Block{Kind: Heading1, Segments: []Segment{{Text: fmt.Sprintf("Chapter %d", i)}}})
		blocks = append(blocks, Block{Kind: Heading2, Segments: []Segment{{Text: fmt.Sprintf("Section %d.1", i)}}})
		for p := 0; p < 12; p++ {
			blocks = append(blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: strings.Repeat("words of the paragraph ", 12)}}})
		}
	}
	return Snapshot{Title: "Annual report", Lang: "en", Blocks: blocks, PageWidth: 600, PageHeight: 800, MarginLeft: 60, MarginTop: 70, MarginRight: 60, MarginBottom: 70}
}

func TestFillFieldsAndParts(t *testing.T) {
	if got := FillFields("{title}: page {page} of {pages} {unknown}", 3, 12, "Report"); got != "Report: page 3 of 12 {unknown}" {
		t.Errorf("FillFields = %q", got)
	}
	for in, want := range map[string][3]string{
		"centred":     {"", "centred", ""},
		"left\tright": {"left", "", "right"},
		"a\tb\tc":     {"a", "b", "c"},
		"a\tb\tc\td":  {"a", "b", "c d"},
		"":            {"", "", ""},
	} {
		l, c, r := decorParts(in)
		if [3]string{l, c, r} != want {
			t.Errorf("decorParts(%q) = %q %q %q, want %v", in, l, c, r, want)
		}
	}
}

func TestOutlineListsHeadingsInOrder(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Paragraph, Segments: []Segment{{Text: "no"}}},
		{Kind: Heading1, Segments: []Segment{{Text: "One"}}},
		{Kind: Heading3, Segments: []Segment{{Text: "Deep "}, {SoftBreak: true}, {Text: "one"}, {Text: " gone", Format: Format{Change: Deleted}}}},
		{Kind: Heading2, Segments: []Segment{{Text: "  "}}},
		{Kind: Heading2, Segments: []Segment{{Text: "Two"}}},
	}}
	want := []OutlineEntry{{1, "One", 1}, {3, "Deep one", 2}, {2, "Two", 4}}
	got := snap.Outline()
	if len(got) != len(want) {
		t.Fatalf("outline = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestFromDocumentCarriesThePrintLayout(t *testing.T) {
	var d docfile.Document
	d.Title = "T"
	d.SetPageLayout(docfile.PageLayout{Header: "{title}", Footer: "{page}", TOC: true, TOCTitle: "Inhalt", Width: 595, Height: 842, Margins: [4]float64{72, 36, 72, 36}})
	snap := FromDocument(d)
	if snap.Header != "{title}" || snap.Footer != "{page}" || !snap.TOC || snap.TOCTitle != "Inhalt" {
		t.Errorf("snapshot = %+v", snap)
	}
	if snap.PageWidth != 595/0.75 || snap.PageHeight != 842/0.75 || snap.MarginTop != 96 || snap.MarginLeft != 48 {
		t.Errorf("the sheet and margins are in CSS pixels: %v x %v, margins %v %v", snap.PageWidth, snap.PageHeight, snap.MarginTop, snap.MarginLeft)
	}
	plain := FromDocument(docfile.Document{Title: "T"})
	if plain.Header != "" || plain.TOC || plain.PageWidth != defaultPageWidth || plain.MarginTop != defaultMargin {
		t.Errorf("a document with no layout keeps the default sheet: %+v", plain)
	}
}

func TestPDFHeadersFootersAndPageNumbersOnEveryPage(t *testing.T) {
	snap := longSnapshot(3)
	snap.Header = "{title}\t\tdraft"
	snap.Footer = "Page {page} of {pages}"
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	n := len(pages)
	if n < 3 {
		t.Fatalf("the document should run to several pages, got %d", n)
	}
	for i, txt := range pages {
		if want := fmt.Sprintf("Page %d of %d", i+1, n); !strings.Contains(txt, want) {
			t.Errorf("page %d lacks %q: %q", i+1, want, txt)
		}
		if !strings.Contains(txt, "Annual report") || !strings.Contains(txt, "draft") {
			t.Errorf("page %d lacks the header: %q", i+1, txt)
		}
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
	// With neither set nothing is added.
	snap.Header, snap.Footer = "", ""
	plain, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if txt := plainTextOf(t, plain); strings.Contains(txt, "Page 1 of") || strings.Contains(txt, "draft") {
		t.Error("no header or footer was asked for")
	}
}

func TestPDFTableOfContentsHasTheRealPageNumbers(t *testing.T) {
	snap := longSnapshot(4)
	snap.TOC = true
	snap.TOCTitle = "Table of contents"
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pages[0], "Table of contents") {
		t.Fatalf("the contents come first: %q", pages[0])
	}
	where := map[string]int{}
	for _, e := range snap.Outline() {
		for i := 1; i < len(pages); i++ {
			if strings.Contains(pages[i], e.Text) {
				where[e.Text] = i + 1
				break
			}
		}
		if where[e.Text] == 0 {
			t.Fatalf("heading %q is not in the body", e.Text)
		}
	}
	if where["Chapter 4"] <= where["Chapter 1"] || where["Chapter 1"] < 2 {
		t.Fatalf("the headings should be on increasing pages after the contents: %v", where)
	}
	for _, e := range snap.Outline() {
		re := regexp.MustCompile(regexp.QuoteMeta(e.Text) + `[\s.]*?(\d+)`)
		m := re.FindStringSubmatch(pages[0])
		if m == nil {
			t.Errorf("no contents line for %q in %q", e.Text, pages[0])
			continue
		}
		if got, _ := strconv.Atoi(m[1]); got != where[e.Text] {
			t.Errorf("%q: the contents say page %d, the heading is on page %d", e.Text, got, where[e.Text])
		}
	}
	if got, err := pdfua.Check(data); err != nil || len(got) != 0 {
		t.Fatalf("pdfua: %v, %v", got, err)
	}
}

func TestPDFTableOfContentsThatRunsToSeveralPages(t *testing.T) {
	var blocks []Block
	for i := 1; i <= 70; i++ {
		blocks = append(blocks, Block{Kind: Heading1, Segments: []Segment{{Text: fmt.Sprintf("Topic %d", i)}}})
		blocks = append(blocks, Block{Kind: Paragraph, Segments: []Segment{{Text: "text"}}})
	}
	snap := Snapshot{Title: "Long", Blocks: blocks, PageWidth: 600, PageHeight: 500, MarginLeft: 60, MarginTop: 60, MarginRight: 60, MarginBottom: 60, TOC: true}
	data, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := pdftext.ExtractPagesBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	first := 0
	for p, txt := range pages {
		if first == 0 && strings.Contains(txt, "Topic 1text") {
			first = p + 1
		}
	}
	if first < 3 {
		t.Fatalf("the contents should take several pages, the first topic is on page %d of %d", first, len(pages))
	}
	contents := strings.Join(pages[:first-1], "")
	for i := 1; i <= 70; i++ {
		page := 0
		for p, txt := range pages {
			if strings.Contains(txt, fmt.Sprintf("Topic %dtext", i)) {
				page = p + 1
			}
		}
		m := regexp.MustCompile(fmt.Sprintf(`Topic %d(?:\. )+\s*(\d+)`, i)).FindStringSubmatch(contents)
		if page == 0 || m == nil || m[1] != strconv.Itoa(page) {
			t.Fatalf("Topic %d is on page %d, the contents say %v", i, page, m)
		}
	}
}

func TestHTMLPrintStylesheetAndContents(t *testing.T) {
	snap := longSnapshot(2)
	snap.Header = `{title}` + "\t" + `a "quoted" </style><script>x</script>`
	snap.Footer = "Page {page} of {pages}"
	snap.TOC = true
	snap.TOCTitle = "Inhalt"
	out, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		"@page{size:600px 800px;margin:70px 60px 70px 60px;",
		`@bottom-center{content:"Page " counter(page) " of " counter(pages)`,
		`@top-left{content:"Annual report"`,
		"break-after:avoid", "orphans:3", `<nav class="toc" aria-label="Inhalt">`, "<h2>Inhalt</h2>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	if strings.Count(s, "</style>") != 1 || strings.Contains(s, "<script>") {
		t.Errorf("the header text got out of the style element:\n%s", s)
	}
	if !strings.Contains(s, `\"quoted\"`) {
		t.Error("quotes in the header are escaped in the CSS string")
	}
	links := regexp.MustCompile(`<a href="#(toc-\d+)">([^<]*)</a>`).FindAllStringSubmatch(s, -1)
	if len(links) != 4 {
		t.Fatalf("contents links = %v", links)
	}
	for _, l := range links {
		if !regexp.MustCompile(`<h[123] id="` + l[1] + `">` + regexp.QuoteMeta(l[2])).MatchString(s) {
			t.Errorf("link %v has no heading", l)
		}
	}
	snap.TOC = false
	plain, _ := HTML{}.Export(snap)
	if strings.Contains(string(plain), "<nav") || strings.Contains(string(plain), ` id="toc-`) {
		t.Error("no contents was asked for")
	}
	snap.TOC = true
	epub := renderHTMLDocument(snap, true, "")
	if strings.Contains(epub, "<nav") || strings.Contains(epub, "@page") {
		t.Error("the XHTML of an EPUB has no print layout")
	}
}

func docxParts(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		parts[f.Name] = string(b)
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			d := xml.NewDecoder(bytes.NewReader(b))
			for {
				if _, err := d.Token(); err != nil {
					if err != io.EOF {
						t.Errorf("%s is not well-formed XML: %v", f.Name, err)
					}
					break
				}
			}
		}
	}
	return parts
}

func TestDOCXHeaderFooterAndContents(t *testing.T) {
	snap := longSnapshot(2)
	snap.Header = "{title}\t\tConfidential & private"
	snap.Footer = "Page {page} of {pages}"
	snap.TOC = true
	data, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	p := docxParts(t, data)
	hdr, ftr := p["word/header1.xml"], p["word/footer1.xml"]
	if !strings.Contains(hdr, "Annual report") || !strings.Contains(hdr, "Confidential &amp; private") || strings.Contains(hdr, "PAGE") {
		t.Errorf("header = %s", hdr)
	}
	if !strings.Contains(ftr, "> PAGE <") || !strings.Contains(ftr, "> NUMPAGES <") || !strings.Contains(ftr, "Page ") {
		t.Errorf("footer = %s", ftr)
	}
	doc := p["word/document.xml"]
	rels := p["word/_rels/document.xml.rels"]
	for _, kind := range []string{"header", "footer"} {
		m := regexp.MustCompile(`<w:` + kind + `Reference w:type="default" r:id="(rId\d+)"/>`).FindStringSubmatch(doc)
		if m == nil {
			t.Fatalf("the section has no %s reference: %s", kind, doc[len(doc)-300:])
		}
		if !strings.Contains(rels, `Id="`+m[1]+`" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/`+kind+`" Target="`+kind+`1.xml"`) {
			t.Errorf("relationship %s for the %s is missing in %s", m[1], kind, rels)
		}
		if !strings.Contains(p["[Content_Types].xml"], `/word/`+kind+`1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.`+kind+`+xml"`) {
			t.Errorf("no content type for the %s", kind)
		}
	}
	if strings.Index(doc, "headerReference") > strings.Index(doc, "<w:pgSz") {
		t.Error("the references come before the page size in sectPr")
	}
	if !strings.Contains(doc, `TOC \o "1-3" \h \z \u`) || !strings.Contains(doc, `w:dirty="true"`) {
		t.Error("the contents are a TOC field that Word refreshes")
	}
	anchors := regexp.MustCompile(`<w:hyperlink w:anchor="(_Toc\d+)"`).FindAllStringSubmatch(doc, -1)
	if len(anchors) != 4 {
		t.Fatalf("contents entries = %d", len(anchors))
	}
	for _, a := range anchors {
		if !strings.Contains(doc, `w:name="`+a[1]+`"/>`) || !strings.Contains(doc, `PAGEREF `+a[1]+` \h`) {
			t.Errorf("entry %s has no bookmark or page reference", a[1])
		}
	}
	if strings.Count(doc, "<w:bookmarkStart") != strings.Count(doc, "<w:bookmarkEnd") || strings.Count(doc, "<w:bookmarkStart") != 4 {
		t.Error("each heading has a matching bookmark")
	}
	back, err := docximport.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, it := range back.Content {
		if it.Chars != nil {
			all.WriteString(it.Chars.Text)
		}
	}
	if s := all.String(); !strings.Contains(s, "Chapter 2") || !strings.Contains(s, "words of the paragraph") {
		t.Errorf("the imported text lost the body: %.200q", s)
	}
	snap.Header, snap.Footer, snap.TOC = "", "", false
	data, _ = DOCX{}.Export(snap)
	p = docxParts(t, data)
	if _, ok := p["word/header1.xml"]; ok || strings.Contains(p["word/document.xml"], "headerReference") || strings.Contains(p["word/document.xml"], "bookmarkStart") {
		t.Error("nothing was asked for")
	}
}

func TestRTFHeaderFooterAndSheet(t *testing.T) {
	snap := longSnapshot(1)
	snap.Header = "{title}"
	snap.Footer = "Page {page} of {pages}"
	out, err := RTF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`\paperw9000\paperh12000`, `\margl900\margr900\margt1050\margb1050`, `{\header`, `{\footer`, `Annual report`, `\fldinst PAGE}`, `\fldinst NUMPAGES}`} {
		if !strings.Contains(s, want) {
			t.Errorf("RTF lacks %q:\n%s", want, s[:min(len(s), 900)])
		}
	}
	if depth := 0; true {
		for _, r := range strings.ReplaceAll(strings.ReplaceAll(s, `\{`, ""), `\}`, "") {
			switch r {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth < 0 {
				t.Fatal("unbalanced braces")
			}
		}
		if depth != 0 {
			t.Fatal("unbalanced braces")
		}
	}
}

func TestRTFContentsListHeadingsWithPageReferences(t *testing.T) {
	snap := longSnapshot(1)
	snap.TOC = true
	snap.TOCTitle = "Inhalt"
	out, err := RTF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{`Inhalt`, `\tldot`, `\fldinst PAGEREF _Toc`, `\fldinst HYPERLINK \\l _Toc`, `\flddirty`, `{\*\bkmkstart _Toc`, `{\*\bkmkend _Toc`, `\page`} {
		if !strings.Contains(s, want) {
			t.Errorf("RTF contents lack %q:\n%s", want, s[:min(len(s), 1200)])
		}
	}
	if strings.Count(s, `\bkmkstart`) != strings.Count(s, `PAGEREF`) {
		t.Errorf("%d bookmarks for %d page references", strings.Count(s, `\bkmkstart`), strings.Count(s, `PAGEREF`))
	}
	snap.TOC = false
	off, _ := RTF{}.Export(snap)
	if strings.Contains(string(off), "PAGEREF") {
		t.Error("contents written although not asked for")
	}
}
