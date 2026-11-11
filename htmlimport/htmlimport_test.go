package htmlimport

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func plain(d docfile.Document) string {
	var sb strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			sb.WriteString(it.Chars.Text)
		}
	}
	return sb.String()
}

func chars(d docfile.Document, sub string) *docfile.Chars {
	for _, it := range d.Content {
		if it.Chars != nil && strings.Contains(it.Chars.Text, sub) {
			return it.Chars
		}
	}
	return nil
}

const page = `<!doctype html><html lang="en"><head><title> My  page </title><style>p{color:red}</style><script>alert(1)</script></head>
<body><h1>Heading</h1><p>Some <strong>bold</strong>, <em>italic</em>, <u>under</u> and <s>gone</s> text with a
<a href="https://example.org/x">link</a> and <a href="javascript:alert(1)">bad</a>.</p>
<p lang="fr">bonjour <sup>2</sup><sub>3</sub> <mark>marked</mark> <span style="color:#f00;background:#00ff00">coloured</span></p>
<ul><li>one<ul><li>inner</li></ul></li><li>two</li></ul><ol><li>first</li></ol>
<blockquote>quoted</blockquote><pre>a
  b</pre><hr>
<img src="https://tracker.example/p.png" alt="remote"><img src="data:image/png;base64,iVBORw0KGgo=" alt="a square">
<table><tr><th colspan="2">Head</th></tr><tr><td>a</td><td rowspan="2">b</td></tr><tr><td>c</td></tr></table>
<form><input type=password name=q></form><iframe src="x"></iframe><svg></svg><video></video>
<div style="position:absolute">layout</div></body></html>`

