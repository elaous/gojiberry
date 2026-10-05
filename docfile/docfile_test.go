package docfile

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
)

func sample() Document {
	items := []canvas.Item{
		canvas.ParaFormat{Style: "Heading 1", Align: canvas.AlignCenter, List: canvas.ListStyle{Kind: canvas.ListOrdered, Level: 1, Start: 3, Checked: true}},
		canvas.Chars{Text: "Hello ", Format: canvas.Format{FontFamily: "Lato", FontWeight: 700, Color: "#123", Case: canvas.CaseSmallCaps, Link: "https://gojiberry.io"}},
		canvas.Chars{Text: "world\n"},
		canvas.ColumnConfig{Widths: []float64{100, 50.5}, Gap: 8},
		canvas.Break{}, // the zero value must survive
		canvas.Break{Kind: canvas.BreakPage},
	}
	d := Document{Title: "T", Updated: 42, Content: FromItems(items),
		Styles: FromStyles([]canvas.StyleDef{{Name: "Loud", Format: canvas.Format{Underline: true}, Para: canvas.ParaFormat{SpaceAfter: 6}, Paragraph: true}}),
		Fonts:  []FontRef{{Family: "Lato", Source: "Google", Variants: []FontVariant{{Weight: 700, URL: "u", Key: "fonts/Lato/700-n"}}}}}
	d.Comments = FromComments(canvas.NewCommentSet(canvas.Comment{ID: "c1", Author: "ana", Text: "**hi** @bob", Created: time.Unix(0, 5), Resolved: true,
		Replies: []canvas.Reply{{Author: "bob", Text: "ok", Created: time.Unix(0, 9)}, {Author: "x"}}}, canvas.Comment{ID: "c2"}))
	d.Set("author", "me")
	return d
}

func TestRoundTripAllFormats(t *testing.T) {
	for _, c := range Formats() {
		data, err := c.Marshal(sample())
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		got, err := c.Unmarshal(data)
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		if !reflect.DeepEqual(got, sample()) {
			t.Fatalf("%s: round trip differs\n got %+v\nwant %+v", c.Name(), got, sample())
		}
		if got2 := Detect(data); got2.Name() != c.Name() {
			t.Errorf("%s detected as %s", c.Name(), got2.Name())
		}
		if c.Ext() == "" || c.MIME() == "" || c.Label() == "" {
			t.Errorf("%s: incomplete metadata", c.Name())
		}
	}
}

func TestEmptyDocument(t *testing.T) {
	for _, c := range Formats() {
		data, err := c.Marshal(Document{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Unmarshal(data)
		if err != nil || got.Title != "" || len(got.Content) != 0 {
			t.Fatalf("%s: %v %+v", c.Name(), err, got)
		}
	}
}

func TestBadInput(t *testing.T) {
	for _, c := range Formats() {
		if _, err := c.Unmarshal([]byte("{{{ \xff\xff nonsense")); err == nil {
			t.Errorf("%s accepted garbage", c.Name())
		}
	}
}

func TestItemsRoundTrip(t *testing.T) {
	items := []canvas.Item{
		canvas.ParaFormat{Align: canvas.AlignRight, DropCap: 2},
		canvas.Chars{Text: "x", Format: canvas.Format{Scale: 2, Strike: true}},
		canvas.ColumnConfig{Count: 2, Gap: 4},
		canvas.Break{Kind: canvas.BreakColumn},
	}
	if got := ToItems(FromItems(items)); !reflect.DeepEqual(got, items) {
		t.Fatalf("got %#v", got)
	}
	if len(ToItems([]Item{{}})) != 0 {
		t.Fatal("an empty item is skipped")
	}
}

func TestFlowRoundTrip(t *testing.T) {
	f := &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "abc", Format: canvas.Format{Color: "red"}}}}
	f.Styles = canvas.DefaultStyles()[:2]
	d := FromFlow("Doc", f)
	g := &canvas.TextFlow{}
	d.Apply(g)
	if !reflect.DeepEqual(g.ContentItems(), f.ContentItems()) || len(g.Styles) != 2 {
		t.Fatalf("%v", g.ContentItems())
	}
	if !strings.Contains(d.String(), "Doc") {
		t.Fatal(d.String())
	}
}

