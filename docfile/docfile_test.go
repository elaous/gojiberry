package docfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
)

func sample() Document {
	items := []canvas.Item{
		canvas.ParaFormat{Style: "Heading 1", Align: canvas.AlignCenter, List: canvas.ListStyle{Kind: canvas.ListOrdered, Level: 1, Start: 3, Checked: true}},
		canvas.Chars{Text: "Hello ", Format: canvas.Format{FontFamily: "Lato", FontWeight: 700, Color: "#123", Case: canvas.CaseSmallCaps, Link: "https://x.test"}},
		canvas.Chars{Text: "world\n"},
		canvas.ColumnConfig{Widths: []float64{100, 50.5}, Gap: 8},
		canvas.Break{}, // the zero value must survive
		canvas.Break{Kind: canvas.BreakPage},
	}
	d := Document{Title: "T", Updated: 42, Content: FromItems(items),
		Styles: FromStyles([]canvas.StyleDef{{Name: "Loud", Format: canvas.Format{Underline: true}, Para: canvas.ParaFormat{SpaceAfter: 6}, Paragraph: true}}),
		Fonts:  []FontRef{{Family: "Lato", Source: "Google", Variants: []FontVariant{{Weight: 700, URL: "u", Key: "fonts/Lato/700-n"}}}}}
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
