package gox

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

type bitmap struct{ w, h float64 }

func (b bitmap) Size() (float64, float64) { return b.w, b.h }

func TestObjectsWrapAroundTheFlowBeforeThem(t *testing.T) {
	r, _ := newRoot()
	anchor := &canvas.Anchor{Page: 0, X: 10, Y: 20}
	tree := func(wrap bool, opacity float64) gox.Node {
		return Canvas(CanvasProps{Width: 700, Height: 500, Mode: canvas.ModeEdit, Children: []gox.Node{
			TextFlow(TextFlowProps{Text: "hello", PageCount: 1, PageWidth: 300, PageHeight: 300}),
			Image(ImageProps{Source: bitmap{40, 30}, Anchor: anchor, Wrap: wrap, Opacity: opacity, Width: 80}),
			FloatingText(FloatingTextProps{Text: "note", Anchor: &canvas.Anchor{X: 5, Y: 5}, Wrap: true, Vocabulary: []string{"alpha"}}),
			Table(TableProps{Cells: [][]string{{"a", "b"}, {"c", "d"}}, Anchor: &canvas.Anchor{X: 50, Y: 60}, Wrap: true}),
		}})
	}
	if err := r.Render(tree(true, 0.5)); err != nil {
		t.Fatal(err)
	}
	flow := r.Find(TagTextFlow, 0).(*canvas.TextFlow)
	im := r.Find(TagImage, 0).(*canvas.Image)
	ft := r.Find(TagFloatingText, 0).(*canvas.FloatingText)
	tv := r.Find(TagTable, 0).(*canvas.TableView)
	if im.Flow() != flow || ft.Flow() != flow || tv.Flow() != flow {
		t.Fatal("floating objects wrap around the flow before them")
	}
	if im.Alpha != 0.5 || im.W != 80 || im.H != 30 || !im.Wrap || im.Anchor == nil || im.Anchor == anchor {
		t.Errorf("image: %+v", im)
	}
	if ft.Width != 190 || ft.Text() != "note" || !ft.Wrap || tv.Table.Cell(1, 1) != "d" {
		t.Errorf("float/table: %+v", ft)
	}
	for _, m := range []canvas.Mode{im.Mode(), ft.Mode(), tv.Mode()} {
		if m != canvas.ModeEdit {
			t.Error("children take the Canvas mode")
		}
	}
	r.Stage.Draw() // everything draws

	// A drag on the canvas survives an identical re-render; a changed prop wins.
	im.X, im.Y = 99, 98
	ft.SetText("edited")
	tv.Table.SetCell(0, 0, "Z")
	redraws := 0
	r.Redraw = func() { redraws++ }
	_ = r.Render(tree(true, 0.5))
	if redraws != 0 || im.X != 99 || ft.Text() != "edited" || tv.Table.Cell(0, 0) != "Z" {
		t.Errorf("identical render changed things: redraws=%d", redraws)
	}
	_ = r.Render(tree(false, 0.5))
	if im.Wrap || redraws != 1 {
		t.Errorf("wrap prop: %v, redraws %d", im.Wrap, redraws)
	}
	_ = r.Render(tree(false, 0)) // zero opacity prop = opaque
	if im.Alpha != 1 {
		t.Errorf("alpha = %g", im.Alpha)
	}
}

func TestObjectPropsUpdate(t *testing.T) {
	r, _ := newRoot()
	render := func(img, text string, cells [][]string, x float64) {
		t.Helper()
		var src canvas.ImageSource = bitmap{10, 10}
		if img == "big" {
			src = bitmap{50, 50}
		}
		err := r.Render(Canvas(CanvasProps{Children: []gox.Node{
			Image(ImageProps{Source: src, X: x, Y: x, Inline: img == "inline", Clip: canvas.Insets{Left: x}}),
			FloatingText(FloatingTextProps{Text: text, X: x, Y: x, Width: 120 + x, Height: 50, Background: "#eee"}),
			Table(TableProps{Cells: cells, X: x, Y: x}),
		}}))
		if err != nil {
			t.Fatal(err)
		}
	}
	render("small", "a", [][]string{{"1"}}, 5)
	render("big", "b", [][]string{{"1", "2"}}, 7)
	im := r.Find(TagImage, 0).(*canvas.Image)
	ft := r.Find(TagFloatingText, 0).(*canvas.FloatingText)
	tv := r.Find(TagTable, 0).(*canvas.TableView)
	if im.W != 50 || im.X != 7 || im.Clip.Left != 7 || ft.Text() != "b" || ft.X != 7 || ft.Width != 127 ||
		tv.Table.Cols() != 2 || tv.X != 7 || ft.Background != "#eee" {
		t.Errorf("updated: im=%+v ft=%+v tv.X=%g", im, ft, tv.X)
	}
	render("inline", "b", [][]string{{"1", "2"}}, 7)
	if im.Placement != canvas.PlaceInline {
		t.Error("inline prop")
	}
}