// The mirrors must carry every field of the canvas types they stand for.
func TestMirrorsCoverCanvasTypes(t *testing.T) {
	for _, p := range []struct{ mirror, canvas reflect.Type }{
		{reflect.TypeFor[Format](), reflect.TypeFor[canvas.Format]()},
		{reflect.TypeFor[Para](), reflect.TypeFor[canvas.ParaFormat]()},
		{reflect.TypeFor[Columns](), reflect.TypeFor[canvas.ColumnConfig]()},
		{reflect.TypeFor[StyleDef](), reflect.TypeFor[canvas.StyleDef]()},
		{reflect.TypeFor[List](), reflect.TypeFor[canvas.ListStyle]()},
	} {
		if m := mirrorMissing(p.mirror, p.canvas); len(m) != 0 {
			t.Errorf("fields missing from docfile: %v", m)
		}
	}
}

func TestSchema(t *testing.T) {
	s := Schema()
	for _, want := range []string{`syntax = "proto3";`, "message Document {", "repeated Item content = 3;", "message Format {", "double font_size = 2;", "int64 font_weight = 4;"} {
		if !strings.Contains(s, want) {
			t.Errorf("schema lacks %q:\n%s", want, s)
		}
	}
}

func TestMetaAndLookup(t *testing.T) {
	var d Document
	d.Set("a", "1")
	d.Set("a", "2")
	if v, ok := d.Get("a"); !ok || v != "2" || len(d.Meta) != 1 {
		t.Fatal(d.Meta)
	}
	if _, ok := d.Get("nope"); ok {
		t.Fatal("missing key")
	}
	if _, err := ByName("nope"); err == nil {
		t.Fatal("unknown format")
	}
	if c, err := ByName("json"); err != nil || c.Name() != "json" {
		t.Fatal(err)
	}
	if got := strings.Join(FormatNames(), ","); got != "json,proto,proto-text" {
		t.Fatal(got)
	}
}

func TestFontRefsRoundTrip(t *testing.T) {
	in := []fonts.Font{{Family: "Lato", Source: "Google", Category: "sans-serif",
		Variants: []fonts.Variant{{Weight: 700, Italic: true, Style: "Bold Italic", URL: "u", Key: "fonts/Lato/700-i.woff2"}}}}
	if got := ToFonts(FromFonts(in)); !reflect.DeepEqual(got, in) {
		t.Fatalf("%+v", got)
	}
	if FromFonts(nil) != nil || ToFonts(nil) != nil {
		t.Fatal("nil in, nil out")
	}
}

func TestCommentsRoundTripThroughFlow(t *testing.T) {
	f := &canvas.TextFlow{Items: []canvas.Item{canvas.Chars{Text: "abc", Format: canvas.Format{Comment: "c1"}}}}
	f.Comments = canvas.NewCommentSet(canvas.Comment{ID: "c1", Author: "ana", Text: "hi", Created: time.Unix(0, 7)})
	d := FromFlow("T", f)
	g := &canvas.TextFlow{}
	d.Apply(g)
	c, ok := g.Comments.Get("c1")
	if !ok || c.Author != "ana" || !c.Created.Equal(time.Unix(0, 7)) {
		t.Fatalf("%+v", c)
	}
	if g.ContentItems()[0].(canvas.Chars).Format.Comment != "c1" {
		t.Fatal("the text keeps its comment ID")
	}
	if FromComments(nil) != nil {
		t.Fatal("nil set")
	}
}

func TestNotesRoundTrip(t *testing.T) {
	f := &canvas.TextFlow{Items: []canvas.Item{
		canvas.Inline{Object: &canvas.TOCObject{Title: "Contents"}, Block: true}, canvas.Chars{Text: "\n"},
		canvas.Chars{Text: "text", Format: canvas.Format{Index: "term", Bookmark: "b"}},
		canvas.Inline{Object: &canvas.FootnoteRef{ID: "n1"}},
		canvas.Inline{Object: &canvas.IndexObject{}, Block: true},
		canvas.Inline{Object: oddObject{}, Block: true}, // objects without a saved form are dropped
	}}
	f.Footnotes = canvas.NewFootnoteSet(canvas.Footnote{ID: "n1", Text: "the note"})
	for _, c := range Formats() {
		data, err := c.Marshal(FromFlow("T", f))
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.Unmarshal(data)
		if err != nil {
			t.Fatal(err)
		}
		g := &canvas.TextFlow{}
		d.Apply(g)
		items := g.ContentItems()
		if len(items) != 5 {
			t.Fatalf("%s: %d items: %#v", c.Name(), len(items), items)
		}
		if toc, ok := items[0].(canvas.Inline); !ok || !toc.Block || toc.Object.(*canvas.TOCObject).Title != "Contents" {
			t.Fatalf("%s: toc %#v", c.Name(), items[0])
		}
		if ch := items[2].(canvas.Chars); ch.Format.Index != "term" || ch.Format.Bookmark != "b" {
			t.Fatalf("%s: %#v", c.Name(), ch)
		}
		if ref := items[3].(canvas.Inline).Object.(*canvas.FootnoteRef); ref.ID != "n1" {
			t.Fatal(ref)
		}
		if _, ok := items[4].(canvas.Inline).Object.(*canvas.IndexObject); !ok {
			t.Fatal("index block")
		}
		if n, ok := g.Footnotes.Get("n1"); !ok || n.Text != "the note" {
			t.Fatal(n)
		}
	}
}

