package export

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
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

func TestBrailleAsksTheServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Text, Table string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/translate" || !strings.Contains(req.Text, "Intro") || req.Table != "en-us-g1.ctb" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"braille": ",intro"})
	}))
	defer srv.Close()
	b := Braille{Backend: srv.URL + "/", Table: "en-us-g1.ctb"}
	out, err := b.Export(sampleDoc())
	if err != nil || string(out) != ",intro" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := (Braille{Backend: srv.URL, Table: "other"}).Export(sampleDoc()); err == nil {
		t.Error("a refusal from the server must be an error")
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
