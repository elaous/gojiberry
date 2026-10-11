package components

import (
	"strings"
	"testing"
)

func chunkTexts(cs []SpeechChunk) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return strings.Join(out, "|")
}

func TestSpeechChunksSentencesAndPauses(t *testing.T) {
	got := SpeechChunks([]A11yBlock{
		{Kind: A11yHeading, Level: 1, Text: "Title"},
		{Kind: A11yParagraph, Text: "One. Two is 3.5 long! Three?"},
		{Kind: A11yListItem, Text: "item"},
	}, "en")
	if g, want := chunkTexts(got), "Title.|One.|Two is 3.5 long!|Three?|item."; g != want {
		t.Fatalf("got %q want %q", g, want)
	}
	if got[0].Block != 0 || got[4].Block != 2 || got[1].Lang != "en" {
		t.Fatalf("blocks/lang wrong: %+v", got)
	}
}

func TestSpeechChunksLanguageRuns(t *testing.T) {
	got := SpeechChunks([]A11yBlock{{Kind: A11yParagraph, Text: "Say bonjour now", Runs: []A11yRun{{Text: "Say "}, {Text: "bonjour", Lang: "fr"}, {Text: " now"}}}}, "en")
	if len(got) != 3 || got[1].Lang != "fr" || got[0].Lang != "en" || got[2].Lang != "en" {
		t.Fatalf("got %+v", got)
	}
}

func TestSpeechChunksLongTextAndTables(t *testing.T) {
	got := SpeechChunks([]A11yBlock{{Kind: A11yParagraph, Text: strings.Repeat("word ", 150)}, {Kind: A11yTableRow, Cells: []string{"a", "", "b"}}}, "")
	for _, c := range got {
		if len([]rune(c.Text)) > MaxSpeechChunk {
			t.Fatalf("piece too long: %d", len(c.Text))
		}
	}
	if last := got[len(got)-1].Text; last != "a, b" {
		t.Fatalf("table row = %q", last)
	}
	if n := len(SpeechChunksOfText("  ", "en")); n != 0 {
		t.Fatalf("empty text gave %d pieces", n)
	}
}
