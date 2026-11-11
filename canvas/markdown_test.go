package canvas

import (
	"reflect"
	"strings"
	"testing"
)

type seg struct {
	Text string
	F    Format
}

func segs(items []Item) []seg {
	var out []seg
	for _, it := range items {
		if c, ok := it.(Chars); ok {
			out = append(out, seg{c.Text, c.Format})
		}
	}
	return out
}

func plainOf(items []Item) string {
	var b strings.Builder
	for _, it := range items {
		switch it := it.(type) {
		case Chars:
			b.WriteString(it.Text)
		case Inline:
			b.WriteString("<obj>")
		case Break:
			b.WriteString("<" + it.Kind.String() + " break>")
		case ColumnConfig:
			b.WriteString("<columns>")
		}
	}
	return b.String()
}

func TestMarkdownInlineFormats(t *testing.T) {
	got := segs(Markdown{}.inline("a **b** *c* ***d*** ~~e~~ ++f++ ==g== x^2^ H~2~O `h` i", Format{}))
	want := []seg{
		{"a ", Format{}},
		{"b", Format{FontWeight: 700}},
		{" ", Format{}},
		{"c", Format{FontStyle: StyleItalic}},
		{" ", Format{}},
		{"d", Format{FontWeight: 700, FontStyle: StyleItalic}},
		{" ", Format{}},
		{"e", Format{Strike: true}},
		{" ", Format{}},
		{"f", Format{Underline: true}},
		{" ", Format{}},
		{"g", Format{Background: "#fff3a3"}},
		{" x", Format{}},
		{"2", Format{Superscript: true}},
		{" H", Format{}},
		{"2", Format{Subscript: true}},
		{"O ", Format{}},
		{"h", Format{FontFamily: "monospace", Background: "#eceef1"}},
		{" i", Format{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%+v\nwant\n%+v", got, want)
	}
}

func TestMarkdownNestedAndUnderscoreBold(t *testing.T) {
	got := segs(Markdown{}.inline("__bold *and italic* text__", Format{}))
	want := []seg{
		{"bold ", Format{FontWeight: 700}},
		{"and italic", Format{FontWeight: 700, FontStyle: StyleItalic}},
		{" text", Format{FontWeight: 700}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}

func TestMarkdownLiteralDelimiters(t *testing.T) {
	for _, s := range []string{
		"2 * 3 * 4",           // spaces around: not emphasis
		"snake_case_name",     // intraword underscores
		"a*b",                 // no closer
		"price is 5^ today",   // no closer
		"x ~ y",               // spaces
		"1 ^2 3^ 4",           // superscript can't contain spaces
		"unclosed **bold",     // no closer
		"== not highlighted",  // nothing after
		`escaped \*star\* ok`, // escapes
	} {
		items := Markdown{}.inline(s, Format{})
		for _, sg := range segs(items) {
			if !sg.F.IsZero() {
				t.Errorf("%q: unexpected formatting %+v on %q", s, sg.F, sg.Text)
			}
		}
		want := strings.ReplaceAll(s, `\`, "")
		if got := plainOf(items); got != want {
			t.Errorf("%q rendered as %q, want %q", s, got, want)
		}
	}
}

func TestMarkdownCodeSpans(t *testing.T) {
	got := segs(Markdown{}.inline("use ``a ` b`` and `**not bold**`", Format{}))
	if len(got) != 4 || got[1].Text != "a ` b" || got[3].Text != "**not bold**" || got[3].F.FontFamily != "monospace" || got[3].F.FontWeight != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestMarkdownLinksAndImages(t *testing.T) {
	got := segs(Markdown{}.inline("see [the **docs**](http://x.y \"title\") now", Format{}))
	want := []seg{
		{"see ", Format{}},
		{"the ", Format{Link: "http://x.y"}},
		{"docs", Format{Link: "http://x.y", FontWeight: 700}},
		{" now", Format{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	got = segs(Markdown{}.inline("![a cat](cat.png)", Format{}))
	if len(got) != 1 || got[0].Text != "[a cat]" || got[0].F.FontStyle != StyleItalic {
		t.Fatalf("got %+v", got)
	}
	var gotURL, gotAlt string
	obj := &Rule{}
	m := Markdown{Images: func(url, alt string) InlineObject { gotURL, gotAlt = url, alt; return obj }}
	items := m.inline("x ![a cat](cat.png) y", Format{})
	if gotURL != "cat.png" || gotAlt != "a cat" || len(items) != 3 {
		t.Fatalf("url=%q alt=%q items=%+v", gotURL, gotAlt, items)
	}
	if in, ok := items[1].(Inline); !ok || in.Object != obj || in.Block {
		t.Fatalf("items[1] = %+v", items[1])
	}
	m.Images = func(string, string) InlineObject { return nil }
	if got := plainOf(m.inline("![alt](u)", Format{})); got != "[alt]" {
		t.Fatalf("got %q", got)
	}
	if got := plainOf(Markdown{}.inline("[just brackets] and [x](", Format{})); got != "[just brackets] and [x](" {
		t.Fatalf("got %q", got)
	}
}

func TestMarkdownHeadings(t *testing.T) {
	items := Markdown{}.Transform("# Title\n\n### Third ###\n\nbody")
	got := segs(items)
	if got[0].Text != "Title" || got[0].F.Scale != 2 || got[0].F.FontWeight != 700 {
		t.Fatalf("h1 = %+v", got[0])
	}
	if got[2].Text != "Third" || got[2].F.Scale != 1.35 {
		t.Fatalf("h3 = %+v", got[2])
	}
	custom := Markdown{HeadingScale: [6]float64{3}}
	if g := segs(custom.Transform("# A")); g[0].F.Scale != 3 {
		t.Fatalf("custom scale = %+v", g[0])
	}
	if plainOf(items) != "Title\n"+"Third\n"+"body" {
		t.Fatalf("plain = %q", plainOf(items))
	}
}

func TestMarkdownParagraphs(t *testing.T) {
	got := plainOf(Markdown{}.Transform("one\ntwo\n\nthree  \nfour\\\nfive\n"))
	if got != "one two\n\nthree\nfour\nfive" {
		t.Fatalf("got %q", got)
	}
}

func paraFormats(items []Item) []ParaFormat {
	var out []ParaFormat
	for _, it := range items {
		if p, ok := it.(ParaFormat); ok {
			out = append(out, p)
		}
	}
	return out
}

func TestMarkdownLists(t *testing.T) {
	src := "- a\n- b\n  - nested\n    - deeper\n- c\n\n1. one\n2. two\n   continued\n\n- [ ] todo\n- [x] done"
	items := Markdown{}.Transform(src)
	if got := plainOf(items); got != "a\nb\nnested\ndeeper\nc\n\none\ntwo continued\n\ntodo\ndone" {
		t.Fatalf("text = %q", got)
	}
	var kinds []string
	for _, p := range paraFormats(items) {
		k := map[ListKind]string{ListBullet: "b", ListOrdered: "o", ListCheck: "c"}[p.List.Kind]
		if p.List.Checked {
			k = "C"
		}
		kinds = append(kinds, k+itoaTest(p.List.Level))
	}
	if strings.Join(kinds, " ") != "b0 b0 b1 b2 b0 o0 o0 c0 C0" {
		t.Fatalf("list items = %v", kinds)
	}
	f, _ := newFlow("", 600, 600)
	f.Items = items
	var markers []string
	for _, ln := range f.Layout().Lines {
		if ln.Marker != nil {
			markers = append(markers, ln.Marker.Text)
		}
	}
	if strings.Join(markers, " ") != "• • ◦ ▪ • 1. 2. ☐ ☑" {
		t.Fatalf("markers = %v", markers)
	}
}

func itoaTest(n int) string { return string(rune('0' + n)) }

func TestMarkdownOrderedListStartsAtItsFirstNumber(t *testing.T) {
	items := Markdown{}.Transform("3. c\n4. d\n\n1) x")
	ps := paraFormats(items)
	if len(ps) != 3 || ps[0].List.Start != 3 || ps[1].List.Start != 0 || ps[2].List.Start != 0 || ps[2].List.Marker != "{n})" {
		t.Fatalf("formats = %+v", ps)
	}
	f, _ := newFlow("", 600, 600)
	f.Items = items
	var markers []string
	for _, ln := range f.Layout().Lines {
		if ln.Marker != nil {
			markers = append(markers, ln.Marker.Text)
		}
	}
	if strings.Join(markers, " ") != "3. 4. 1)" {
		t.Fatalf("markers = %v", markers)
	}
}

func TestMarkdownQuoteAndCode(t *testing.T) {
	items := Markdown{}.Transform("> quoted *text*\n> more\n\n```go\nfunc x() {}\n  indented\n```\nafter")
	if plainOf(items) != "quoted text\nmore\n\nfunc x() {}\n  indented\n\nafter" {
		t.Fatalf("plain = %q", plainOf(items))
	}
	if ps := paraFormats(items); len(ps) != 2 || ps[0].QuoteBar != "#c4c7cd" || ps[0].IndentLeft != 16 {
		t.Fatalf("quote formats = %+v", ps)
	}
	var sawQuote, sawCode bool
	for _, s := range segs(items) {
		if s.Text == "quoted " && s.F.FontStyle == StyleItalic && s.F.Color == "#5b616b" {
			sawQuote = true
		}
		if s.Text == "func x() {}\n" && s.F.FontFamily == "monospace" && s.F.Background != "" {
			sawCode = true
		}
	}
	if !sawQuote || !sawCode {
		t.Fatalf("quote=%v code=%v in %+v", sawQuote, sawCode, segs(items))
	}
}

func TestMarkdownTables(t *testing.T) {
	src := "| Name | **Qty** | Note \\| pipe |\n|:--|--:|---|\n| apple | 3 | `x` |\n| pear | 10 | |\n\nafter"
	items := Markdown{}.Transform(src)
	var tbl *TableObject
	for _, it := range items {
		if in, ok := it.(Inline); ok {
			if !in.Block {
				t.Fatal("a table is a block object")
			}
			tbl = in.Object.(*TableObject)
		}
	}
	if tbl == nil {
		t.Fatalf("no table in %v", plainOf(items))
	}
	want := [][]string{{"Name", "Qty", "Note | pipe"}, {"apple", "3", "x"}, {"pear", "10", ""}}
	if !reflect.DeepEqual(tbl.View.Table.Cells, want) {
		t.Fatalf("cells = %v", tbl.View.Table.Cells)
	}
	if plainOf(items) != "<obj>\nafter" {
		t.Fatalf("plain = %q", plainOf(items))
	}
	if got := plainOf(Markdown{}.Transform("a | b")); got != "a | b" {
		t.Fatalf("got %q", got)
	}
}

func TestMarkdownRuleAndDirectives(t *testing.T) {
	items := Markdown{}.Transform("above\n\n---\n\\columns 2 gap=30\ntext\n\\columnbreak\nmore\n\\columns 3\nx\n\\columns 1\ny\n\\pagebreak\nend")
	if plainOf(items) != "above\n\n<obj>\n<columns>text\n<column break>more\n<columns>x\n<columns>y\n<page break>end" {
		t.Fatalf("plain = %q", plainOf(items))
	}
	var cfgs []ColumnConfig
	for _, it := range items {
		if c, ok := it.(ColumnConfig); ok {
			cfgs = append(cfgs, c)
		}
	}
	if !reflect.DeepEqual(cfgs, []ColumnConfig{{Count: 2, Gap: 30}, {Count: 3, Gap: 24}, {}}) {
		t.Fatalf("configs = %+v", cfgs)
	}
	var rule *Rule
	for _, it := range items {
		if in, ok := it.(Inline); ok {
			rule, _ = in.Object.(*Rule)
			if !in.Block {
				t.Fatal("a rule is a block object")
			}
		}
	}
	if rule == nil {
		t.Fatal("no rule object")
	}
}

func TestMarkdownCRLFAndEmpty(t *testing.T) {
	if got := plainOf((Markdown{}).Transform("a\r\nb\r\n\r\nc")); got != "a b\n\nc" {
		t.Fatalf("got %q", got)
	}
	m := Markdown{}
	if got := m.Transform(""); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
	if got := m.Transform("\n\n\n"); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestMarkdownWriteBackTablesRulesAndImages(t *testing.T) {
	src := "| Name | Note \\| pipe |\n| --- | --- |\n| apple | 3 |\n\n---\nafter"
	items := Markdown{}.Transform(src)
	got := MarkdownFromItems(items)
	back := MarkdownFromItems(Markdown{}.Transform(got))
	if back != got {
		t.Fatalf("not stable:\n%s\n--\n%s", got, back)
	}
	for _, want := range []string{"| Name | Note \\| pipe |", "| --- | --- |", "| apple | 3 |", "---\nafter"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	img := &Image{Src: urlFake{}, Alt: "a [cat]"}
	md := MarkdownFromItems([]Item{Chars{Text: "see "}, Inline{Object: img}, Chars{Text: " here"}})
	if md != "see ![a (cat)](http://x/y.png) here" {
		t.Errorf("image: %q", md)
	}
	if md := MarkdownFromItems([]Item{Inline{Object: &Image{Src: fakeImage{}}}}); md != "" {
		t.Errorf("a picture with no address has no form: %q", md)
	}
}

type urlFake struct{ fakeImage }

func (urlFake) URL() string { return "http://x/y.png" }

func TestMarkdownWritesAPicturesLegendUnderIt(t *testing.T) {
	img := &Image{Src: urlFake{}, Alt: "a cat", Legend: "Figure 1: a *grey* cat"}
	md := MarkdownFromItems([]Item{Chars{Text: "see "}, Inline{Object: img}})
	if md != "see ![a cat](http://x/y.png)  \n*Figure 1: a \\*grey\\* cat*" {
		t.Errorf("legend: %q", md)
	}
}
