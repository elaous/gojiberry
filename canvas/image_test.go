package canvas

import "testing"

// 200x100 bitmap shown at natural size at (40, 60): the visible
// rectangle is x 40..240, y 60..160. Toolbar buttons sit at y 32..54,
// x = 40 + 56*i, 52 wide.

func newImg() (*Image, *fakePainter) {
	p := &fakePainter{}
	im := NewImage(fakeImage{200, 100}, 40, 60)
	im.SetPainter(p)
	im.SetMode(ModeEdit)
	return im, p
}

func TestNewImageDefaults(t *testing.T) {
	im := NewImage(fakeImage{200, 100}, 1, 2)
	if im.W != 200 || im.H != 100 || im.Alpha != 1 || im.Snap != 5 || im.Mode() != ModeView || im.Tool() != ToolMove || im.Wrap {
		t.Fatalf("%+v", im)
	}
}

func TestVisibleRespectsClip(t *testing.T) {
	im, _ := newImg()
	im.Clip = Insets{Left: 10, Top: 20, Right: 30, Bottom: 40}
	if x, y, w, h := im.Visible(); x != 50 || y != 80 || w != 160 || h != 40 {
		t.Fatalf("visible = %g %g %g %g", x, y, w, h)
	}
}

func TestDrawScalesSourceRect(t *testing.T) {
	p := &fakePainter{}
	im := NewImage(fakeImage{200, 100}, 40, 60)
	im.SetPainter(p)
	im.W, im.H = 100, 50 // shown at half size: 2 source px per displayed px
	im.Clip = Insets{Left: 10, Top: 5, Right: 20}
	im.Alpha = 0.5
	im.Draw()
	for _, want := range []string{"alpha 0.5", "drawImage 20 10 140 90 -> 50 65 70 45", "alpha 1"} {
		if !p.has(want) {
			t.Errorf("missing %q in %v", want, p.calls)
		}
	}
	if p.has("strokeRect") || p.has("fillText") {
		t.Fatal("view mode draws no chrome")
	}
}

func TestMoveDragSnapsToFive(t *testing.T) {
	im, _ := newImg()
	changes := 0
	im.OnChange = func(*Image) { changes++ }
	if !im.MouseDown(100, 100) {
		t.Fatal("press on the image starts a move")
	}
	im.MouseMove(113, 140) // +13 px right, and a vertical move that must be ignored
	if im.X != 55 || im.Y != 60 {
		t.Fatalf("X,Y = %g,%g, want 55,60", im.X, im.Y)
	}
	im.MouseMove(111, 100) // +11 -> 51 -> snaps to 50
	if im.X != 50 {
		t.Fatalf("X = %g, want 50", im.X)
	}
	if im.MouseMove(112, 100) { // +12 -> 52 -> still 50: no change
		t.Fatal("no change, no redraw")
	}
	if !im.MouseUp(0, 0) || im.MouseUp(0, 0) {
		t.Fatal("release ends the drag once")
	}
	if changes != 2 {
		t.Fatalf("OnChange fired %d times, want 2", changes)
	}
}

func TestMoveClampsToBounds(t *testing.T) {
	im, _ := newImg()
	im.BoundsW = 300
	im.MouseDown(100, 100)
	im.MouseMove(1000, 100)
	if im.X != 100 { // visible right edge at 300
		t.Fatalf("X = %g, want 100", im.X)
	}
	im.MouseMove(-1000, 100)
	if im.X != 0 {
		t.Fatalf("X = %g, want 0", im.X)
	}
}

func TestMoveClampKeepsVisiblePartInBoundsWhenClipped(t *testing.T) {
	im, _ := newImg()
	im.Clip.Left = 50
	im.BoundsW = 300
	im.MouseDown(100, 100)
	im.MouseMove(-1000, 100)
	if vx, _, _, _ := im.Visible(); vx != 0 {
		t.Fatalf("visible x = %g, want 0", vx)
	}
}

