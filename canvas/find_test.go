package canvas

import (
	"reflect"
	"testing"
)

func TestFindAll(t *testing.T) {
	text := "The cat sat. Concatenate the CAT's hat; the café cat."
	cases := []struct {
		name string
		o    FindOptions
		want []Match
	}{
		{"ignores case by default", FindOptions{Query: "cat"}, []Match{{4, 7}, {16, 19}, {29, 32}, {49, 52}}},
		{"matches case", FindOptions{Query: "cat", MatchCase: true}, []Match{{4, 7}, {16, 19}, {49, 52}}},
		{"whole words", FindOptions{Query: "cat", WholeWord: true}, []Match{{4, 7}, {29, 32}, {49, 52}}},
		{"regex (offsets are in runes: the é before the last match is one)", FindOptions{Query: `\bc\w+`, Regex: true, MatchCase: true}, []Match{{4, 7}, {44, 47}, {49, 52}}},
		{"nothing", FindOptions{Query: "dog"}, nil},
	}
	for _, c := range cases {
		got, err := FindAll(text, c.o)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if _, err := FindAll("x", FindOptions{}); err != ErrEmptyQuery {
		t.Errorf("empty query: %v", err)
	}
	if _, err := FindAll("x", FindOptions{Query: "(", Regex: true}); err == nil {
		t.Error("a bad regex must be an error")
	}
}

func TestFlowFindAndReplacePreserveFormatting(t *testing.T) {
	f := NewTextFlow("", 0, 0, 400, 200)
	f.SetPainter(&fakePainter{})
	f.Items = []Item{Chars{Text: "one "}, Chars{Text: "two", Format: Format{FontWeight: 700}}, Chars{Text: " one two"}}
	n, err := f.ReplaceAll(FindOptions{Query: "two"}, "three")
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if got := f.EditText(); got != "one three one three" {
		t.Fatalf("text %q", got)
	}
	bold := ""
	for _, it := range f.ContentItems() {
		if c, ok := it.(Chars); ok && c.Format.FontWeight == 700 {
			bold += c.Text
		}
	}
	if bold != "three" {
		t.Errorf("bold text %q", bold)
	}
	n, err = f.ReplaceAll(FindOptions{Query: `(\w+) one`, Regex: true, MatchCase: true}, "$1 1")
	if err != nil || n != 1 || f.EditText() != "one three 1 three" {
		t.Errorf("regex replace: %d %v %q", n, err, f.EditText())
	}
}

func TestRegexReplacementKnowsTheContext(t *testing.T) {
	f := NewTextFlow("cat at", 0, 0, 400, 200)
	n, err := f.ReplaceAll(FindOptions{Query: `\Bat`, Regex: true, MatchCase: true}, "[$0]")
	if err != nil || n != 1 || f.EditText() != "c[at] at" {
		t.Errorf("%d %v %q", n, err, f.EditText())
	}
}

func TestSearchHighlightsAreDrawn(t *testing.T) {
	p := &fakePainter{}
	f := NewTextFlow("find the needle in this text", 0, 0, 400, 200)
	f.SetPainter(p)
	ms, _ := f.Find(FindOptions{Query: "needle"})
	if len(ms) != 1 {
		t.Fatal(ms)
	}
	f.SetSearch(ms, 0)
	f.Draw()
	if got, cur := f.Search(); len(got) != 1 || cur != 0 {
		t.Errorf("search %v %d", got, cur)
	}
	f.ClearSearch()
	if got, _ := f.Search(); len(got) != 0 {
		t.Error("ClearSearch")
	}
	f.GotoMatch(ms[0])
	if from, to := f.Selection(); from != ms[0].From || to != ms[0].To {
		t.Errorf("selection %d-%d", from, to)
	}
}

func TestRegexReplacementWithNonASCIIAndWholeWord(t *testing.T) {
	f := NewTextFlow("é café cafés ça", 0, 0, 400, 200)
	n, err := f.ReplaceAll(FindOptions{Query: `\b(caf)é\b`, Regex: true, MatchCase: true}, "${1}E")
	if err != nil || n != 1 || f.EditText() != "é café cafEs ça" {
		t.Fatalf("%d %v %q", n, err, f.EditText())
	}
	g := NewTextFlow("one two one", 0, 0, 400, 200)
	n, err = g.ReplaceAll(FindOptions{Query: `(o)ne`, Regex: true, WholeWord: true, MatchCase: true}, "<$1>")
	if err != nil || n != 2 || g.EditText() != "<o> two <o>" {
		t.Fatalf("%d %v %q", n, err, g.EditText())
	}
}
