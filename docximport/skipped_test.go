package docximport

import (
	"archive/zip"
	"bytes"
	"testing"
)

const wNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"`

func docxParts(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

// What Import leaves out of a file is counted, so the person is told: a file with comments, a header, a text
// box, a chart, an equation, an endnote, fields, bookmarks, columns and indented paragraphs reports each.
func TestInspectCountsWhatImportLeavesOut(t *testing.T) {
	body := `<w:p><w:pPr><w:ind w:left="720"/></w:pPr><w:bookmarkStart w:id="1" w:name="intro"/><w:r><w:t>Kept text</w:t></w:r></w:p>` +
		`<w:p><w:bookmarkStart w:id="2" w:name="_GoBack"/><w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText>PAGE</w:instrText></w:r><w:r><w:t>3</w:t></w:r></w:p>` +
		`<w:p><w:fldSimple w:instr="NUMPAGES"><w:r><w:t>9</w:t></w:r></w:fldSimple><w:r><w:endnoteReference w:id="2"/></w:r></w:p>` +
		`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><wp:txbx><w:txbxContent><w:p><w:r><w:t>In a box</w:t></w:r></w:p></w:txbxContent></wp:txbx></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>` +
		`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData uri="chart"><chart/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>` +
		`<w:p><m:oMath><m:r><m:t>x</m:t></m:r></m:oMath></w:p>` +
		`<w:sectPr><w:cols w:num="2"/></w:sectPr>`
	data := docxParts(t, map[string]string{
		"word/document.xml": `<w:document ` + wNS + `><w:body>` + body + `</w:body></w:document>`,
		"word/comments.xml": `<w:comments ` + wNS + `><w:comment w:id="0" w:author="Ann"><w:p><w:r><w:t>Check this</w:t></w:r></w:p></w:comment><w:comment w:id="1" w:author="Bo"><w:p><w:r><w:t>Done</w:t></w:r></w:p></w:comment></w:comments>`,
		"word/header1.xml":  `<w:hdr ` + wNS + `><w:p><w:r><w:t>Company</w:t></w:r></w:p></w:hdr>`,
		"word/footer1.xml":  `<w:ftr ` + wNS + `><w:p/></w:ftr>`, // empty: nothing lost
	})
	got, err := Inspect(data)
	if err != nil {
		t.Fatal(err)
	}
	want := Skipped{Comments: 2, HeaderFoots: 1, TextBoxes: 1, Drawings: 1, Equations: 1, Endnotes: 1, Fields: 2, Bookmarks: 1, Columns: 1, Layout: 1}
	if got != want {
		t.Errorf("Inspect = %+v, want %+v", got, want)
	}
	if got.Total() != 12 {
		t.Errorf("Total = %d, want 12", got.Total())
	}
	// The same file imports, and what is in the body is read.
	doc, err := Import(data)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range doc.Content {
		found = found || it.Chars != nil && it.Chars.Text == "Kept text"
	}
	if !found {
		t.Errorf("the body text was not read: %v", doc.Content)
	}
}

func TestInspectOfAPlainFileFindsNothingAndRefusesWhatIsNotDocx(t *testing.T) {
	data := docxWith(t, `<w:p><w:r><w:t>Just text</w:t></w:r></w:p>`)
	got, err := Inspect(data)
	if err != nil || got.Total() != 0 {
		t.Errorf("Inspect of a plain file = %+v, %v", got, err)
	}
	if _, err := Inspect([]byte("not a zip")); err != ErrNotDocx {
		t.Errorf("Inspect of a non-zip: %v", err)
	}
}
