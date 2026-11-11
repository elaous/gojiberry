package export

import (
	"strings"
	"testing"
)

func seg(text string) []Segment { return []Segment{{Text: text}} }

func cell(r, c, rows, cols int, text string) Block {
	return Block{Kind: TableCell, TableRow: r, TableCol: c, TableRows: rows, TableCols: cols, Segments: seg(text)}
}

func TestMarkdownBlocksAndInlineFormats(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Heading1, Segments: seg("Title")},
		{Kind: Heading2, Segments: seg("Part")},
		{Kind: Heading3, Segments: seg("Sub")},
		{Kind: Paragraph, Segments: []Segment{
			{Text: "plain "}, {Text: "bold", Format: Format{Bold: true}}, {Text: " "}, {Text: "both", Format: Format{Bold: true, Italic: true}},
			{SoftBreak: true}, {Text: "u", Format: Format{Underline: true}}, {Text: "s", Format: Format{Strikethrough: true}},
			{Text: "x", Format: Format{Superscript: true}}, {Text: "y", Format: Format{Subscript: true}},
			{Text: "link", Format: Format{Href: "https://example.com"}}, {Text: "bad", Format: Format{Href: "javascript:alert(1)"}},
			{Text: ""}, {Text: " 2*3 [a] _b_ `c` \\"},
		}},
		{Kind: BulletItem, Level: 1, Segments: seg("nested")},
		{Kind: NumberedItem, NumberedIndex: 3, Segments: seg("third")},
		{Kind: Quote, Segments: []Segment{{Text: "wise"}, {SoftBreak: true}, {Text: "words"}}},
		{Kind: Code, Segments: seg("a *b* [c]\n  d")},
	}}
	out, err := Markdown{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"# Title\n\n", "## Part\n\n", "### Sub\n\n",
		"plain **bold** ***both***", "  \n", "<u>u</u>", "~~s~~", "<sup>x</sup>", "<sub>y</sub>", "[link](https://example.com)",
		` 2\*3 \[a\] \_b\_ \` + "`c\\` \\\\",
		"  - nested\n\n", "3. third\n\n", "> wise  \n> words\n\n", "```\na *b* [c]\n  d\n```\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "javascript:") {
		t.Error("an unsafe link must not be written as a link")
	}
	if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("ends with exactly one newline: %q", got[len(got)-4:])
	}
}

func TestMarkdownImageAndTable(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Image, ImageSrc: "data:image/png;base64,AAAA", ImageCaption: "A *chart*", ImageAlt: "Sales by year"},
		{Kind: Image, ImageSrc: "data:image/png;base64,BBBB", ImageCaption: "Only a caption"},
		cell(0, 0, 2, 2, "Name"), cell(0, 1, 2, 2, "Qty|x"),
		{Kind: TableCell, TableRow: 1, TableCol: 0, TableRows: 2, TableCols: 2, Segments: []Segment{{Text: "a"}, {SoftBreak: true}, {Text: "b", Format: Format{Bold: true}}}},
		{Kind: TableCell, TableRow: 1, TableCol: 1, TableRows: 2, TableCols: 2, TableCovered: true, Segments: seg("hidden")},
		{Kind: Paragraph, Segments: seg("after")},
	}}
	out, _ := Markdown{}.Export(snap)
	got := string(out)
	for _, want := range []string{
		"![Sales by year](data:image/png;base64,AAAA)\n\n*A \\*chart\\**\n\n",
		"![Only a caption](data:image/png;base64,BBBB)\n\n",
		"| Name | Qty\\|x |\n| --- | --- |\n| a<br>**b** |  |\n\n", "after\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "hidden") {
		t.Error("a covered cell repeats nothing")
	}
	cut, _ := Markdown{}.Export(Snapshot{Blocks: []Block{cell(0, 0, 2, 2, "x")}})
	if !strings.Contains(string(cut), "[Table]") {
		t.Errorf("a cut-off table: %q", cut)
	}
}

func TestPlainTextBlocksImagesAndTables(t *testing.T) {
	snap := Snapshot{Blocks: []Block{
		{Kind: Heading1, Segments: seg("Title")},
		{Kind: Paragraph, Segments: []Segment{{Text: "one"}, {SoftBreak: true}, {Text: "two", Format: Format{Bold: true}}}},
		{Kind: Code, Segments: seg("x := 1")},
		{Kind: Image, ImageCaption: "Cap"},
		{Kind: Image, ImageAlt: "A cat"},
		{Kind: Image},
		cell(0, 0, 2, 2, "ab"), cell(0, 1, 2, 2, "c"),
		{Kind: TableCell, TableRow: 1, TableCol: 0, TableRows: 2, TableCols: 2, Segments: []Segment{{Text: "d"}, {SoftBreak: true}, {Text: "e"}}},
		{Kind: TableCell, TableRow: 1, TableCol: 1, TableRows: 2, TableCols: 2, TableCovered: true, Segments: seg("gone")},
		{Kind: Paragraph, Segments: seg("end")},
	}}
	out, err := PlainText{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"Title\n\n", "one\ntwo\n\n", "x := 1\n\n", "[Image] Cap\n\n", "[Image: A cat]\n\n", "[Image]\n\n",
		"+-------+---+\n| ab    | c |\n+-------+---+\n| d / e |   |\n+-------+---+\n\nend\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "gone") {
		t.Error("a covered cell is empty")
	}
	cut, _ := PlainText{}.Export(Snapshot{Blocks: []Block{cell(0, 0, 3, 3, "x")}})
	if !strings.Contains(string(cut), "[Table]") {
		t.Errorf("a cut-off table: %q", cut)
	}
	empty, _ := PlainText{}.Export(Snapshot{})
	if string(empty) != "\n" {
		t.Errorf("an empty document: %q", empty)
	}
}