func TestToolbarSelectsToolAndTogglesWrap(t *testing.T) {
	im, _ := newImg()
	im.MouseDown(40+56+5, 40) // "Clip"
	if im.Tool() != ToolClip {
		t.Fatalf("tool = %v", im.Tool())
	}
	im.MouseDown(40+56*2+5, 40) // "Alpha"
	if im.Tool() != ToolAlpha {
		t.Fatalf("tool = %v", im.Tool())
	}
	im.MouseDown(40+5, 40)
	if im.Tool() != ToolMove {
		t.Fatalf("tool = %v", im.Tool())
	}
	im.MouseDown(40+56*3+5, 40) // "Wrap"
	if !im.Wrap {
		t.Fatal("wrap should be on")
	}
	im.MouseDown(40+56*3+5, 40)
	if im.Wrap {
		t.Fatal("wrap should be off")
	}
	if im.Mode() != ModeEdit {
		t.Fatal("mode changed")
	}
}

func TestClipHandlesSnapAndClamp(t *testing.T) {
	im, _ := newImg()
	im.SetTool(ToolClip)
	im.MouseDown(40, 110) // left handle (x 36..44, y 102..118)
	im.MouseMove(40+23, 110)
	if im.Clip.Left != 25 {
		t.Fatalf("left = %g, want 25", im.Clip.Left)
	}
	im.MouseMove(40+1000, 110)
	if im.Clip.Left != 190 { // W - right(0) - minSize(10)
		t.Fatalf("left = %g, want 190", im.Clip.Left)
	}
	im.MouseMove(40-50, 110)
	if im.Clip.Left != 0 {
		t.Fatalf("left = %g", im.Clip.Left)
	}
	im.MouseUp(0, 0)

	im.Clip = Insets{}
	im.MouseDown(240, 110) // right handle
	im.MouseMove(240-47, 110)
	if im.Clip.Right != 45 {
		t.Fatalf("right = %g, want 45", im.Clip.Right)
	}
	im.MouseUp(0, 0)

	im.Clip = Insets{}
	im.MouseDown(140, 60) // top handle
	im.MouseMove(140, 60+18)
	if im.Clip.Top != 20 {
		t.Fatalf("top = %g, want 20", im.Clip.Top)
	}
	im.MouseUp(0, 0)

	im.Clip = Insets{}
	im.MouseDown(140, 160) // bottom handle
	im.MouseMove(140, 160-33)
	if im.Clip.Bottom != 35 {
		t.Fatalf("bottom = %g, want 35", im.Clip.Bottom)
	}
}

func TestClipToolDoesNotMove(t *testing.T) {
	im, _ := newImg()
	im.SetTool(ToolClip)
	if im.MouseDown(140, 110) {
		t.Fatal("body drag is the Move tool's job")
	}
}

func TestClipDrawsGhostOfCroppedPart(t *testing.T) {
	im, p := newImg()
	im.SetTool(ToolClip)
	im.Clip.Left = 40
	im.Draw()
	if !p.has("alpha 0.3") || !p.has("drawImage 0 0 200 100 -> 40 60 200 100") {
		t.Fatalf("ghost not drawn: %v", p.calls)
	}
	if !p.has("drawImage 40 0 160 100 -> 80 60 160 100") {
		t.Fatalf("kept part not drawn: %v", p.calls)
	}
}

func TestAlphaSliderSnapsToFivePercent(t *testing.T) {
	im, _ := newImg()
	im.SetTool(ToolAlpha)
	// Track: x 40..240, y 170..178.
	if !im.MouseDown(40+100, 172) || im.Alpha != 0.5 {
		t.Fatalf("alpha = %g, want 0.5", im.Alpha)
	}
	im.MouseMove(40+131, 300) // 65.5% -> 65%
	if im.Alpha != 0.65 {
		t.Fatalf("alpha = %g, want 0.65", im.Alpha)
	}
	im.MouseMove(40+1000, 0)
	if im.Alpha != 1 {
		t.Fatalf("alpha = %g", im.Alpha)
	}
	im.MouseMove(-1000, 0)
	if im.Alpha != 0 {
		t.Fatalf("alpha = %g", im.Alpha)
	}
}

