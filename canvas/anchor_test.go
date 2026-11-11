package canvas

import "testing"

func TestAnchorResolveAndAnchorAt(t *testing.T) {
	ps := twoPages() // pages at x=24; y=24 and y=148
	if x, y := ps.Resolve(Anchor{Page: 1, X: 10, Y: 20}); x != 34 || y != 168 {
		t.Fatalf("resolve = %g,%g", x, y)
	}
	if x, y := ps.Resolve(Anchor{Page: 9, X: 1, Y: 2}); x != 25 || y != 150 {
		t.Fatalf("an out-of-range page clamps to the last: %g,%g", x, y)
	}
	if a := ps.AnchorAt(34, 168); a != (Anchor{Page: 1, X: 10, Y: 20}) {
		t.Fatalf("anchorAt = %+v", a)
	}
	if a := ps.AnchorAt(50, 130); a.Page != 0 || a.Y != 106 { // in the gap, nearer page 0
		t.Fatalf("gap point anchors to the nearest page: %+v", a)
	}
	if a := ps.AnchorAt(50, 140); a.Page != 1 || a.Y != -8 {
		t.Fatalf("gap point nearer page 1: %+v", a)
	}
	if a := ps.AnchorAt(5, 5); a.Page != 0 || a.X != -19 || a.Y != -19 {
		t.Fatalf("off the top: %+v", a)
	}
	empty := NewPageSet()
	if x, y := empty.Resolve(Anchor{X: 1, Y: 2}); x != 1 || y != 2 {
		t.Fatal("no pages: anchors are absolute")
	}
	if a := empty.AnchorAt(3, 4); a != (Anchor{X: 3, Y: 4}) {
		t.Fatalf("%+v", a)
	}
}

func TestFloatingImageAnchoredToAPage(t *testing.T) {
	ps := twoPages()
	im, _ := newImg() // 200x100
	im.Pages = ps
	im.Anchor = &Anchor{Page: 1, X: 30, Y: 10}
	if x, y, _, _ := im.Visible(); x != 54 || y != 158 {
		t.Fatalf("visible at %g,%g, want page 1's sheet (24,148) + (30,10)", x, y)
	}
	ps.Y = 100 // the whole document moves
	if _, y, _, _ := im.Visible(); y != 258 {
		t.Fatalf("anchored images move with their page: y=%g", y)
	}
	ps.Y = 0
	im.MouseDown(100, 200)
	im.MouseMove(133, 200)
	if im.Anchor.X != 65 || im.Anchor.Page != 1 || im.Anchor.Y != 10 { // 30+33 snapped to 5, relative to the page
		t.Fatalf("anchor = %+v", *im.Anchor)
	}
	im.Wrap = true
	if x0, _, ok := im.Exclusion(160, 170); !ok || x0 != 24+65-8 {
		t.Fatalf("exclusion x0 = %g ok=%v", x0, ok)
	}
}

func TestAnchoredImageUsesTheFlowsPages(t *testing.T) {
	f := pagedFlow(Chars{Text: "x"})
	im := NewImage(fakeImage{50, 50}, 0, 0)
	im.SetFlow(f)
	im.Anchor = &Anchor{Page: 0, X: 5, Y: 6}
	if x, y, _, _ := im.Visible(); x != 29 || y != 30 {
		t.Fatalf("visible at %g,%g", x, y)
	}
	im2 := NewImage(fakeImage{50, 50}, 7, 8)
	im2.Anchor = &Anchor{Page: 3, X: 1, Y: 1}
	if x, y, _, _ := im2.Visible(); x != 7 || y != 8 {
		t.Fatalf("visible at %g,%g", x, y)
	}
}

func TestFloatingTextAnchoredAndReanchoredAcrossPages(t *testing.T) {
	ps := twoPages()
	p := &fakePainter{}
	ft := NewFloatingText("note", 0, 0, 100, 40)
	ft.SetPainter(p)
	ft.Pages = ps
	ft.Anchor = &Anchor{Page: 0, X: 20, Y: 30}
	ft.SetMode(ModeEdit)
	if x, y, _, _ := ft.Box(); x != 44 || y != 54 {
		t.Fatalf("box at %g,%g", x, y)
	}
	ft.MouseDown(50, 54-17)
	ft.MouseMove(50, 54-17+105)
	if ft.Anchor.Page != 1 || ft.Anchor.Y != 10 || ft.Anchor.X != 20 {
		t.Fatalf("anchor = %+v", *ft.Anchor)
	}
	if x, y, _, _ := ft.Box(); x != 44 || y != 158 {
		t.Fatalf("box at %g,%g", x, y)
	}
	ps.Y = 50
	if _, y, _, _ := ft.Box(); y != 208 {
		t.Fatalf("the box follows its page: y=%g", y)
	}
	if _, _, ok := ft.Exclusion(0, 1000); ok {
		t.Fatal("not wrapping")
	}
}

func inlineImage(t *testing.T) (*Image, *TextFlow, *fakePainter) {
	t.Helper()
	p := &fakePainter{}
	f := NewTextFlow("", 10, 30, 100, 300) // 100px column
	f.SetPainter(p)
	im := NewImage(fakeImage{200, 100}, 0, 0)
	im.Placement = PlaceInline
	im.SetPainter(p)
	im.SetFlow(f)
	f.Items = []Item{Inline{Object: im}}
	return im, f, p
}