func TestScrollViewElement(t *testing.T) {
	r, _ := newRoot()
	tree := func(lock bool, pages int) gox.Node {
		return Canvas(CanvasProps{Mode: canvas.ModeEdit, Children: []gox.Node{
			ScrollView(ScrollViewProps{X: 10, Y: 10, Width: 300, Height: 200, LockH: lock, Children: []gox.Node{
				TextFlow(TextFlowProps{Text: "hi", PageCount: pages, PageWidth: 250, PageHeight: 300}),
				Table(TableProps{Cells: [][]string{{"x"}}}),
			}}),
		}})
	}
	if err := r.Render(tree(true, 2)); err != nil {
		t.Fatal(err)
	}
	sv := r.Find(TagScrollView, 0).(*canvas.ScrollView)
	if w, h := sv.ContentSize(); h < 600 || w < 250 || !sv.LockH || sv.Width != 300 {
		t.Errorf("scroll view: content %g x %g lockH=%v", w, h, sv.LockH)
	}
	if r.Find(TagTextFlow, 0) == nil || r.Find(TagTable, 0) == nil || r.Find(TagTextFlow, 1) != nil {
		t.Error("Find looks inside a ScrollView")
	}
	if flow := r.Find(TagTextFlow, 0).(*canvas.TextFlow); flow.Mode() != canvas.ModeEdit {
		t.Error("content takes the mode")
	}
	r.Stage.Draw()
	_ = r.Render(tree(false, 3))
	if _, h := sv.ContentSize(); h < 900 || sv.LockH {
		t.Errorf("a changed page count regrows the content: %g, lockH %v", h, sv.LockH)
	}
	// A child added later gets the mode too.
	_ = r.Render(Canvas(CanvasProps{Mode: canvas.ModeEdit, Children: []gox.Node{
		ScrollView(ScrollViewProps{Width: 100, Height: 100, ContentWidth: 400, ContentHeight: 900, Children: []gox.Node{
			Table(TableProps{Cells: [][]string{{"x"}}}),
		}}),
	}}))
	if tv := r.Find(TagTable, 0).(*canvas.TableView); tv.Mode() != canvas.ModeEdit {
		t.Error("late child takes the mode")
	}
	if w, h := sv.ContentSize(); w != 400 || h != 900 {
		t.Errorf("explicit content size: %g x %g", w, h)
	}
}

func TestElementErrorsAndRegistry(t *testing.T) {
	r, _ := newRoot()
	for name, n := range map[string]gox.Node{
		"image without source": Image(ImageProps{}),
		"empty table":          Table(TableProps{}),
		"text child":           gox.Text("x"),
	} {
		if err := r.Render(Canvas(CanvasProps{Children: []gox.Node{n}})); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if err := r.Render(Canvas(CanvasProps{Children: []gox.Node{
		gox.Host(TagImage, "not props"),
	}})); err == nil || !strings.Contains(err.Error(), "props of type") {
		t.Errorf("wrong props type: %v", err)
	}
	for _, tag := range []string{TagTextFlow, TagFloatingText, TagTable, TagScrollView} {
		if err := (&Root{Stage: r.Stage}).Render(Canvas(CanvasProps{Children: []gox.Node{gox.Host(tag, 42)}})); err == nil {
			t.Errorf("%s with wrong props", tag)
		}
	}
	want := []string{TagFloatingText, TagImage, TagScrollView, TagTable, TagTextFlow}
	got := Elements()
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("%s not registered: %v", w, got)
		}
	}
}

// A custom element type registers like the built-in ones.
type badge struct{ text string }

func (b *badge) Drawers() []canvas.Drawer { return nil }
func (b *badge) Update(_ *Context, props any, _ []gox.Node) (bool, error) {
	s := props.(string)
	changed := s != b.text
	b.text = s
	return changed, nil
}
func (b *badge) Find(tag string) any {
	if tag == "x.Badge" {
		return b
	}
	return nil
}

func TestRegisterCustomElement(t *testing.T) {
	Register("x.Badge", func() Element { return &badge{} })
	defer func() {
		if recover() == nil {
			t.Error("registering a tag twice should panic")
		}
	}()
	r, _ := newRoot()
	if err := r.Render(Canvas(CanvasProps{Children: []gox.Node{gox.Host("x.Badge", "new")}})); err != nil {
		t.Fatal(err)
	}
	if r.Find("x.Badge", 0).(*badge).text != "new" {
		t.Error("custom element updated")
	}
	Register("x.Badge", func() Element { return &badge{} })
}