func TestAlphaToolDrawsSlider(t *testing.T) {
	im, p := newImg()
	im.SetTool(ToolAlpha)
	im.Alpha = 0.5
	im.Draw()
	if !p.has(`fillText "50%"`) || !p.has("alpha 0.5") {
		t.Fatalf("slider/label missing: %v", p.calls)
	}
}

func TestImageViewModeIsReadOnly(t *testing.T) {
	im, p := newImg()
	im.SetMode(ModeView)
	if im.MouseDown(100, 100) || im.MouseMove(100, 100) || im.CSSCursor() != "" {
		t.Fatal("view mode ignores input")
	}
	p.calls = nil
	im.Draw()
	if p.has(`fillText "Move"`) || p.has("strokeRect") {
		t.Fatal("view mode draws no chrome")
	}
	if !p.has("drawImage") {
		t.Fatal("view mode still draws the image")
	}
}

func TestSwitchingToViewCancelsDrag(t *testing.T) {
	im, _ := newImg()
	im.MouseDown(100, 100)
	im.SetMode(ModeView)
	im.SetMode(ModeEdit)
	if im.MouseMove(300, 100) {
		t.Fatal("drag should have been cancelled")
	}
}

func TestEditDrawsToolbarWithActiveTool(t *testing.T) {
	im, p := newImg()
	im.Draw()
	for _, l := range []string{"Move", "Clip", "Alpha", "Wrap"} {
		if !p.has(`fillText "` + l + `"`) {
			t.Errorf("toolbar button %q missing", l)
		}
	}
	if !p.has("fillStyle #7c5cff") {
		t.Fatal("active tool highlighted")
	}
}

func TestHoverCursor(t *testing.T) {
	im, _ := newImg()
	im.MouseMove(100, 100)
	if im.CSSCursor() != "move" {
		t.Fatalf("cursor = %q", im.CSSCursor())
	}
	im.MouseMove(45, 40)
	if im.CSSCursor() != "pointer" {
		t.Fatalf("cursor = %q", im.CSSCursor())
	}
	im.SetTool(ToolClip)
	im.MouseMove(40, 110)
	if im.CSSCursor() != "ew-resize" {
		t.Fatalf("cursor = %q", im.CSSCursor())
	}
	im.MouseMove(140, 60)
	if im.CSSCursor() != "ns-resize" {
		t.Fatalf("cursor = %q", im.CSSCursor())
	}
	im.MouseDown(140, 60)
	if im.CSSCursor() != "ns-resize" {
		t.Fatal("cursor during drag")
	}
	im.MouseUp(0, 0)
	im.SetTool(ToolMove)
	im.MouseDown(100, 100)
	if im.CSSCursor() != "grabbing" {
		t.Fatalf("cursor = %q", im.CSSCursor())
	}
	im.MouseUp(0, 0)
	im.MouseMove(1000, 1000)
	if !im.MouseLeave() && im.CSSCursor() != "" {
		t.Fatal("leave clears hover")
	}
}

// --- wrap-around ---

