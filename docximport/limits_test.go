package docximport

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func docxWith(t *testing.T, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body + `</w:body></w:document>`))
	_ = zw.Close()
	return buf.Bytes()
}

func TestHostileInputIsBounded(t *testing.T) {
	// Nesting far beyond maxDepth is refused rather than walked.
	deep := strings.Repeat("<w:sdt><w:sdtContent>", 2000) + strings.Repeat("</w:sdtContent></w:sdt>", 2000)
	if _, err := Import(docxWith(t, deep)); err == nil {
		t.Error("a 4000-deep document was accepted")
	}
	// One wide cell must not allocate a grid of its width times its rows.
	var tbl strings.Builder
	tbl.WriteString("<w:tbl>")
	for range 300 {
		tbl.WriteString(`<w:tr><w:tc><w:tcPr><w:gridSpan w:val="63"/></w:tcPr><w:p/></w:tc><w:tc><w:tcPr><w:gridSpan w:val="63"/></w:tcPr><w:p/></w:tc><w:tc><w:tcPr><w:gridSpan w:val="63"/></w:tcPr><w:p/></w:tc><w:tc><w:tcPr><w:gridSpan w:val="63"/></w:tcPr><w:p/></w:tc><w:tc><w:p/></w:tc></w:tr>`)
	}
	tbl.WriteString("</w:tbl>")
	doc, err := Import(docxWith(t, tbl.String()))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range doc.Content {
		if it.Table != nil && len(it.Table.Rows[0].Cells) > maxCols {
			t.Errorf("table is %d columns wide", len(it.Table.Rows[0].Cells))
		}
	}
}
