//go:build !(js && wasm)

package main

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
)

type painter struct{ texts []string }

func (p *painter) SetFillStyle(string)           {}
func (p *painter) SetStrokeStyle(string)         {}
func (p *painter) SetLineWidth(float64)          {}
func (p *painter) SetFont(string)                {}
func (p *painter) SetTextAlign(string)           {}
func (p *painter) SetTextBaseline(string)        {}
func (p *painter) FillRect(x, y, w, h float64)   {}
func (p *painter) StrokeRect(x, y, w, h float64) {}
func (p *painter) ClearRect(x, y, w, h float64)  {}
func (p *painter) BeginPath()                    {}
func (p *painter) MoveTo(x, y float64)           {}
func (p *painter) LineTo(x, y float64)           {}
func (p *painter) Stroke()                       {}
func (p *painter) FillText(t string, x, y float64) {
	p.texts = append(p.texts, t)
}
func (p *painter) MeasureTextWidth(t string) float64 { return 8 * float64(utf8.RuneCountInString(t)) }
func (p *painter) SetGlobalAlpha(float64)            {}
func (p *painter) Save()                             {}
func (p *painter) Restore()                          {}
func (p *painter) Translate(x, y float64)            {}
func (p *painter) ClipRect(x, y, w, h float64)       {}
func (p *painter) Rotate(float64)                    {}
func (p *painter) DrawImage(_ canvas.ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64) {
}

func (p *painter) drawn(s string) bool {
	for _, t := range p.texts {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func render(t *testing.T, st State) (*cxango.Root, *painter) {
	t.Helper()
	p := &painter{}
	root := cxango.NewRoot(canvas.NewStage(p, 0, 0))
	if err := root.Render(Doc(st)); err != nil {
		t.Fatal(err)
	}
	return root, p
}

func furniture(root *cxango.Root) (headers, numbers []string) {
	ps := root.Find(cxango.TagPage, 0).(*canvas.PageSet)
	for i := range ps.Pages {
		h := ""
		if hf := ps.ResolvedHeader(i); hf != nil {
			h = hf.Text
		}
		headers = append(headers, h)
		numbers = append(numbers, ps.PageNumberText(i))
	}
	return headers, numbers
}

func TestDocGivesEachPageItsHeaderAndNumber(t *testing.T) {
	root, p := render(t, State{Source: "# Quarterly report\n\nSome text.", Pages: 3})
	headers, numbers := furniture(root)
	if want := []string{"Gojiberry report", "Gojiberry report", "Appendix"}; !reflect.DeepEqual(headers, want) {
		t.Errorf("headers = %q, want %q (inherited, then changed on page 3)", headers, want)
	}
	if want := []string{"", "Page 2 of 3", "Page 3 of 3"}; !reflect.DeepEqual(numbers, want) {
		t.Errorf("numbers = %q, want %q (none on page 1)", numbers, want)
	}
	if root.Stage.Width != 900 || root.Stage.Height != 560 {
		t.Errorf("stage = %g x %g, want 900 x 560", root.Stage.Width, root.Stage.Height)
	}
	root.Stage.Draw()
	for _, want := range []string{"Gojiberry report", "Appendix", "Page 2 of 3", "Page 3 of 3", "Some text."} {
		if !p.drawn(want) {
			t.Errorf("%q was not drawn; drawn: %q", want, p.texts)
		}
	}
	if p.drawn("Page 1 of") {
		t.Errorf("page 1 shows a number: %q", p.texts)
	}
}

func TestDocFollowsThePageCount(t *testing.T) {
	cases := []struct {
		pages   int
		headers []string
		numbers []string
	}{
		{1, []string{"Gojiberry report"}, []string{""}},
		{2, []string{"Gojiberry report", "Gojiberry report"}, []string{"", "Page 2 of 2"}},
		{5, []string{"Gojiberry report", "Gojiberry report", "Appendix", "Appendix", "Appendix"},
			[]string{"", "Page 2 of 5", "Page 3 of 5", "Page 4 of 5", "Page 5 of 5"}},
	}
	for _, c := range cases {
		root, _ := render(t, State{Source: "text", Pages: c.pages})
		headers, numbers := furniture(root)
		if !reflect.DeepEqual(headers, c.headers) || !reflect.DeepEqual(numbers, c.numbers) {
			t.Errorf("%d pages: headers %q numbers %q, want %q and %q", c.pages, headers, numbers, c.headers, c.numbers)
		}
	}
}

func TestDocPassesTheModeToTheFlow(t *testing.T) {
	for _, mode := range []canvas.Mode{canvas.ModeView, canvas.ModeEdit} {
		root, _ := render(t, State{Mode: mode, Source: "text", Pages: 1})
		flow := root.Find(cxango.TagTextFlow, 0).(*canvas.TextFlow)
		if flow.Mode() != mode {
			t.Errorf("flow mode = %v, want %v", flow.Mode(), mode)
		}
	}
}

func TestRerenderUpdatesTheComponentsInPlace(t *testing.T) {
	p := &painter{}
	root := cxango.NewRoot(canvas.NewStage(p, 0, 0))
	if err := root.Render(Doc(State{Source: "first text", Pages: 3})); err != nil {
		t.Fatal(err)
	}
	flow := root.Find(cxango.TagTextFlow, 0)
	pages := root.Find(cxango.TagPage, 0)

	if err := root.Render(Doc(State{Source: "second text", Pages: 2, Mode: canvas.ModeEdit})); err != nil {
		t.Fatal(err)
	}
	if root.Find(cxango.TagTextFlow, 0) != flow || root.Find(cxango.TagPage, 0) != pages {
		t.Error("the components were replaced instead of updated")
	}
	headers, numbers := furniture(root)
	if len(headers) != 2 || numbers[1] != "Page 2 of 2" {
		t.Errorf("after the change to 2 pages: headers %q numbers %q", headers, numbers)
	}
	if flow.(*canvas.TextFlow).Mode() != canvas.ModeEdit {
		t.Error("the new mode did not reach the flow")
	}
	p.texts = nil
	root.Stage.Draw()
	if !p.drawn("second text") || p.drawn("first text") {
		t.Errorf("drawn after re-render: %q", p.texts)
	}
}