func TestExclusion(t *testing.T) {
	im, _ := newImg()
	if _, _, ok := im.Exclusion(60, 160); ok {
		t.Fatal("no exclusion while Wrap is off")
	}
	im.Wrap = true
	x0, x1, ok := im.Exclusion(100, 120)
	if !ok || x0 != 32 || x1 != 248 {
		t.Fatalf("got %g %g %v, want the visible span +/- 8px margin", x0, x1, ok)
	}
	if _, _, ok := im.Exclusion(0, 52); ok {
		t.Fatal("band above the image (incl. margin) is clear")
	}
	if _, _, ok := im.Exclusion(168, 200); ok {
		t.Fatal("band below the image (incl. margin) is clear")
	}
	if _, _, ok := im.Exclusion(0, 53); !ok {
		t.Fatal("margin counts")
	}
	im.Clip.Left = 100
	if x0, _, _ := im.Exclusion(100, 120); x0 != 132 {
		t.Fatalf("cropped part no longer excludes text: x0 = %g", x0)
	}
}

func wrapSetup(wrap bool) (*Image, *TextFlow) {
	p := &fakePainter{}
	f := NewTextFlow("aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj", 0, 0, 400, 400)
	f.SetPainter(p)
	im := NewImage(fakeImage{100, 44}, 300, 10) // visible x 300..400, y 10..54 (+8 margin)
	im.SetPainter(p)
	im.SetFlow(f)
	im.Wrap = wrap
	return im, f
}

// overlaps reports whether any laid-out line intrudes on the x0..x1 by
// y0..y1 rectangle.
func overlaps(l Layout, lineH, x0, x1, y0, y1 float64) bool {
	for _, ln := range l.Lines {
		if ln.Y < y1 && ln.Y+lineH > y0 && ln.X < x1 && ln.X+ln.W > x0 {
			return true
		}
	}
	return false
}

func TestWrapAroundKeepsTextOffTheImage(t *testing.T) {
	_, f := wrapSetup(false)
	open := f.Layout()
	if len(open.Lines) != 1 || !overlaps(open, 22, 300, 400, 10, 54) {
		t.Fatalf("without wrap the text runs under the image: %+v", open.Lines)
	}
	_, f = wrapSetup(true)
	lay := f.Layout()
	if len(lay.Lines) < 2 {
		t.Fatalf("text should have wrapped: %+v", lay.Lines)
	}
	// Image is x 300..400, y 10..54; with the 8px margin: 292..408, 2..62.
	if overlaps(lay, 22, 292, 408, 2, 62) {
		t.Fatalf("text overlaps the image: %+v", lay.Lines)
	}
}

func TestDraggingImageRewrapsText(t *testing.T) {
	im, f := wrapSetup(true)
	im.SetMode(ModeEdit)
	im.MouseDown(350, 30)
	im.MouseMove(350-150, 30) // visible x is now 150..250 (+/- 8 margin)
	lay := f.Layout()
	if overlaps(lay, 22, 142, 258, 2, 62) {
		t.Fatalf("text overlaps the moved image: %+v", lay.Lines)
	}
	if lay.Overflow {
		t.Fatal("everything should still fit")
	}
}

func TestCroppingImageRewrapsText(t *testing.T) {
	im, f := wrapSetup(true)
	before := f.Layout()
	im.Clip.Left = 50 // visible x is now 350..400
	after := f.Layout()
	if after.Lines[0].W <= before.Lines[0].W {
		t.Fatalf("a narrower image leaves more room: before %+v after %+v", before.Lines[0], after.Lines[0])
	}
}

func TestSetFlowReplacesObstacle(t *testing.T) {
	im, f1 := wrapSetup(true)
	f2 := NewTextFlow("x", 0, 0, 10, 10)
	im.SetFlow(f2)
	if len(f1.obstacles) != 0 || len(f2.obstacles) != 1 || im.Flow() != f2 {
		t.Fatal("image should move to the new flow")
	}
	im.SetFlow(nil)
	if len(f2.obstacles) != 0 {
		t.Fatal("nil removes it")
	}
}

func TestSetWrap(t *testing.T) {
	im, _ := newImg()
	im.SetWrap(true)
	if !im.Wrap {
		t.Fatal("SetWrap(true)")
	}
	im.SetWrap(false)
	if im.Wrap {
		t.Fatal("SetWrap(false)")
	}
}
