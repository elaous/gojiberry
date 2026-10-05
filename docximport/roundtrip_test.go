package docximport_test

import (
	"reflect"
	"strings"
	"testing"

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
