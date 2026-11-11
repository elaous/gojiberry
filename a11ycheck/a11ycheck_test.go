package a11ycheck

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func chars(text string, f docfile.Format) docfile.Item {
	return docfile.Item{Chars: &docfile.Chars{Text: text, Format: f}}
}

func heading(level int, text string) docfile.Item {
	return chars(text, docfile.Format{Scale: []float64{2, 1.6, 1.35}[level-1]})
}

func rules(fs []Finding) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return strings.Join(out, " ")
}

func TestImageNeedsADescriptionOrToBeDecorative(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Image: &docfile.ImageItem{URL: "a.png"}},
		{Image: &docfile.ImageItem{URL: "b.png", Alt: "A graph of sales rising"}},
		{Image: &docfile.ImageItem{URL: "c.png", Decorative: true}},
	}}
	fs := Check(doc, Options{})
	if len(fs) != 1 || fs[0].Rule != RuleImageAlt || fs[0].Severity != Error || fs[0].Text != "a.png" || fs[0].Offset != 0 || fs[0].Item != 0 || fs[0].Fix == "" {
		t.Fatalf("findings: %+v", fs)
	}
}

func TestHeadingLevelsAndEmptyHeadings(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		heading(1, "Title"), chars("\n", docfile.Format{}),
		heading(3, "Deep"), chars("\n", docfile.Format{}), // skips level 2
		heading(2, "Back up"), chars("\n", docfile.Format{}), // going up is fine
		heading(2, " "), chars("\n", docfile.Format{}), // empty
		heading(3, "Next"),
	}}
	fs := Check(doc, Options{})
	if rules(fs) != "heading-skip heading-empty" {
		t.Fatalf("findings: %+v", fs)
	}
	if !strings.Contains(fs[0].Message, "level 3 after a level 1") || !strings.Contains(fs[0].Fix, "level 2") {
		t.Errorf("skip message: %+v", fs[0])
	}
}

func TestLowContrastIsFoundAgainstTheBackgroundOrThePage(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		chars("fine ", docfile.Format{Color: "#111827"}),
		chars("faint grey", docfile.Format{Color: "#cccccc"}),                     // 1.6:1 on white
		chars("yellow on white", docfile.Format{Color: "yellow"}),                 // 1.07:1
		chars("white on dark", docfile.Format{Color: "#fff", Background: "#222"}), // fine
		chars("grey on grey", docfile.Format{Color: "#777", Background: "#888"}),
		chars("big mid grey", docfile.Format{Color: "#949494", FontSize: 30}), // 3.03:1: enough for large text
		chars("small mid grey", docfile.Format{Color: "#949494"}),             // not for small text
		chars("unknown", docfile.Format{Color: "var(--x)"}),                   // not understood: skipped
	}}
	var got []string
	for _, f := range Check(doc, Options{}) {
		if f.Rule != RuleContrast || f.Item < 0 {
			t.Errorf("unexpected finding %+v", f)
		}
		got = append(got, f.Text)
	}
	if want := "faint grey|yellow on white|grey on grey|small mid grey"; strings.Join(got, "|") != want {
		t.Errorf("flagged %q, want %q", strings.Join(got, "|"), want)
	}
	dark := Check(docfile.Document{Content: []docfile.Item{chars("light", docfile.Format{Color: "#cccccc"}), chars("dim", docfile.Format{Color: "#222222"})}}, Options{Page: "#101010"})
	if len(dark) != 1 || dark[0].Text != "dim" {
		t.Errorf("dark page: %+v", dark)
	}
}

