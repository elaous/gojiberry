package export

import (
	"encoding/json"
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
