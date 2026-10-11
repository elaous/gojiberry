package docdiff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
)

// doc builds a document from paragraphs; a leading "# " makes a heading 1.
func doc(paras ...string) docfile.Document {
	var items []docfile.Item
	for _, p := range paras {
		if t, ok := strings.CutPrefix(p, "# "); ok {
			items = append(items, docfile.Item{Chars: &docfile.Chars{Text: t, Format: docfile.Format{Scale: 2}}})
		} else {
			items = append(items, docfile.Item{Chars: &docfile.Chars{Text: p}})
		}
		items = append(items, docfile.Item{Chars: &docfile.Chars{Text: "\n"}})
	}
	return docfile.Document{Content: items}
}

func ops(cs []Change) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Op.String()[:1])
	}
	return strings.Join(out, "")
}

func TestIdenticalDocumentsAreAllEqual(t *testing.T) {
	cs := Compare(doc("# Title", "one", "two"), doc("# Title", "one", "two"))
	if ops(cs) != "eee" || Summarize(cs).Changed() {
		t.Fatalf("%s", ops(cs))
	}
}

func TestInsertAndDeleteBlocks(t *testing.T) {
	cs := Compare(doc("# T", "alpha", "omega"), doc("# T", "alpha", "beta", "omega"))
	if ops(cs) != "eeie" || cs[2].Text != "beta" {
		t.Fatalf("insert: %s %+v", ops(cs), cs)
	}
	cs = Compare(doc("a", "b", "c"), doc("a", "c"))
	if ops(cs) != "ede" || cs[1].Text != "b" {
		t.Fatalf("delete: %s", ops(cs))
	}
	s := Summarize(cs)
	if s.Deleted != 1 || s.Unchanged != 2 || !s.Changed() {
		t.Fatalf("%+v", s)
	}
}

func TestAReworkedParagraphIsOneEditShowingTheWords(t *testing.T) {
	cs := Compare(doc("The quick brown fox jumps"), doc("The quick red fox leaps"))
	if len(cs) != 1 || cs[0].Op != Edit {
		t.Fatalf("%s", ops(cs))
	}
	got := Describe(cs[0])
	for _, want := range []string{"Edited paragraph", "removed 'brown', 'jumps'", "added 'red', 'leaps'"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	var joined strings.Builder
	for _, r := range cs[0].Runs {
		if r.Op != Delete {
			joined.WriteString(r.Text)
		}
	}
	if strings.TrimSpace(joined.String()) != "The quick red fox leaps" {
		t.Errorf("the newer text from the runs: %q", joined.String())
	}
}

func TestUnrelatedBlocksAreDeleteAndInsertNotEdit(t *testing.T) {
	cs := Compare(doc("completely different sentence here"), doc("nothing alike whatsoever now"))
	if ops(cs) != "di" {
		t.Fatalf("%s", ops(cs))
	}
	if Describe(cs[0]) != "Deleted paragraph: completely different sentence here" || Describe(cs[1]) != "Inserted paragraph: nothing alike whatsoever now" {
		t.Errorf("%q / %q", Describe(cs[0]), Describe(cs[1]))
	}
}

func TestAStructureChangeIsReported(t *testing.T) {
	cs := Compare(doc("Introduction"), doc("# Introduction"))
	if len(cs) != 1 || cs[0].Op != Edit || cs[0].Kind != export.Heading1 || cs[0].OldKind != export.Paragraph {
		t.Fatalf("%+v", cs)
	}
	if got := Describe(cs[0]); !strings.Contains(got, "was paragraph") || !strings.Contains(got, "heading 1") {
		t.Errorf("%q", got)
	}
}

func TestEmptyDocumentsAndDescribeOfEqual(t *testing.T) {
	if cs := Compare(docfile.Document{}, docfile.Document{}); len(cs) != 0 {
		t.Fatalf("%d", len(cs))
	}
	if cs := Compare(docfile.Document{}, doc("new")); ops(cs) != "i" {
		t.Fatalf("%s", ops(cs))
	}
	if Describe(Change{Op: Equal, Text: "x"}) != "" {
		t.Error("an unchanged block says nothing")
	}
}

func TestWordsKeepTheirSpacing(t *testing.T) {
	for _, s := range []string{"", "one", "one two", "  lead and  gaps  ", "a\nb\tc"} {
		if got := strings.Join(words(s), ""); got != s {
			t.Errorf("%q -> %q", s, got)
		}
	}
}

func TestHugeMiddleFallsBackToDeleteAndInsert(t *testing.T) {
	var a, b []string
	for i := 0; i < 2100; i++ {
		a = append(a, fmt.Sprintf("old%d", i))
		b = append(b, fmt.Sprintf("new%d", i))
	}
	cs := Compare(doc(a...), doc(b...))
	s := Summarize(cs)
	if s.Deleted != len(a) || s.Inserted != len(b) {
		t.Fatalf("%+v", s)
	}
}
