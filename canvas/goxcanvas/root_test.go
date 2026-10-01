package goxcanvas

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

// painter records fillText calls; every rune is 8px wide.
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
func (p *painter) DrawImage(_ canvas.ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64) {
}

func (p *painter) has(s string) bool {
	for _, t := range p.texts {
		if strings.Contains(t, s) {
			return true
		}
	}
	return false
}

func template(page gox.Node, _ int) gox.Node {
	return gox.Frag(
		Header(HeaderProps{Text: "Report"}),
		page,
		Footer(FooterProps{Children: []gox.Node{PageNumber(PageNumberProps{Format: "Page {n}"})}}),
	)
}

func tree(src string, pages int, tpl func(gox.Node, int) gox.Node) gox.Node {
	return Canvas(CanvasProps{Width: 700, Height: 500, Children: []gox.Node{
		TextFlow(TextFlowProps{
			Source: "", Text: src, PageCount: pages, PageWidth: 300, PageHeight: 200,
			Margins: []float64{20, 20, 30, 30}, Pages: tpl,
		}),
	}})
}

func newRoot() (*Root, *painter) {
	p := &painter{}
	st := canvas.NewStage(p, 0, 0)
	return NewRoot(st), p
}

func TestRenderBuildsPagesWithInheritance(t *testing.T) {
	r, p := newRoot()
	if err := r.Render(tree("hello", 3, template)); err != nil {
		t.Fatal(err)
	}
	ps := r.Find(TagPage, 0).(*canvas.PageSet)
	if len(ps.Pages) != 3 {
		t.Fatalf("pages = %d", len(ps.Pages))
	}
	if ps.Pages[0].Header == nil || ps.Pages[0].Header.Text != "Report" {
		t.Fatalf("page 0 header = %+v", ps.Pages[0].Header)
	}
	if ps.Pages[1].Header != nil || ps.Pages[2].Header != nil {
		t.Error("equal headers must inherit (nil) on later pages")
	}
	if got := ps.PageNumberText(2); got != "Page 3" {
		t.Errorf("page 3 number = %q, want Page 3", got)
	}
	if ps.Pages[0].Margin != (canvas.Insets{Top: 20, Right: 20, Bottom: 30, Left: 30}) {
		t.Errorf("margin = %+v", ps.Pages[0].Margin)
	}
	if r.Stage.Width != 700 || r.Stage.Height != 500 {
		t.Errorf("stage size = %g x %g", r.Stage.Width, r.Stage.Height)
	}
	r.Stage.Draw()
	if !p.has("hello") || !p.has("Report") || !p.has("Page 1") {
		t.Errorf("drawn text = %q", p.texts)
	}
}

func TestPerPageTemplateChanges(t *testing.T) {
	tpl := func(page gox.Node, i int) gox.Node {
		switch i {
		case 0:
			return gox.Frag(Header(HeaderProps{Text: "A"}), page,
				PageNumber(PageNumberProps{None: true}))
		case 1:
			return gox.Frag(Header(HeaderProps{Text: "B"}), page,
				PageNumber(PageNumberProps{Format: "{n}", Start: 7}))
		default: // header dropped, numbering continues
			return gox.Frag(page, PageNumber(PageNumberProps{Format: "{n}"}))
		}
	}
	r, _ := newRoot()
	if err := r.Render(tree("x", 3, tpl)); err != nil {
		t.Fatal(err)
	}
	ps := r.Find(TagPage, 0).(*canvas.PageSet)
	if ps.ResolvedHeader(0).Text != "A" || ps.ResolvedHeader(1).Text != "B" || ps.ResolvedHeader(2) != nil {
		t.Errorf("headers: %v %v %v", ps.ResolvedHeader(0), ps.ResolvedHeader(1), ps.ResolvedHeader(2))
	}
	if ps.PageNumberText(0) != "" || ps.PageNumberText(1) != "7" {
		t.Errorf("numbers: %q %q", ps.PageNumberText(0), ps.PageNumberText(1))
	}
	// Page 2's config equals page 1's apart from Start, so it continues: 8.
	if ps.PageNumberText(2) != "8" {
		t.Errorf("page 3 number = %q, want 8", ps.PageNumberText(2))
	}
}