func TestRuleAndTableRoundTrip(t *testing.T) {
	tbl := &canvas.Table{Cells: [][]string{{"a", "b"}, {"c", "d"}}}
	f := &canvas.TextFlow{Items: []canvas.Item{
		canvas.Inline{Object: &canvas.Rule{Color: "#123", Thickness: 2}, Block: true},
		canvas.Inline{Object: canvas.NewTableObject(tbl), Block: true},
	}}
	for _, c := range Formats() {
		data, err := c.Marshal(FromFlow("T", f))
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.Unmarshal(data)
		if err != nil {
			t.Fatal(err)
		}
		items := ToItems(d.Content)
		if len(items) != 2 {
			t.Fatalf("%s: %d items", c.Name(), len(items))
		}
		if r := items[0].(canvas.Inline).Object.(*canvas.Rule); r.Color != "#123" || r.Thickness != 2 {
			t.Fatalf("%s: %+v", c.Name(), r)
		}
		if got := items[1].(canvas.Inline).Object.(*canvas.TableObject).View.Table.Cells; !reflect.DeepEqual(got, tbl.Cells) {
			t.Fatalf("%s: %v", c.Name(), got)
		}
	}
	if got := ToItems([]Item{{Table: &TableItem{}}}); len(got) != 1 {
		t.Fatal("an empty table still has a cell")
	}
}

type oddObject struct{}

func (oddObject) Measure(canvas.Painter, float64, float64) (float64, float64) { return 1, 1 }
func (oddObject) DrawAt(canvas.Painter, float64, float64)                     {}

func TestTableMergesAndStylesRoundTrip(t *testing.T) {
	tbl := canvas.NewTableFrom([][]string{{"a", "b", "c"}, {"d", "e", "f"}})
	tbl.MergeRange(0, 0, 1, 1)
	tbl.SetCellStyle(0, 2, canvas.CellStyle{Background: "#ff0", Bold: true, Align: canvas.AlignRight})
	tbl.SetCellStyle(1, 2, canvas.CellStyle{Color: "#f00", Italic: true})
	f := &canvas.TextFlow{Items: []canvas.Item{canvas.Inline{Object: canvas.NewTableObject(tbl), Block: true}}}
	for _, c := range Formats() {
		data, err := c.Marshal(FromFlow("T", f))
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.Unmarshal(data)
		if err != nil {
			t.Fatal(err)
		}
		got := ToItems(d.Content)[0].(canvas.Inline).Object.(*canvas.TableObject).View.Table
		if !reflect.DeepEqual(got.Cells, tbl.Cells) || !reflect.DeepEqual(got.Merges, tbl.Merges) || !reflect.DeepEqual(got.Styles, tbl.Styles) {
			t.Fatalf("%s: got %v %+v %+v, want %v %+v %+v", c.Name(), got.Cells, got.Merges, got.Styles, tbl.Cells, tbl.Merges, tbl.Styles)
		}
	}
	// A file with merges that do not fit the grid loads without them.
	bad := ToItems([]Item{{Table: &TableItem{Rows: []TableRow{{Cells: []string{"a", "b"}}},
		Merges: []TableMerge{{Row: 0, Col: 1, Rows: 2, Cols: 2}, {Row: -1, Col: 0, Rows: 2, Cols: 1}, {Row: 0, Col: 0, Rows: 1, Cols: 1}}}}})
	if m := bad[0].(canvas.Inline).Object.(*canvas.TableObject).View.Table.Merges; len(m) != 0 {
		t.Errorf("bad merges kept: %+v", m)
	}
}

func TestIDsAreUUIDsAndSurviveEveryFormat(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b || !ValidID(a) || a[14] != '4' {
		t.Fatalf("ids %s %s", a, b)
	}
	if ValidID("plan.json") || ValidID(strings.ToUpper(a)) {
		t.Error("ValidID accepts what is not a UUID")
	}
	var d Document
	d.SetID(a)
	d.SetName("Plan")
	d.SetTags([]string{"x"})
	for _, c := range Formats() {
		data, err := c.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		back, err := c.Unmarshal(data)
		if err != nil || back.ID() != a || back.Name() != "Plan" || !back.HasTag("x") {
			t.Errorf("%s: %q %q %v", c.Name(), back.ID(), back.Name(), err)
		}
	}
}
