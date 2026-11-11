package read

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/components"
)

func chunkTexts(cs []Chunk) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return strings.Join(out, "|")
}

func TestChunksSentencesAndPauses(t *testing.T) {
	got := Chunks([]components.A11yBlock{
		{Kind: components.A11yHeading, Level: 1, Text: "Title"},
		{Kind: components.A11yParagraph, Text: "One. Two is 3.5 long! Three?"},
		{Kind: components.A11yListItem, Text: "item"},
	}, "en")
	if g, want := chunkTexts(got), "Title.|One.|Two is 3.5 long!|Three?|item."; g != want {
		t.Fatalf("got %q want %q", g, want)
	}
	if got[0].Block != 0 || got[4].Block != 2 || got[1].Lang != "en" {
		t.Fatalf("blocks/lang wrong: %+v", got)
	}
}

func TestChunksLanguageRuns(t *testing.T) {
	got := Chunks([]components.A11yBlock{{Kind: components.A11yParagraph, Text: "Say bonjour now", Runs: []components.A11yRun{{Text: "Say "}, {Text: "bonjour", Lang: "fr"}, {Text: " now"}}}}, "en")
	if len(got) != 3 || got[1].Lang != "fr" || got[0].Lang != "en" || got[2].Lang != "en" {
		t.Fatalf("got %+v", got)
	}
}

func TestChunksLongTextAndTables(t *testing.T) {
	got := Chunks([]components.A11yBlock{{Kind: components.A11yParagraph, Text: strings.Repeat("word ", 150)}, {Kind: components.A11yTableRow, Cells: []string{"a", "", "b"}}}, "")
	for _, c := range got {
		if len([]rune(c.Text)) > MaxChunk {
			t.Fatalf("piece too long: %d", len(c.Text))
		}
	}
	if last := got[len(got)-1].Text; last != "a, b" {
		t.Fatalf("table row = %q", last)
	}
	if n := len(OfText("  ", "en", -1)); n != 0 {
		t.Fatalf("empty text gave %d pieces", n)
	}
}

func TestReadingSkipsTextSuggestedToBeRemoved(t *testing.T) {
	got := Chunks([]components.A11yBlock{{Kind: components.A11yParagraph, Text: "the red fox", Runs: []components.A11yRun{
		{Text: "the "}, {Text: "brown ", Change: "del", Author: "Ana"}, {Text: "red ", Change: "ins", Author: "Ana"}, {Text: "fox."}}}}, "en")
	if g := chunkTexts(got); g != "the red fox." {
		t.Fatalf("%q", g)
	}
	if strings.Contains(chunkTexts(got), "brown") {
		t.Error("struck text is not read")
	}
}

func TestChunksKnowTheirPlaceInTheStream(t *testing.T) {
	got := Chunks([]components.A11yBlock{
		{Kind: components.A11yHeading, Level: 1, Text: "Title", Mapped: true, Start: 0},
		{Kind: components.A11yParagraph, Text: "One.  Two is long.", Mapped: true, Start: 6},
		{Kind: components.A11yTableRow, Cells: []string{"a", "b"}},
	}, "en")
	if g := chunkTexts(got); g != "Title.|One.|Two is long.|a, b" {
		t.Fatalf("%q", g)
	}
	check := func(i, from, to int) {
		f, e, ok := got[i].Span()
		if !ok || f != from || e != to {
			t.Errorf("piece %d (%q) spans %d..%d (%v), want %d..%d", i, got[i].Text, f, e, ok, from, to)
		}
	}
	check(0, 0, 5)
	check(1, 6, 10)
	check(2, 12, 24)
	if _, _, ok := got[3].Span(); ok {
		t.Error("a table row has no place in the stream")
	}
	if f, e, ok := got[2].WordAt(4, 2); !ok || f != 16 || e != 18 { // "is"
		t.Errorf("WordAt: %d..%d %v", f, e, ok)
	}
	if f, e, ok := got[2].WordAt(7, 0); !ok || f != 19 || e != 24 { // no length: up to the space, "long."
		t.Errorf("WordAt without a length: %d..%d %v", f, e, ok)
	}
	if _, _, ok := got[2].WordAt(99, 1); ok {
		t.Error("an index past the text")
	}
}

func TestChunkPlacesSkipSuggestedRemovalsAndCountMarksOnce(t *testing.T) {
	got := Chunks([]components.A11yBlock{{Kind: components.A11yParagraph, Mapped: true, Start: 100, Text: "the brown red fox12 end", Runs: []components.A11yRun{
		{Text: "the "}, {Text: "brown ", Change: "del"}, {Text: "red fox"}, {Text: "12", Stream: 1}, {Text: " end"}}}}, "en")
	if len(got) != 1 || got[0].Text != "the red fox12 end." && got[0].Text != "the red fox12 end" {
		t.Fatalf("%+v", got)
	}
	offs := got[0].Offsets
	if offs[0] != 100 || offs[4] != 110 || offs[len(offs)-4] != 118 {
		t.Errorf("offsets %v", offs)
	}
}