func TestInlineImageFitsTheColumnKeepingAspectRatio(t *testing.T) {
	im, f, _ := inlineImage(t)
	l := f.Layout()
	o := l.Lines[0].Objects[0]
	if o.W != 100 || o.H != 50 || im.scale != 0.5 {
		t.Fatalf("object %gx%g scale %g, want 100x50 at 0.5", o.W, o.H, im.scale)
	}
	f.Height = 30
	f.Layout()
	if im.scale != 0.3 {
		t.Fatalf("scale = %g, want 0.3 (30/100)", im.scale)
	}
	small := NewImage(fakeImage{20, 10}, 0, 0)
	if w, h := small.Measure(nil, 500, 500); w != 20 || h != 10 {
		t.Fatalf("small image = %gx%g", w, h)
	}
	if w, h := (&Image{W: 10, H: 10, Clip: Insets{Left: 5, Right: 5}}).Measure(nil, 50, 50); w != 0 || h != 0 {
		t.Fatalf("fully clipped image has no size: %gx%g", w, h)
	}
}

func TestInlineImageDrawAt(t *testing.T) {
	im, f, p := inlineImage(t)
	im.Alpha = 0.5
	im.Clip = Insets{Left: 20, Top: 10}
	f.Draw()
	if !p.has("alpha 0.5") || !p.has("drawImage 20 10 180 90 -> 10 ") {
		t.Fatalf("calls: %v", p.calls)
	}
	im.Draw()
	count := 0
	for _, c := range p.calls {
		if len(c) > 9 && c[:9] == "drawImage" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("bitmap drawn %d times, want 1 (by the flow only)", count)
	}
}

func TestInlineImageIsNotAnObstacleAndHasNoMove(t *testing.T) {
	im, f, _ := inlineImage(t)
	im.Wrap = true
	if _, _, ok := im.Exclusion(0, 1000); ok {
		t.Fatal("an inline image is part of the text, not an obstacle")
	}
	f.Layout()
	im.SetMode(ModeEdit)
	vx, vy, vw, vh := im.Visible()
	if im.MouseDown(vx+vw/2, vy+vh/2) {
		t.Fatal("Move tool is disabled for inline images")
	}
	for _, i := range []int{0, 3} { // the Move and Wrap toolbar buttons
		b := im.buttonRect(i)
		if im.MouseDown(b[0]+2, b[1]+2) || im.Wrap != true {
			t.Fatalf("toolbar button %d should do nothing for an inline image", i)
		}
	}
	b := im.buttonRect(1) // Clip works
	if !im.MouseDown(b[0]+2, b[1]+2) || im.Tool() != ToolClip {
		t.Fatal("Clip tool selectable")
	}
}

func TestInlineImageClipDragWorksInDisplayedPixels(t *testing.T) {
	im, f, _ := inlineImage(t)
	f.Layout()
	f.Draw() // places the image (DrawAt records where it is)
	im.SetMode(ModeEdit)
	im.SetTool(ToolClip)
	vx, vy, vw, vh := im.Visible()
	if vw != 100 || vh != 50 {
		t.Fatalf("visible %gx%g", vw, vh)
	}
	im.MouseDown(vx, vy+vh/2)
	im.MouseMove(vx+20, vy+vh/2)
	if im.Clip.Left != 40 {
		t.Fatalf("clip left = %g, want 40", im.Clip.Left)
	}
	im.MouseUp(0, 0)
	f.Layout()
	f.Draw()
	if _, _, w, _ := im.Visible(); w != 80 { // Re-fitted to the 100px column at scale 0.625.
		if w != 100 {
			t.Fatalf("visible width after the clip = %g", w)
		}
	}
	p := im.Painter.(*fakePainter)
	p.calls = nil
	im.Draw()
	if !p.has(`fillText "Clip"`) || !p.has("strokeStyle #7c5cff") {
		t.Fatalf("edit chrome missing: %v", p.calls)
	}
}

func TestInlineImageAlphaSlider(t *testing.T) {
	im, f, _ := inlineImage(t)
	f.Draw()
	im.SetMode(ModeEdit)
	im.SetTool(ToolAlpha)
	vx, vy, vw, vh := im.Visible()
	im.MouseDown(vx+vw/2, vy+vh+14)
	if im.Alpha != 0.5 {
		t.Fatalf("alpha = %g", im.Alpha)
	}
}

func TestInlineImageInTextLineSitsOnBaseline(t *testing.T) {
	im, f, _ := inlineImage(t)
	f.Width = 400
	f.Items = []Item{Chars{Text: "ab "}, Inline{Object: im}, Chars{Text: " cd"}}
	l := f.Layout()
	ln := l.Lines[0]
	if len(ln.Objects) != 1 || ln.Objects[0].Y+ln.Objects[0].H != ln.Baseline {
		t.Fatalf("line = %+v", ln)
	}
	if im.scale != 1 || ln.Objects[0].W != 200 {
		t.Fatalf("fits at natural size in a wide column: scale=%g", im.scale)
	}
}