// Inserting an element in the middle (a fragment child that was empty
// before) keeps the others -- and what the user did to them.
func TestFragmentsAndStableMatching(t *testing.T) {
	r, _ := newRoot()
	tree := func(withImage bool) gox.Node {
		img := gox.Frag()
		if withImage {
			img = Image(ImageProps{Source: bitmap{10, 10}})
		}
		return Canvas(CanvasProps{Children: []gox.Node{
			TextFlow(TextFlowProps{Text: "x"}),
			img,
			FloatingText(FloatingTextProps{Text: "note"}),
			gox.Frag(Table(TableProps{Cells: [][]string{{"a"}}})),
		}})
	}
	if err := r.Render(tree(false)); err != nil {
		t.Fatal(err)
	}
	ft := r.Find(TagFloatingText, 0).(*canvas.FloatingText)
	tv := r.Find(TagTable, 0).(*canvas.TableView)
	ft.SetText("typed")
	if err := r.Render(tree(true)); err != nil {
		t.Fatal(err)
	}
	if r.Find(TagFloatingText, 0) != ft || r.Find(TagTable, 0) != tv || ft.Text() != "typed" {
		t.Error("an element inserted before them must not recreate them")
	}
	if r.Find(TagImage, 0) == nil {
		t.Error("image added")
	}
	order := func() []string {
		var tags []string
		for _, d := range r.Stage.Components {
			switch d.(type) {
			case *canvas.Image:
				tags = append(tags, "image")
			case *canvas.FloatingText:
				tags = append(tags, "float")
			}
		}
		return tags
	}
	if got := order(); strings.Join(got, ",") != "image,float" {
		t.Errorf("draw order follows the tree: %v", got)
	}
	im := r.Find(TagImage, 0).(*canvas.Image)
	_ = r.Render(tree(false))
	if r.Find(TagImage, 0) != nil || r.Find(TagFloatingText, 0) != ft {
		t.Error("removing the image keeps the rest")
	}
	if im.Flow() != nil {
		t.Error("a removed image stops obstructing the flow")
	}
}

func TestWatermarkBackgroundSpreadAndOpacityProps(t *testing.T) {
	r, _ := newRoot()
	img := bitmap{40, 20}
	tpl := func(page gox.Node, i int) gox.Node {
		switch i {
		case 0:
			return gox.Frag(page, Watermark(WatermarkProps{Text: "DRAFT", Opacity: 0.3}), Background(BackgroundProps{Image: img, Opacity: 0.5, Fit: canvas.FitContain}))
		case 3:
			return gox.Frag(page, Watermark(WatermarkProps{None: true}))
		default: // same as before: inherits; page 2 drops the background
			if i == 2 {
				return gox.Frag(page, Watermark(WatermarkProps{Text: "DRAFT", Opacity: 0.3}))
			}
			return gox.Frag(page, Watermark(WatermarkProps{Text: "DRAFT", Opacity: 0.3}), Background(BackgroundProps{Image: img, Opacity: 0.5, Fit: canvas.FitContain}))
		}
	}
	err := r.Render(Canvas(CanvasProps{Children: []gox.Node{
		TextFlow(TextFlowProps{Text: "x", PageCount: 4, Pages: tpl, Spread: true, SpreadBook: true, Gutter: 6}),
		FloatingText(FloatingTextProps{Text: "n", Opacity: 0.4}),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	ps := r.Find(TagPage, 0).(*canvas.PageSet)
	if ps.Pages[0].Watermark == nil || ps.Pages[0].Watermark.Text != "DRAFT" || ps.Pages[0].Watermark.Opacity != 0.3 {
		t.Errorf("page 0 watermark = %+v", ps.Pages[0].Watermark)
	}
	if ps.Pages[1].Watermark != nil || ps.Pages[1].Background != nil {
		t.Error("equal furniture inherits")
	}
	if ps.Pages[2].Background == nil || !ps.Pages[2].Background.None() {
		t.Errorf("a dropped background is an empty one: %+v", ps.Pages[2].Background)
	}
	if ps.ResolvedBackground(2) != nil || ps.ResolvedBackground(1) == nil {
		t.Error("background resolves: on for page 1, off from page 2")
	}
	if ps.Pages[3].Watermark == nil || !ps.Pages[3].Watermark.None() || ps.ResolvedWatermark(3) != nil {
		t.Errorf("none removes the watermark: %+v", ps.Pages[3].Watermark)
	}
	if ps.Pages[3].Background != nil && !ps.Pages[3].Background.None() {
		t.Errorf("the background stays off: %+v", ps.Pages[3].Background)
	}
	if !ps.Spread || !ps.SpreadBook || ps.Gutter != 6 {
		t.Error("spread props")
	}
	ft := r.Find(TagFloatingText, 0).(*canvas.FloatingText)
	if ft.Opacity != 0.4 {
		t.Errorf("floating text opacity = %g", ft.Opacity)
	}
	r.Stage.Draw()
	// A watermark with neither text nor image counts as none.
	_ = r.Render(Canvas(CanvasProps{Children: []gox.Node{TextFlow(TextFlowProps{Text: "x", PageCount: 2, Pages: func(page gox.Node, i int) gox.Node {
		return gox.Frag(page, Watermark(WatermarkProps{}), Background(BackgroundProps{}))
	}})}}))
	ps = r.Find(TagPage, 0).(*canvas.PageSet)
	if ps.Pages[0].Watermark != nil || ps.Pages[0].Background != nil {
		t.Errorf("empty watermark and background change nothing: %+v %+v", ps.Pages[0].Watermark, ps.Pages[0].Background)
	}
}