func TestUpdateKeepsIdentityAndEdits(t *testing.T) {
	r, _ := newRoot()
	_ = r.Render(tree("one", 2, template))
	flow := r.Find(TagTextFlow, 0).(*canvas.TextFlow)
	ps := r.Find(TagPage, 0).(*canvas.PageSet)
	flow.SetSelection(0, 2)
	// An edit made on the canvas survives a re-render with the same props.
	ps.Pages[0].Header = &canvas.HeaderFooter{Text: "edited"}

	redraws := 0
	r.Redraw = func() { redraws++ }
	_ = r.Render(tree("one", 2, template))
	if redraws != 0 {
		t.Errorf("identical render redrew %d times", redraws)
	}
	if r.Find(TagTextFlow, 0) != flow || r.Find(TagPage, 0) != ps {
		t.Fatal("components were recreated")
	}
	if ps.Pages[0].Header.Text != "edited" {
		t.Error("re-render with the same props undid the canvas edit")
	}
	if from, to := flow.Selection(); from != 0 || to != 2 {
		t.Errorf("selection lost: %d..%d", from, to)
	}

	// A changed prop is pushed down.
	_ = r.Render(tree("two", 2, template))
	if flow.Text != "two" || redraws != 1 {
		t.Errorf("text = %q, redraws = %d", flow.Text, redraws)
	}
	if ps.Pages[0].Header.Text != "edited" {
		t.Error("changing the text must not rebuild pages")
	}
	_ = r.Render(tree("two", 3, template))
	if len(ps.Pages) != 3 || ps.Pages[0].Header.Text != "Report" {
		t.Errorf("pages after count change: %d, header %+v", len(ps.Pages), ps.Pages[0].Header)
	}
}

func TestModeAndErrors(t *testing.T) {
	r, _ := newRoot()
	n := Canvas(CanvasProps{Mode: canvas.ModeEdit, Children: []gox.Node{TextFlow(TextFlowProps{Text: "x"})}})
	if err := r.Render(n); err != nil {
		t.Fatal(err)
	}
	if r.Find(TagTextFlow, 0).(*canvas.TextFlow).Mode() != canvas.ModeEdit {
		t.Error("Canvas mode not applied")
	}
	if err := r.Render(gox.Text("x")); err == nil {
		t.Error("non-Canvas root should fail")
	}
	bad := Canvas(CanvasProps{Children: []gox.Node{PageNumber(PageNumberProps{})}})
	if err := r.Render(bad); err == nil || !strings.Contains(err.Error(), "unsupported element") {
		t.Errorf("err = %v", err)
	}
	if NewRoot(nil).Find(TagTextFlow, 0) != nil {
		t.Error("Find before Render should be nil")
	}
}

func TestHeaderTextFromChildrenAndMargins(t *testing.T) {
	tpl := func(page gox.Node, _ int) gox.Node {
		return gox.Frag(Header(HeaderProps{Children: []gox.Node{gox.Text(" Title ")}}), page,
			Footer(FooterProps{Text: "foot"}))
	}
	r, _ := newRoot()
	n := Canvas(CanvasProps{Children: []gox.Node{TextFlow(TextFlowProps{Text: "x", Pages: tpl, Margins: []float64{10, 20}})}})
	if err := r.Render(n); err != nil {
		t.Fatal(err)
	}
	ps := r.Find(TagPage, 0).(*canvas.PageSet)
	if ps.Pages[0].Header.Text != "Title" || ps.Pages[0].Footer.Text != "foot" {
		t.Errorf("hf = %+v %+v", ps.Pages[0].Header, ps.Pages[0].Footer)
	}
	if m := ps.Pages[0].Margin; m != (canvas.Insets{Top: 10, Bottom: 10, Left: 20, Right: 20}) {
		t.Errorf("margin = %+v", m)
	}
	if got := insets([]float64{5}); got.Left != 5 || got.Bottom != 5 {
		t.Errorf("one-value insets = %+v", got)
	}
	if got := insets(nil); got.Top != 50 {
		t.Errorf("default insets = %+v", got)
	}
}

func TestNumberingStartsAtSheetPosition(t *testing.T) {
	tpl := func(page gox.Node, i int) gox.Node {
		if i == 0 {
			return gox.Frag(page)
		}
		return gox.Frag(page, PageNumber(PageNumberProps{}))
	}
	r, _ := newRoot()
	if err := r.Render(tree("x", 3, tpl)); err != nil {
		t.Fatal(err)
	}
	ps := r.Find(TagPage, 0).(*canvas.PageSet)
	if ps.PageNumberText(0) != "" || ps.PageNumberText(1) != "2" || ps.PageNumberText(2) != "3" {
		t.Errorf("numbers = %q %q %q", ps.PageNumberText(0), ps.PageNumberText(1), ps.PageNumberText(2))
	}
}

func TestFeaturesProp(t *testing.T) {
	r, _ := newRoot()
	if err := r.Render(Canvas(CanvasProps{})); err != nil {
		t.Fatal(err)
	}
	if !r.Stage.Features.Has(canvas.CmdMoveLineUp) {
		t.Fatal("default features stay when Features is nil")
	}
	_ = r.Render(Canvas(CanvasProps{Features: []canvas.Feature{canvas.MultiCursor{}}}))
	if r.Stage.Features.Has(canvas.CmdMoveLineUp) || !r.Stage.Features.Has(canvas.CmdAddNextMatch) {
		t.Error("Features replaces the registry")
	}
	reg := r.Stage.Features
	_ = r.Render(Canvas(CanvasProps{Features: []canvas.Feature{canvas.MultiCursor{}}}))
	if r.Stage.Features != reg {
		t.Error("the same feature list must not rebuild the registry")
	}
}