func TestParseColor(t *testing.T) {
	for in, want := range map[string]rgb{"#fff": {255, 255, 255}, "#0a0b0c": {10, 11, 12}, "rgb(1, 2, 3)": {1, 2, 3}, "Black": {0, 0, 0}, "LightGoldenRodYellow": {250, 250, 210}, "darkslategrey": {47, 79, 79}} {
		if got, ok := parseColor(in); !ok || got != want {
			t.Errorf("%q = %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "#12", "#gggggg", "rgb(1,2)", "rgb(300,0,0)", "transparent", "hsl(0,0%,0%)"} {
		if _, ok := parseColor(in); ok {
			t.Errorf("%q should not be understood", in)
		}
	}
	if r := contrast(rgb{0, 0, 0}, rgb{255, 255, 255}); r < 20.9 || r > 21.1 {
		t.Errorf("black on white = %v", r)
	}
}

func TestACleanDocumentHasNoFindings(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{heading(1, "Title"), chars("\n", docfile.Format{}), chars("Body.", docfile.Format{Color: "#111827"})}}
	if fs := Check(doc, Options{}); len(fs) != 0 {
		t.Errorf("findings: %+v", fs)
	}
}

func TestFindingsCarryTheirPositionInTheTextStream(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		chars("Intro\n", docfile.Format{}),                                            // offsets 0 to 5, the newline is 5
		{Table: &docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"a"}}}}}, // one object at 6
		{Image: &docfile.ImageItem{URL: "a.png"}},                                     // at 7
		chars("\n", docfile.Format{}),                                                 // 8
		{Para: &docfile.Para{Style: "Heading 5"}},
		chars("Deep\n", docfile.Format{}),
		{Para: &docfile.Para{Style: "Heading 2"}},
		chars("Up", docfile.Format{}),
	}}
	fs := Check(doc, Options{})
	if len(fs) != 1 || fs[0].Rule != RuleImageAlt || fs[0].Offset != 7 || fs[0].Item != 2 {
		t.Fatalf("image findings: %+v", fs)
	}
	doc2 := docfile.Document{Content: []docfile.Item{
		{Para: &docfile.Para{Style: "Heading 1"}}, chars("Title\n", docfile.Format{}),
		{Para: &docfile.Para{Style: "Heading 5"}}, chars("Deep\n", docfile.Format{}), // 6 is where this paragraph starts
	}}
	fs = Check(doc2, Options{})
	if len(fs) != 1 || fs[0].Rule != RuleHeadingSkip || fs[0].Offset != 6 || !strings.Contains(fs[0].Message, "level 5 after a level 1") {
		t.Fatalf("heading findings: %+v", fs)
	}
}

func TestGateDecides(t *testing.T) {
	errF := Finding{Severity: Error}
	warnF := Finding{Severity: Warning}
	for _, c := range []struct {
		mode     GateMode
		fs       []Finding
		override bool
		allow    bool
		errs     int
	}{
		{GateAllow, []Finding{errF}, false, true, 1},
		{GateWarn, []Finding{errF, warnF}, false, true, 1},
		{GateBlock, []Finding{errF}, false, false, 1},
		{GateBlock, []Finding{errF}, true, true, 1},
		{GateBlock, []Finding{warnF}, false, true, 0},
		{GateBlock, nil, false, true, 0},
	} {
		if allow, errs := c.mode.Decide(c.fs, c.override); allow != c.allow || errs != c.errs {
			t.Errorf("%q %d findings override %v: allow %v errors %d, want %v %d", c.mode, len(c.fs), c.override, allow, errs, c.allow, c.errs)
		}
	}
}

func TestFindingsNameTheirOneClickAction(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Para: &docfile.Para{Style: "Heading 1"}}, chars("Title\n", docfile.Format{}),
		{Para: &docfile.Para{Style: "Heading 3"}}, chars("Deep\n", docfile.Format{}),
		chars("faint", docfile.Format{Color: "#cccccc"}),
		{Image: &docfile.ImageItem{URL: "a.png"}},
	}}
	got := map[string]Finding{}
	for _, f := range Check(doc, Options{}) {
		got[f.Rule] = f
	}
	if f := got[RuleHeadingSkip]; f.Action != ActionHeadingLevel || f.Arg != 2 {
		t.Errorf("heading: %+v", f)
	}
	if f := got[RuleContrast]; f.Action != ActionDefaultColour || f.Length != 5 {
		t.Errorf("contrast: %+v", f)
	}
	if f := got[RuleImageAlt]; f.Action != ActionDescribe {
		t.Errorf("image: %+v", f)
	}
}

func TestFloatingObjectsGetTheSameRules(t *testing.T) {
	fs := CheckObjects([]Object{
		{Kind: "image", Where: "Picture 1"},
		{Kind: "image", Where: "Picture 2", Alt: "A red door"},
		{Kind: "image", Where: "Picture 3", Decorative: true},
		{Kind: "table", Where: "Table 1", Rows: [][]string{{"", ""}, {"a", "b"}}},
		{Kind: "table", Where: "Table 2", Rows: [][]string{{"Name", "Amount"}, {"a", "1"}}},
		{Kind: "table", Where: "Table 3", Rows: [][]string{{"Name", ""}, {"a", "1"}}},
	})
	got := map[string]string{}
	for _, f := range fs {
		got[f.Text] = f.Rule
		if f.Offset != -1 || f.Item != -1 || f.Fix == "" {
			t.Errorf("a floating object has no place in the text: %+v", f)
		}
		if !strings.Contains(f.Message, f.Text) {
			t.Errorf("the message names the object: %q", f.Message)
		}
	}
	want := map[string]string{"Picture 1": RuleImageAlt, "Table 1": RuleTableHeaderMissing, "Table 3": RuleTableHeaderEmpty}
	if len(got) != len(want) {
		t.Fatalf("findings %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
}
