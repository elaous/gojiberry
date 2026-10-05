package components

import (
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
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
	// Margins that would leave no room for text are shrunk, negative ones are 0.
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
