package docximport_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docximport"
	"github.com/elaous/gojiberry/export"
)

const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// What the exporter writes, the importer reads back: links, super and
// subscript, lists (ordered restarting, nested), a table and a picture.
func TestDocxRoundTrip(t *testing.T) {
	c := func(text string, f docfile.Format) docfile.Item {
		return docfile.Item{Chars: &docfile.Chars{Text: text, Format: f}}
	}
	in := docfile.Document{Title: "Round trip", Content: []docfile.Item{
		c("A heading", docfile.Format{Scale: 2}), c("\n", docfile.Format{}),
		c("Some ", docfile.Format{}), c("bold", docfile.Format{FontWeight: 700}), c(" and ", docfile.Format{}),
		c("a link", docfile.Format{Link: "https://example.com/x?a=1&b=2"}), c(" H", docfile.Format{}), c("2", docfile.Format{Subscript: true}), c("O\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 2}}}, c("one\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 2}}}, c("two\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 1}}}, c("bullet\n", docfile.Format{}),
		{Para: &docfile.Para{List: docfile.List{Kind: 2}}}, c("restarted\n", docfile.Format{}),
		{Table: &docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"a", "b"}}, {Cells: []string{"c", "d"}}}}},
		{Image: &docfile.ImageItem{URL: "data:image/png;base64," + pngB64, Width: 64, Height: 32}},
		c("\nthe end", docfile.Format{}),
	}}
	data, err := export.DOCX{}.Export(export.FromDocument(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := docximport.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "Round trip" {
		t.Errorf("title %q", out.Title)
	}
	var text strings.Builder
	var link, sub, heading, table, image bool
	var lists []docfile.List
	for _, it := range out.Content {
		switch {
		case it.Chars != nil:
			text.WriteString(it.Chars.Text)
			link = link || it.Chars.Format.Link == "https://example.com/x?a=1&b=2" && it.Chars.Text == "a link"
			sub = sub || it.Chars.Format.Subscript && it.Chars.Text == "2"
			heading = heading || it.Chars.Format.Scale == 2 && it.Chars.Text == "A heading"
		case it.Para != nil && it.Para.List.Kind != 0:
			lists = append(lists, it.Para.List)
		case it.Table != nil:
			table = len(it.Table.Rows) == 2 && it.Table.Rows[0].Cells[1] == "b"
		case it.Image != nil:
			image = it.Image.Width == 64 && it.Image.Height == 32 && strings.HasPrefix(it.Image.URL, "data:image/png;base64,")
		}
	}
	for name, ok := range map[string]bool{"link": link, "subscript": sub, "heading": heading, "table": table, "image": image} {
		if !ok {
			t.Errorf("%s lost in the round trip", name)
		}
	}
	if len(lists) != 4 || lists[0].Kind != 2 || lists[2].Kind != 1 || lists[3].Kind != 2 {
		t.Errorf("lists %+v", lists)
	}
	if !strings.Contains(text.String(), "Some bold and a link H2O") || !strings.HasSuffix(text.String(), "the end") {
		t.Errorf("text %q", text.String())
	}
}

// Merged cells survive the trip through a Word file: the writer emits gridSpan
// and vMerge, the reader turns them back into merges.
func TestDocxRoundTripKeepsMergedCells(t *testing.T) {
	in := docfile.Document{Content: []docfile.Item{{Table: &docfile.TableItem{
		Rows: []docfile.TableRow{
			{Cells: []string{"wide", "", "c", "d"}},
			{Cells: []string{"tall", "e", "block", ""}},
			{Cells: []string{"", "g", "", ""}},
			{Cells: []string{"x", "y", "z", "w"}},
		},
		Merges: []docfile.TableMerge{{Row: 0, Col: 0, Rows: 1, Cols: 2}, {Row: 1, Col: 0, Rows: 2, Cols: 1}, {Row: 1, Col: 2, Rows: 2, Cols: 2}},
	}}}}
	data, err := export.DOCX{}.Export(export.FromDocument(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := docximport.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	var got *docfile.TableItem
	for _, it := range out.Content {
		if it.Table != nil {
			got = it.Table
		}
	}
	if got == nil {
		t.Fatalf("no table: %+v", out.Content)
	}
	if !reflect.DeepEqual(got.Merges, in.Content[0].Table.Merges) {
		t.Errorf("merges %+v, want %+v", got.Merges, in.Content[0].Table.Merges)
	}
	for r, row := range in.Content[0].Table.Rows {
		for c, want := range row.Cells {
			if got.Rows[r].Cells[c] != want {
				t.Errorf("cell %d,%d = %q, want %q", r, c, got.Rows[r].Cells[c], want)
			}
		}
	}
}

// Cell background, alignment and bold survive the trip too.
func TestDocxRoundTripKeepsCellStyles(t *testing.T) {
	in := docfile.Document{Content: []docfile.Item{{Table: &docfile.TableItem{
		Rows: []docfile.TableRow{{Cells: []string{"a", "b"}}, {Cells: []string{"c", "d"}}},
		Styles: []docfile.TableCellStyle{
			{Row: 0, Col: 0, Background: "#fff3b0", Align: 1, Bold: true},
			{Row: 1, Col: 1, Align: 2},
		},
	}}}}
	data, err := export.DOCX{}.Export(export.FromDocument(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := docximport.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	var got *docfile.TableItem
	for _, it := range out.Content {
		if it.Table != nil {
			got = it.Table
		}
	}
	if got == nil {
		t.Fatal("no table")
	}
	want := in.Content[0].Table.Styles
	if !reflect.DeepEqual(got.Styles, want) {
		t.Errorf("styles %+v, want %+v", got.Styles, want)
	}
	html, _ := export.HTML{}.Export(export.FromDocument(in))
	if !strings.Contains(string(html), `background:#fff3b0;text-align:center;`) || !strings.Contains(string(html), `text-align:right;`) {
		t.Errorf("html: %s", html)
	}
}

// Suggestions leave as Word tracked changes and come back as suggestions: the replacement is still one,
// the struck text is still there to accept or reject, and who and when are kept.
func TestTrackedChangesRoundTrip(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	mark := func(kind canvas.ProposalKind, id, author string) string {
		return canvas.ProposalMark{Kind: kind, ID: id, Author: author, Time: at}.String()
	}
	c := func(text, proposal string) docfile.Item {
		return docfile.Item{Chars: &docfile.Chars{Text: text, Format: docfile.Format{Proposal: proposal}}}
	}
	in := docfile.Document{Content: []docfile.Item{
		c("the ", ""), c("brown", mark(canvas.ProposalDelete, "p1", "ana@example.com")), c("red", mark(canvas.ProposalInsert, "p1", "ana@example.com")),
		c(" fox ", ""), c("runs", mark(canvas.ProposalDelete, "p2", "bo")), c(" and ", ""), c("hides", mark(canvas.ProposalInsert, "p3", "cy")), c("\n", ""),
	}}
	data, err := export.DOCX{}.Export(export.FromDocumentTracked(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := docximport.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	f := &canvas.TextFlow{}
	out.Apply(f)
	ps := f.Proposals()
	if len(ps) != 3 {
		t.Fatalf("%d proposals: %+v", len(ps), ps)
	}
	if ps[0].Author != "ana@example.com" || ps[0].Deleted != "brown" || ps[0].Inserted != "red" || !ps[0].Time.Equal(at) {
		t.Errorf("the replacement is one proposal: %+v", ps[0])
	}
	if ps[1].Author != "bo" || ps[1].Deleted != "runs" || ps[1].Inserted != "" {
		t.Errorf("a removal: %+v", ps[1])
	}
	if ps[2].Author != "cy" || ps[2].Inserted != "hides" || ps[2].Deleted != "" {
		t.Errorf("an addition: %+v", ps[2])
	}
	f.AcceptAllProposals()
	if got := strings.TrimSpace(f.EditText()); got != "the red fox  and hides" {
		t.Errorf("accepted: %q", got)
	}
}

// A vertical section comes back as vertical text.
func TestDocxVerticalTextRoundTrip(t *testing.T) {
	snap := export.Snapshot{Title: "T", Vertical: true, PageWidth: 794, PageHeight: 1123, MarginLeft: 96, MarginTop: 96, MarginRight: 96, MarginBottom: 96,
		Blocks: []export.Block{{Kind: export.Paragraph, Segments: []export.Segment{{Text: "日本語"}}}}}
	data, err := export.DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	d, err := docximport.Import(data)
	if err != nil || !d.PageLayout().Vertical {
		t.Errorf("vertical after import: %v, %v", d.PageLayout().Vertical, err)
	}
	snap.Vertical = false
	data, _ = export.DOCX{}.Export(snap)
	if d, _ := docximport.Import(data); d.PageLayout().Vertical {
		t.Error("horizontal text came back vertical")
	}
}