func TestImportReadsWhatADocumentNeeds(t *testing.T) {
	d, s, err := ImportReport([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "My page" {
		t.Errorf("title %q", d.Title)
	}
	txt := plain(d)
	for _, want := range []string{"Heading\n", "Some bold, italic, under and gone text", "bonjour", "one\n", "inner\n", "two\n", "first\n", "a\n  b\n", "layout"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text lacks %q:\n%q", want, txt)
		}
	}
	for _, bad := range []string{"alert", "color:red", "tracker"} {
		if strings.Contains(txt, bad) {
			t.Errorf("text holds %q", bad)
		}
	}
	if c := chars(d, "Heading"); c == nil || c.Format.FontWeight != 700 || c.Format.Scale != 2 {
		t.Errorf("heading: %+v", c)
	}
	if c := chars(d, "bold"); c == nil || c.Format.FontWeight != 700 {
		t.Errorf("bold: %+v", c)
	}
	if c := chars(d, "italic"); c == nil || c.Format.FontStyle != "italic" {
		t.Errorf("italic: %+v", c)
	}
	if c := chars(d, "link"); c == nil || c.Format.Link != "https://example.org/x" {
		t.Errorf("link: %+v", c)
	}
	if c := chars(d, "bad"); c == nil || c.Format.Link != "" {
		t.Errorf("a javascript: link must keep its text and lose its address: %+v", c)
	}
	if c := chars(d, "bonjour"); c == nil || c.Format.Lang != "fr" {
		t.Errorf("language: %+v", c)
	}
	if c := chars(d, "coloured"); c == nil || c.Format.Color != "#ff0000" || c.Format.Background != "#00ff00" {
		t.Errorf("colours: %+v", c)
	}
	if c := chars(d, "a\n  b"); c == nil || c.Format.FontFamily != "monospace" {
		t.Errorf("pre: %+v", c)
	}
	var lists, quotes, rules, images, tables int
	var alt string
	var tbl *docfile.TableItem
	for _, it := range d.Content {
		switch {
		case it.Para != nil && it.Para.List.Kind != 0:
			lists++
			if it.Para.List.Level == 1 && it.Para.List.Kind != 1 {
				t.Error("inner list kind")
			}
		case it.Para != nil && it.Para.QuoteBar != "":
			quotes++
		case it.Rule != nil:
			rules++
		case it.Image != nil:
			images++
			alt = it.Image.Alt
		case it.Table != nil:
			tables++
			tbl = it.Table
		}
	}
	if lists != 4 || quotes != 1 || rules != 1 || images != 1 || alt != "a square" || tables != 1 {
		t.Errorf("blocks: lists %d quotes %d rules %d images %d (%q) tables %d", lists, quotes, rules, images, alt, tables)
	}
	if tbl == nil || len(tbl.Rows) != 3 || len(tbl.Rows[0].Cells) != 2 || tbl.Rows[1].Cells[1] != "b" || tbl.Rows[2].Cells[0] != "c" {
		t.Fatalf("table: %+v", tbl)
	}
	if len(tbl.Merges) != 2 {
		t.Errorf("merges: %+v", tbl.Merges)
	}
	want := Skipped{Scripts: 1, Styles: 1, Forms: 1, Frames: 1, Graphics: 1, Images: 1, Links: 1, Layout: 1}
	if s != want {
		t.Errorf("skipped %+v, want %+v", s, want)
	}
	if s.Total() != 8 {
		t.Errorf("total %d", s.Total())
	}
}

func TestBrokenMarkupAndLimits(t *testing.T) {
	d, err := Import([]byte("<p>one<p>two <b>bold<i>both</p>after"))
	if err != nil {
		t.Fatal(err)
	}
	if got := plain(d); !strings.Contains(got, "one\n") || !strings.Contains(got, "two bold") || !strings.Contains(got, "after") {
		t.Errorf("broken markup read as a browser reads it: %q", got)
	}
	if _, err := Import(make([]byte, MaxBytes+1)); err != ErrTooLarge {
		t.Errorf("a page over the limit: %v", err)
	}
	deep := strings.Repeat("<div>", 5000) + "deep" + strings.Repeat("</div>", 5000)
	if _, err := Import([]byte(deep)); err == nil {
		t.Log("deep nesting was read")
	}
	if d, err := Import(nil); err != nil || len(d.Content) != 0 {
		t.Errorf("empty: %v %v", d, err)
	}
}

func TestVerticalWritingModeIsRead(t *testing.T) {
	for _, c := range []struct {
		html string
		want bool
	}{
		{`<html style="writing-mode:vertical-rl"><body><p>x</p></body></html>`, true},
		{`<body style="color:red; Writing-Mode: vertical-lr"><p>x</p></body>`, true},
		{`<body style="writing-mode: horizontal-tb"><p>x</p></body>`, false},
		{`<body><p>x</p></body>`, false},
	} {
		d, err := Import([]byte(c.html))
		if err != nil || d.PageLayout().Vertical != c.want {
			t.Errorf("%s: vertical %v, want %v (%v)", c.html, d.PageLayout().Vertical, c.want, err)
		}
	}
}

func TestControlsInTableCellsAreCellFields(t *testing.T) {
	d, err := Import([]byte(`<table>
<tr><th>Item</th><th>Qty</th><th>Ok</th></tr>
<tr><td>Pens</td><td><label>Qty <input type="number" name="qty" value="3"></label></td><td><input type="checkbox" name="ok" checked></td></tr>
<tr><td>Ink</td><td><input type="password" name="pw"></td><td><select name="s"><option>A</option><option selected>B</option></select></td></tr>
</table>`))
	if err != nil {
		t.Fatal(err)
	}
	var tb *docfile.TableItem
	for _, it := range d.Content {
		if it.Table != nil {
			tb = it.Table
		}
	}
	if tb == nil || len(tb.Fields) != 3 {
		t.Fatalf("fields: %+v", tb)
	}
	got := map[[2]int]docfile.FormFieldItem{}
	for _, f := range tb.Fields {
		got[[2]int{f.Row, f.Col}] = *f.Field
	}
	if q := got[[2]int{1, 1}]; q.Kind != "number" || q.Label != "Qty" || q.Value != "3" || q.Name != "qty" {
		t.Errorf("the number field: %+v", q)
	}
	if c := got[[2]int{1, 2}]; c.Kind != "checkbox" || c.Value != "true" {
		t.Errorf("the checkbox: %+v", c)
	}
	if s := got[[2]int{2, 2}]; s.Kind != "select" || s.Value != "B" || len(s.Options) != 2 {
		t.Errorf("the list: %+v", s)
	}
	if _, ok := got[[2]int{2, 1}]; ok {
		t.Error("a password is never a field")
	}
	if tb.Rows[1].Cells[1] != "Qty: 3" || tb.Rows[1].Cells[2] != "[x]" || tb.Rows[1].Cells[0] != "Pens" {
		t.Errorf("the cells' text: %q", tb.Rows[1].Cells)
	}
}
