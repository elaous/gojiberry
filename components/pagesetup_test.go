package components

import (
	"math"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango"
)

func TestPageSetupSizeLabelAndLimits(t *testing.T) {
	p := DefaultPageSetup()
	if w, h := p.Size(); w != 595 || h != 842 || p.Label() != "A4 / portrait" {
		t.Fatalf("%v %v %q", w, h, p.Label())
	}
	p.Paper, p.Landscape = "letter", true
	if w, h := p.Size(); w != 792 || h != 612 || p.Label() != "Letter / landscape" {
		t.Fatalf("%v %v %q", w, h, p.Label())
	}
	if (PageSetup{Paper: "nope"}).Normalized().Paper != "a4" {
		t.Error("an unknown paper is A4")
	}
	n := PageSetup{Paper: "a4", Margins: [4]float64{-5, 500, 600, 500}}.Normalized()
	if n.Margins[0] != 0 || n.Margins[1]+n.Margins[3] > 595*3/4 || n.Margins[0]+n.Margins[2] > 842*3/4 {
		t.Errorf("%v", n.Margins)
	}
}

func TestPageSetupPanelReportsChanges(t *testing.T) {
	var got []PageSetup
	n := PageSetupPanel(PageSetupProps{Setup: DefaultPageSetup(), OnChange: func(p PageSetup) { got = append(got, p) }})
	html := render(t, n)
	mustContain(t, html, "Page setup", `value="a4" selected="selected"`, "Letter (216 × 279 mm)", `data-side="left"`, `value="72"`, `value="normal" selected="selected"`)
	hs := findHandlers(n, "change") // paper, orientation, preset, then four margins
	if len(hs) != 7 {
		t.Fatalf("%d handlers", len(hs))
	}
	hs[0](xango.Event{Value: "legal"})
	hs[1](xango.Event{Value: "landscape"})
	hs[2](xango.Event{Value: "narrow"})
	hs[3](xango.Event{Value: "10"})
	hs[4](xango.Event{Value: "oops"}) // not a number: ignored
	if len(got) != 4 || got[0].Paper != "legal" || !got[1].Landscape || got[2].Margins != [4]float64{36, 36, 36, 36} || got[3].Margins[0] != 10 {
		t.Errorf("%+v", got)
	}
}

func TestStatusBarAndHeadingPath(t *testing.T) {
	html := render(t, StatusBar(StatusBarProps{Items: []StatusItem{
		{ID: "setup", Text: "A4 / portrait"}, {ID: "save", Text: "Saved", Live: true, State: "saved"}, {ID: "empty"},
	}}))
	mustContain(t, html, `data-item="setup"`, "A4 / portrait", `role="status"`, `data-state="saved"`)
	mustNotContain(t, html, `data-item="empty"`)

	es := []canvas.OutlineEntry{{Level: 1, Text: "One"}, {Level: 2, Text: "One.a"}, {Level: 3, Text: "One.a.i"}, {Level: 1, Text: "Two"}, {Level: 3, Text: "Two.x"}}
	if got := HeadingBreadcrumb(es, 2); got != "One › One.a › One.a.i" {
		t.Error(got)
	}
	if got := HeadingBreadcrumb(es, 4); got != "Two › Two.x" { // skips the level between
		t.Error(got)
	}
	if got := HeadingBreadcrumb(es, 3); got != "Two" {
		t.Error(got)
	}
	if HeadingBreadcrumb(es, -1) != "" || HeadingBreadcrumb(nil, 0) != "" {
		t.Error("no heading, no path")
	}
}

func TestPageSetupIgnoresNonNumbers(t *testing.T) {
	n := PageSetup{Paper: "a4", Margins: [4]float64{math.NaN(), math.Inf(1), math.Inf(-1), 10}}.Normalized()
	for i, m := range n.Margins {
		if math.IsNaN(m) || math.IsInf(m, 0) || m < 0 {
			t.Errorf("margin %d is %v", i, m)
		}
	}
	if n.Margins[3] != 10 {
		t.Error(n.Margins)
	}
}

func TestPageSetupOfFindsThePaperOfASavedSheet(t *testing.T) {
	m := [4]float64{36, 36, 36, 36}
	if p, ok := PageSetupOf(612, 792, m); !ok || p.Paper != "letter" || p.Landscape || p.Margins != m {
		t.Errorf("Letter: %+v %v", p, ok)
	}
	if p, ok := PageSetupOf(842, 595, m); !ok || p.Paper != "a4" || !p.Landscape {
		t.Errorf("A4 turned: %+v %v", p, ok)
	}
	if p, ok := PageSetupOf(500, 300, m); !ok || p.Paper != CustomPaper || !p.Landscape {
		t.Errorf("custom, wide: %+v %v", p, ok)
	} else if w, h := p.Size(); w != 500 || h != 300 {
		t.Errorf("the custom sheet keeps its size: %v x %v", w, h)
	}
	if p, ok := PageSetupOf(300, 500, m); !ok || p.Paper != CustomPaper || p.Landscape {
		t.Errorf("custom, tall: %+v %v", p, ok)
	}
	if _, ok := PageSetupOf(50, 50, m); ok {
		t.Error("a sheet smaller than 100 points cannot be laid out")
	}
	if p := (PageSetup{Paper: CustomPaper, CustomWidth: 10, CustomHeight: 10}).Normalized(); p.Paper != "a4" {
		t.Errorf("a custom paper with no usable size is A4: %+v", p)
	}
	if _, ok := PageSetupOf(0, 0, m); ok {
		t.Error("no sheet saved is not a paper")
	}
}
