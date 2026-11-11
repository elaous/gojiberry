package a11ycheck

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func table(rows ...[]string) *docfile.TableItem {
	t := &docfile.TableItem{}
	for _, r := range rows {
		t.Rows = append(t.Rows, docfile.TableRow{Cells: r})
	}
	return t
}

func TestTableHeaderRow(t *testing.T) {
	merged := func(cols int) *docfile.TableItem {
		x := table([]string{"Sales", "", ""}, []string{"a", "b", "c"})
		x.Merges = []docfile.TableMerge{{Row: 0, Col: 0, Rows: 1, Cols: cols}}
		return x
	}
	for _, c := range []struct {
		name string
		tbl  *docfile.TableItem
		rule string // "" for no finding
		sev  Severity
		msg  string
	}{
		{"named columns", table([]string{"Name", "Age"}, []string{"Ana", "31"}), "", 0, ""},
		{"no text in the first row", table([]string{"", " "}, []string{"Ana", "31"}), RuleTableHeaderMissing, Error, "no header row"},
		{"empty header cells", table([]string{"Name", "", "City", ""}, []string{"Ana", "31", "Oslo", "x"}), RuleTableHeaderEmpty, Warning, "column 2, 4"},
		{"one row only is left alone", table([]string{"", ""}), "", 0, ""},
		{"a merged title over the header is not empty", merged(3), "", 0, ""},
		{"a covered cell does not count but a real empty one does", merged(2), RuleTableHeaderEmpty, Warning, "column 3"},
	} {
		doc := docfile.Document{Content: []docfile.Item{chars("Intro\n", docfile.Format{}), {Table: c.tbl}}}
		fs := Check(doc, Options{})
		if c.rule == "" {
			if len(fs) != 0 {
				t.Errorf("%s: findings %+v", c.name, fs)
			}
			continue
		}
		if len(fs) != 1 || fs[0].Rule != c.rule || fs[0].Severity != c.sev || !strings.Contains(fs[0].Message, c.msg) || fs[0].Offset != 6 || fs[0].Item != 1 || fs[0].Fix == "" {
			t.Errorf("%s: findings %+v", c.name, fs)
		}
	}
}

func para(text string, p docfile.Para) []docfile.Item {
	return []docfile.Item{{Para: &p}, chars(text+"\n", docfile.Format{})}
}

func TestTypedMarkersAreNotAList(t *testing.T) {
	var items []docfile.Item
	add := func(text string, p docfile.Para) { items = append(items, para(text, p)...) }
	add("Shopping", docfile.Para{}) // offsets 0 to 8
	add("• milk", docfile.Para{})   // starts at 9
	add("", docfile.Para{})         // a blank line does not end the run
	add("• eggs", docfile.Para{})
	add("• bread", docfile.Para{})
	add("Then:", docfile.Para{})
	add("1. wash", docfile.Para{})
	add("2) dry", docfile.Para{})
	add("- a lone dash line", docfile.Para{})
	add("- real", docfile.Para{List: docfile.List{Kind: 1}}) // real list items are fine
	add("- real too", docfile.Para{List: docfile.List{Kind: 1}})
	fs := Check(docfile.Document{Content: items}, Options{})
	if rules(fs) != "list-fake list-fake" {
		t.Fatalf("findings: %+v", fs)
	}
	if fs[0].Offset != 9 || !strings.Contains(fs[0].Message, "3 paragraphs") || !strings.Contains(fs[0].Message, "bullets") || fs[0].Text != "• milk" || fs[0].Severity != Warning || fs[0].Fix == "" {
		t.Errorf("bullets: %+v", fs[0])
	}
	if !strings.Contains(fs[1].Message, "2 paragraphs") || !strings.Contains(fs[1].Message, "numbers") || fs[1].Text != "1. wash" {
		t.Errorf("numbers: %+v", fs[1])
	}
	// one-click fix: paragraphs, list kind and marker runes.
	f := fs[0]
	if f.Action != ActionToList || f.Arg != 0 || f.Length != 22 { // from "• milk" to the end of "• bread"
		t.Errorf("bullets fix: %+v", f)
	}
	if want := [][2]int{{9, 2}, {17, 2}, {24, 2}}; !reflect.DeepEqual(f.Spans, want) {
		t.Errorf("marker spans %v, want %v", f.Spans, want)
	}
	if f := fs[1]; f.Arg != 1 || len(f.Spans) != 2 || f.Spans[0][1] != 3 || f.Spans[1][1] != 3 {
		t.Errorf("numbers fix: %+v", f)
	}
	// a table between typed lines ends the run.
	items = nil
	add("- one", docfile.Para{})
	items = append(items, docfile.Item{Table: table([]string{"a"}, []string{"b"})})
	add("- two", docfile.Para{})
	if fs := Check(docfile.Document{Content: items}, Options{}); len(fs) != 0 {
		t.Errorf("a table between: %+v", fs)
	}
}

func TestEmptyHeadingCanBecomeAParagraph(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		chars("Intro\n", docfile.Format{}),
		heading(2, "  "), chars("\n", docfile.Format{}),
		{Para: &docfile.Para{Style: "Heading 1"}}, chars("\n", docfile.Format{}),
	}}
	fs := Check(doc, Options{})
	if rules(fs) != "heading-empty heading-empty" {
		t.Fatalf("findings: %+v", fs)
	}
	if fs[0].Action != ActionToParagraph || fs[0].Length != 2 || fs[0].Offset != 6 {
		t.Errorf("blank text at heading size: %+v", fs[0])
	}
	if fs[1].Action != ActionToParagraph || fs[1].Length != 0 || fs[1].Offset != 9 {
		t.Errorf("a heading style with no text: %+v", fs[1])
	}
}

func TestOperatorMinimumOfTheGate(t *testing.T) {
	for _, c := range []struct {
		person, operator, want GateMode
		override               bool
	}{
		{GateAllow, GateAllow, GateAllow, true},
		{GateAllow, GateWarn, GateWarn, true},
		{GateAllow, GateBlock, GateBlock, false},
		{GateWarn, GateBlock, GateBlock, false},
		{GateBlock, GateWarn, GateBlock, true}, // the person may go higher
		{GateWarn, GateAllow, GateWarn, true},
	} {
		if got := c.person.AtLeast(c.operator); got != c.want || OverrideAllowed(c.operator) != c.override {
			t.Errorf("person %q operator %q: %q override %v, want %q %v", c.person, c.operator, got, OverrideAllowed(c.operator), c.want, c.override)
		}
	}
	// Block minimum refuses errors, and the person's override does not count.
	errs := []Finding{{Severity: Error}}
	override := true // the person pressed "export anyway"
	if allow, _ := GateAllow.AtLeast(GateBlock).Decide(errs, override && OverrideAllowed(GateBlock)); allow {
		t.Error("an operator block was lowered")
	}
	// A warn minimum keeps the person's block gate and its override.
	if allow, _ := GateBlock.AtLeast(GateWarn).Decide(errs, false); allow {
		t.Error("the person's block did not stop the export")
	}
	if allow, _ := GateBlock.AtLeast(GateWarn).Decide(errs, override && OverrideAllowed(GateWarn)); !allow {
		t.Error("the person's override was refused under a warn minimum")
	}
	for in, want := range map[string]GateMode{"": GateAllow, "none": GateAllow, " Warn ": GateWarn, "BLOCK": GateBlock} {
		if m, ok := ParseGateMode(in); !ok || m != want {
			t.Errorf("ParseGateMode(%q) = %q %v", in, m, ok)
		}
	}
	if _, ok := ParseGateMode("blok"); ok {
		t.Error("a typo is accepted")
	}
}
