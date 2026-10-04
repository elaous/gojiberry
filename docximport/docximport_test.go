package docximport

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

const nsDecl = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:dc="http://purl.org/dc/elements/1.1/"`

// a 1x1 PNG
var png1x1, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func docx(t *testing.T, body string) []byte {
	t.Helper()
	files := map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document ` + nsDecl + `><w:body>` + body + `</w:body></w:document>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="x" Target="https://example.com/page" TargetMode="External"/>
<Relationship Id="rId2" Type="x" Target="media/image1.png"/></Relationships>`,
		"word/styles.xml": `<?xml version="1.0"?><w:styles ` + nsDecl + `>
<w:style w:styleId="Heading1"><w:name w:val="heading 1"/></w:style>
<w:style w:styleId="Heading2"><w:name w:val="heading 2"/></w:style>
<w:style w:styleId="Quote"><w:name w:val="Quote"/></w:style></w:styles>`,
		"word/numbering.xml": `<?xml version="1.0"?><w:numbering ` + nsDecl + `>
<w:abstractNum w:abstractNumId="0"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>
<w:abstractNum w:abstractNumId="1"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="lowerLetter"/></w:lvl></w:abstractNum>
<w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="1"/></w:num></w:numbering>`,
		"word/footnotes.xml": `<?xml version="1.0"?><w:footnotes ` + nsDecl + `>
<w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:t>sep</w:t></w:r></w:p></w:footnote>
<w:footnote w:id="1"><w:p><w:r><w:t>The note text.</w:t></w:r></w:p></w:footnote></w:footnotes>`,
		"docProps/core.xml": `<?xml version="1.0"?><cp:coreProperties xmlns:cp="x" ` + nsDecl + `><dc:title>My title</dc:title></cp:coreProperties>`,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(content))
	}
	w, _ := zw.Create("word/media/image1.png")
	_, _ = w.Write(png1x1)
	_ = zw.Close()
	return buf.Bytes()
}

func TestImportsStructureAndFormatting(t *testing.T) {
	body := `
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Title here</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">Plain, </w:t></w:r><w:r><w:rPr><w:b/><w:i/></w:rPr><w:t>bold italic</w:t></w:r>
 <w:hyperlink r:id="rId1"><w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t>a link</w:t></w:r></w:hyperlink>
 <w:r><w:rPr><w:vertAlign w:val="superscript"/></w:rPr><w:t>2</w:t></w:r><w:r><w:br/><w:t>after break</w:t></w:r>
 <w:r><w:footnoteReference w:id="1"/></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>a bullet</w:t></w:r></w:p>
<w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="2"/></w:numPr></w:pPr><w:r><w:t>nested ordered</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Quote"/><w:jc w:val="center"/></w:pPr><w:r><w:t>quoted</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>d</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:drawing><wp:inline><wp:extent cx="952500" cy="476250"/><a:graphic><a:graphicData><a:pic><a:blipFill><a:blip r:embed="rId2"/></a:blipFill></a:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>
<w:p><w:r><w:br w:type="page"/><w:t>on the next page</w:t></w:r></w:p>`
	d, err := Import(docx(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "My title" {
		t.Errorf("title %q", d.Title)
	}
	var text strings.Builder
	var sawBold, sawLink, sawSup, sawHeading, sawQuote, sawBullet, sawOrdered, sawTable, sawImage, sawBreak, sawNote bool
	for _, it := range d.Content {
		switch {
		case it.Chars != nil:
			text.WriteString(it.Chars.Text)
			f := it.Chars.Format
			if f.FontWeight == 700 && f.FontStyle == "italic" && it.Chars.Text == "bold italic" {
				sawBold = true
			}
			if f.Link == "https://example.com/page" && it.Chars.Text == "a link" && f.Underline {
				sawLink = true
			}
			if f.Superscript {
				sawSup = true
			}
			if f.Scale == 2 && it.Chars.Text == "Title here" {
				sawHeading = true
			}
		case it.Para != nil:
			switch {
			case it.Para.QuoteBar != "" && it.Para.Align == 1:
				sawQuote = true
			case it.Para.List.Kind == 1 && it.Para.List.Level == 0:
				sawBullet = true
			case it.Para.List.Kind == 2 && it.Para.List.Level == 1:
				sawOrdered = true
			}
		case it.Table != nil:
			sawTable = len(it.Table.Rows) == 2 && it.Table.Rows[1].Cells[1] == "d"
		case it.Image != nil:
			sawImage = strings.HasPrefix(it.Image.URL, "data:image/png;base64,") && it.Image.Width == 100 && it.Image.Height == 50
		case it.Break != nil:
			sawBreak = true
		case it.Footnote != nil:
			sawNote = true
		}
	}
	for name, ok := range map[string]bool{"bold italic": sawBold, "link": sawLink, "superscript": sawSup, "heading": sawHeading, "quote": sawQuote,
		"bullet": sawBullet, "nested ordered list": sawOrdered, "table": sawTable, "image": sawImage, "page break": sawBreak, "footnote": sawNote} {
		if !ok {
			t.Errorf("%s was not imported (content %+v)", name, d.Content)
		}
	}
	if !strings.Contains(text.String(), "Plain, ") || !strings.Contains(text.String(), " after break") || !strings.Contains(text.String(), "on the next page") {
		t.Errorf("text = %q", text.String())
	}
	if len(d.Footnotes) != 1 || d.Footnotes[0].Text != "The note text." {
		t.Errorf("footnotes %+v", d.Footnotes)
	}
}

func TestRejectsOtherFiles(t *testing.T) {
	for name, data := range map[string][]byte{"text": []byte("hello"), "empty zip": docxZipOnly(t)} {
		if _, err := Import(data); err != ErrNotDocx {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func docxZipOnly(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("readme.txt")
	_, _ = w.Write([]byte("not a docx"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestImportsTableMergesAndCellStyles(t *testing.T) {
	body := `<w:tbl>
<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:shd w:val="clear" w:fill="FFF3B0"/></w:tcPr><w:p><w:pPr><w:jc w:val="center"/></w:pPr><w:r><w:rPr><w:b/></w:rPr><w:t>wide</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>tall</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>e</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>f</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p/></w:tc><w:tc><w:p><w:r><w:t>h</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>i</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl>`
	d, err := Import(docx(t, body))
	if err != nil {
		t.Fatal(err)
	}
	var tb *docfile.TableItem
	for _, it := range d.Content {
		if it.Table != nil {
			tb = it.Table
		}
	}
	if tb == nil {
		t.Fatalf("no table in %+v", d.Content)
	}
	wantRows := [][]string{{"wide", "", "c"}, {"tall", "e", "f"}, {"", "h", "i"}}
	for r, row := range wantRows {
		for c, want := range row {
			if got := tb.Rows[r].Cells[c]; got != want {
				t.Errorf("cell %d,%d = %q, want %q", r, c, got, want)
			}
		}
	}
	wantMerges := []docfile.TableMerge{{Row: 0, Col: 0, Rows: 1, Cols: 2}, {Row: 1, Col: 0, Rows: 2, Cols: 1}}
	if !reflect.DeepEqual(tb.Merges, wantMerges) {
		t.Errorf("merges %+v, want %+v", tb.Merges, wantMerges)
	}
	if len(tb.Styles) != 1 || tb.Styles[0].Background != "#fff3b0" || !tb.Styles[0].Bold || tb.Styles[0].Align != 1 {
		t.Errorf("styles %+v", tb.Styles)
	}
}
