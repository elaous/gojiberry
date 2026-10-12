package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
)

// A block says where it starts in the editor's text stream, so the accessible copy can map the caret
// to its text: whatever sits between (pictures, rules, tables and footnote marks are one rune each),
// the runes of the stream from Start are the block's own text.
func TestBlocksSayWhereTheyStartInTheTextStream(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Para: &docfile.Para{Style: "Heading 1"}},
		{Chars: &docfile.Chars{Text: "Title\n"}},
		{Para: &docfile.Para{}},
		{Chars: &docfile.Chars{Text: "First line one\nSecond "}},
		{Chars: &docfile.Chars{Text: "bold", Format: docfile.Format{FontWeight: 700}}},
		{Chars: &docfile.Chars{Text: " end\n"}},
		{Image: &docfile.ImageItem{URL: "x.png", Width: 10, Height: 10}},
		{Chars: &docfile.Chars{Text: "\nAfter the picture\n"}},
		{Rule: &docfile.RuleItem{}},
		{Chars: &docfile.Chars{Text: "\nAfter the rule\n"}},
		{Table: &docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"a", "b"}}, {Cells: []string{"c", "d"}}}}},
		{Chars: &docfile.Chars{Text: "\nLast"}},
	}}
	var f canvas.TextFlow
	f.Items = docfile.ToItems(doc.Content)
	stream := []rune(f.EditText())
	snap := FromDocument(doc)
	checked := 0
	for _, b := range snap.Blocks {
		var text string
		for _, s := range b.Segments {
			text += s.Text
		}
		switch {
		case b.Kind == Image || b.Kind == TableCell:
			if stream[b.Start] != '￼' {
				t.Errorf("block of kind %v starts at %d, not at an object (%q)", b.Kind, b.Start, string(stream[b.Start]))
			}
		case strings.HasPrefix(text, "──"):
			// a rule is drawn as a line of dashes
		default:
			if got := string(stream[b.Start : b.Start+len([]rune(text))]); got != text {
				t.Errorf("block %q starts at %d, where the stream has %q", text, b.Start, got)
			}
			checked++
		}
	}
	if checked < 6 {
		t.Fatalf("only %d text blocks were checked: %+v", checked, snap.Blocks)
	}
}
