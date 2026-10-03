package canvas

import (
	"reflect"
	"testing"
)

// A 100x80 viewport at (10, 20) over 300x400 content.
func newScroll() (*ScrollView, *fakePainter) {
	p := &fakePainter{}
	sv := NewScrollView(10, 20, 100, 80)
	sv.SetPainter(p)
	sv.Add(&probe{}, 300, 400)
	return sv, p
}

// probe is a content object that records the events it receives.
type probe struct {
	downs, moves, ups [][2]float64
	wheelHandled      bool
	drew              int
	dragging          bool
}

func (c *probe) Draw() { c.drew++ }
func (c *probe) MouseDown(x, y float64) bool {
	c.downs = append(c.downs, [2]float64{x, y})
	c.dragging = true
	return true
}
func (c *probe) MouseMove(x, y float64) bool {
	c.moves = append(c.moves, [2]float64{x, y})
	return false
}
func (c *probe) MouseUp(x, y float64) bool {
	c.ups = append(c.ups, [2]float64{x, y})
	return false
}
func (c *probe) MouseWheel(x, y, dx, dy float64) bool { return c.wheelHandled }

func content(sv *ScrollView) *probe { return sv.content.Components[0].(*probe) }

func TestContentSizeIsLargestObject(t *testing.T) {
	sv, _ := newScroll()
	sv.Add(&probe{}, 500, 100)
	if w, h := sv.ContentSize(); w != 500 || h != 400 {
		t.Fatalf("content = %gx%g", w, h)
	}
}

func TestWheelScrollsAndClamps(t *testing.T) {
	sv, _ := newScroll()
	if !sv.MouseWheel(50, 50, 0, 30) || sv.ScrollY != 30 || sv.ScrollX != 0 {
		t.Fatalf("scroll = %g,%g", sv.ScrollX, sv.ScrollY)
	}
	sv.MouseWheel(50, 50, 40, 0)
	if sv.ScrollX != 40 {
		t.Fatalf("scrollX = %g", sv.ScrollX)
	}
	sv.MouseWheel(50, 50, 0, 10000)
	sv.MouseWheel(50, 50, 10000, 0)
	if sv.ScrollX != 200 || sv.ScrollY != 320 { // content - viewport
		t.Fatalf("scroll = %g,%g, want 200,320", sv.ScrollX, sv.ScrollY)
	}
	if sv.MouseWheel(50, 50, 0, 10) {
		t.Fatal("at the end: not handled, so the page can take over")
	}
	sv.MouseWheel(50, 50, -10000, -10000)
	if sv.ScrollX != 0 || sv.ScrollY != 0 {
		t.Fatalf("scroll = %g,%g", sv.ScrollX, sv.ScrollY)
	}
}

func TestWheelOutsideViewportIgnored(t *testing.T) {
	sv, _ := newScroll()
	if sv.MouseWheel(500, 500, 0, 30) || sv.ScrollY != 0 {
		t.Fatal("wheel outside the viewport must not scroll")
	}
}

func TestLocksFreezeAnAxis(t *testing.T) {
	sv, _ := newScroll()
	sv.LockV = true
	if sv.MouseWheel(50, 50, 0, 30) || sv.ScrollY != 0 {
		t.Fatal("vertical scroll is locked")
	}
	if !sv.MouseWheel(50, 50, 25, 30) || sv.ScrollX != 25 || sv.ScrollY != 0 {
		t.Fatalf("horizontal should still scroll: %g,%g", sv.ScrollX, sv.ScrollY)
	}
	sv.LockV, sv.LockH = false, true
	sv.MouseWheel(50, 50, 25, 30)
	if sv.ScrollX != 25 || sv.ScrollY != 30 {
		t.Fatalf("scroll = %g,%g", sv.ScrollX, sv.ScrollY)
	}
	if sv.ScrollTo(100, 100); sv.ScrollX != 25 || sv.ScrollY != 100 {
		t.Fatalf("ScrollTo must respect locks: %g,%g", sv.ScrollX, sv.ScrollY)
	}
}

func TestOnScrollAndNoOp(t *testing.T) {
	sv, _ := newScroll()
	n := 0
	sv.OnScroll = func(*ScrollView) { n++ }
	sv.ScrollBy(0, 10)
	sv.ScrollBy(0, 0)
	sv.ScrollTo(0, 10)
	if n != 1 {
		t.Fatalf("OnScroll fired %d times, want 1", n)
	}
}

func TestSmallContentDoesNotScroll(t *testing.T) {
	p := &fakePainter{}
	sv := NewScrollView(0, 0, 100, 80)
	sv.SetPainter(p)
	sv.Add(&probe{}, 50, 50)
	if sv.MouseWheel(10, 10, 20, 20) || sv.ScrollX != 0 || sv.ScrollY != 0 {
		t.Fatal("content fits: nothing to scroll")
	}
	sv.Draw()
	if p.has("fillStyle rgba(0, 0, 0, 0.35)") {
		t.Fatal("no scrollbar when content fits")
	}
}

func TestSetContentSizeReclamps(t *testing.T) {
	sv, _ := newScroll()
	sv.ScrollTo(0, 320)
	sv.SetContentSize(300, 200)
	if sv.ScrollY != 120 {
		t.Fatalf("scrollY = %g, want 120", sv.ScrollY)
	}
}

func TestDrawClipsAndTranslates(t *testing.T) {
	sv, p := newScroll()
	sv.ScrollTo(15, 25)
	p.calls = nil
	sv.Draw()
	want := []string{"save", "clipRect 10 20 100 80", "translate -5 -5", "restore"}
	var got []string
	for _, c := range p.calls {
		for _, w := range want {
			if c == w {
				got = append(got, c)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v", p.calls)
	}
	if content(sv).drew != 1 {
		t.Fatal("content should be drawn once, between save and restore")
	}
	// The content draws inside the clip: its Draw must happen after the
	// translate and before the restore.
	var order []string
	for _, c := range p.calls {
		if c == "translate -5 -5" || c == "restore" {
			order = append(order, c)
		}
	}
	if order[0] != "translate -5 -5" {
		t.Fatalf("order = %v", order)
	}
}

func TestMouseMapsIntoContentCoordinates(t *testing.T) {
	sv, _ := newScroll()
	sv.ScrollTo(30, 40)
	sv.MouseDown(20, 50) // viewport-local (10, 30) + scroll (30, 40)
	c := content(sv)
	if len(c.downs) != 1 || c.downs[0] != [2]float64{40, 70} {
		t.Fatalf("downs = %v", c.downs)
	}
	sv.MouseMove(25, 55)
	sv.MouseUp(25, 55)
	if c.moves[0] != [2]float64{45, 75} || c.ups[0] != [2]float64{45, 75} {
		t.Fatalf("moves=%v ups=%v", c.moves, c.ups)
	}
}

func TestPressesOutsideTheViewportDoNotReachClippedContent(t *testing.T) {
	sv, _ := newScroll()
	if sv.MouseDown(500, 500) || len(content(sv).downs) != 0 {
		t.Fatal("outside the viewport")
	}
	sv.MouseMove(500, 500)
	if len(content(sv).moves) != 0 {
		t.Fatal("hover outside the viewport must not hit clipped content")
	}
}

func TestDragStartedInContentKeepsReceivingMovesOutsideViewport(t *testing.T) {
	sv, _ := newScroll()
	sv.MouseDown(20, 30)
	sv.MouseMove(500, 500)
	if len(content(sv).moves) != 1 {
		t.Fatal("an active drag should keep following the pointer")
	}
	sv.MouseUp(500, 500)
	sv.MouseMove(500, 500)
	if len(content(sv).moves) != 1 {
		t.Fatal("after release, moves outside are ignored again")
	}
}

func TestInnerWheelHandlerWins(t *testing.T) {
	sv, _ := newScroll()
	content(sv).wheelHandled = true
	if !sv.MouseWheel(50, 50, 0, 30) || sv.ScrollY != 0 {
		t.Fatal("content handled the wheel; the view must not also scroll")
	}
}

func TestNestedScrollViewsChain(t *testing.T) {
	p := &fakePainter{}
	outer := NewScrollView(0, 0, 200, 200)
	inner := NewScrollView(10, 10, 100, 100)
	outer.SetPainter(p)
	outer.Add(inner, 400, 400)
	inner.Add(&probe{}, 100, 150) // can scroll 50px
	outer.MouseWheel(50, 50, 0, 30)
	if inner.ScrollY != 30 || outer.ScrollY != 0 {
		t.Fatalf("inner=%g outer=%g", inner.ScrollY, outer.ScrollY)
	}
	outer.MouseWheel(50, 50, 0, 100) // inner hits its end (50); that wheel is consumed there
	if inner.ScrollY != 50 {
		t.Fatalf("inner = %g", inner.ScrollY)
	}
	outer.MouseWheel(50, 50, 0, 100) // inner is at its end: the outer view takes it
	if outer.ScrollY != 100 {
		t.Fatalf("outer = %g", outer.ScrollY)
	}
}

// Vertical bar: x 100..110 (view x 10..110, minus 10), track y 20..100
// (no horizontal bar? there is one: content is wider), so the track
// ends at y=90. Horizontal bar: y 90..100.

func TestScrollbarThumbDrag(t *testing.T) {
	sv, _ := newScroll()
	track, thumb := sv.bar(true)
	if track[3] != 70 || thumb[3] < sv.Style.MinThumb {
		t.Fatalf("track=%v thumb=%v", track, thumb)
	}
	// 70*80/400 = 14 < MinThumb 24 -> 24; room = 46 over 320px of scroll.
	if !sv.MouseDown(thumb[0]+5, thumb[1]+5) {
		t.Fatal("press on the thumb")
	}
	sv.MouseMove(thumb[0]+5, thumb[1]+5+23) // half the room
	if sv.ScrollY != 160 {
		t.Fatalf("scrollY = %g, want 160", sv.ScrollY)
	}
	sv.MouseMove(thumb[0]+5, 10000)
	if sv.ScrollY != 320 {
		t.Fatalf("scrollY = %g, want clamp at 320", sv.ScrollY)
	}
	if len(content(sv).downs) != 0 {
		t.Fatal("a scrollbar press is not a content press")
	}
	if !sv.MouseUp(0, 0) || sv.drag != nil {
		t.Fatal("release ends the drag")
	}
}

func TestScrollbarTrackClickCentersThumb(t *testing.T) {
	sv, _ := newScroll()
	track, thumb := sv.bar(true)
	sv.MouseDown(track[0]+5, track[1]+track[3]-2) // near the bottom
	if sv.ScrollY != 320 {
		t.Fatalf("scrollY = %g", sv.ScrollY)
	}
	_, thumb2 := sv.bar(true)
	if thumb2[1] <= thumb[1] {
		t.Fatal("thumb should have moved down")
	}
}

func TestHorizontalScrollbar(t *testing.T) {
	sv, _ := newScroll()
	track, thumb := sv.bar(false)
	// Width 100 minus the vertical bar's 10.
	if track[2] != 90 || track[1] != 90 {
		t.Fatalf("track = %v", track)
	}
	sv.MouseDown(thumb[0]+3, thumb[1]+3)
	sv.MouseMove(thumb[0]+3+1000, 0)
	if sv.ScrollX != 200 || sv.ScrollY != 0 {
		t.Fatalf("scroll = %g,%g", sv.ScrollX, sv.ScrollY)
	}
}

func TestLockedAxisHidesItsScrollbar(t *testing.T) {
	sv, p := newScroll()
	sv.LockH = true
	if sv.showH() || !sv.showV() {
		t.Fatal("only the vertical bar remains")
	}
	if tr, _ := sv.bar(true); tr[3] != 80 {
		t.Fatalf("vertical track should use the full height: %v", tr)
	}
	sv.Draw()
	if !p.has("fillStyle rgba(0, 0, 0, 0.35)") {
		t.Fatal("vertical thumb drawn")
	}
}

func TestScrollViewForwardsKeysFocusAndCursor(t *testing.T) {
	var log []string
	sv, _ := newScroll()
	s := &spy{name: "s", log: &log, key: true, focus: true, css: "text"}
	sv.Add(s, 10, 10)
	if !sv.HandleKey("a", false) || !sv.InsertText("a") || !sv.WantsFocus() {
		t.Fatal("not forwarded")
	}
	if sv.CSSCursor() != "text" {
		t.Fatalf("cursor = %q", sv.CSSCursor())
	}
	s.css = ""
	if sv.CSSCursor() != "" {
		t.Fatal("no opinion when content has none")
	}
	sv.MouseMove(sv.X+sv.Width-5, sv.Y+5)
	if sv.CSSCursor() != "default" {
		t.Fatal("over a scrollbar: default arrow")
	}
	if !sv.MouseLeave() {
		t.Fatal("leave clears the hovered bar")
	}
}

func TestStageWheelAndModePropagation(t *testing.T) {
	sv, p := newScroll()
	im := NewImage(fakeImage{10, 10}, 0, 0)
	sv.Add(im, 10, 10)
	st := NewStage(p, 300, 300, sv)
	st.SetMode(ModeEdit)
	if im.Mode() != ModeEdit {
		t.Fatal("mode should reach components inside a ScrollView")
	}
	if !st.MouseWheel(50, 50, 0, 20) || sv.ScrollY != 20 {
		t.Fatal("stage should deliver the wheel to the scroll view")
	}
	if st.MouseWheel(250, 250, 0, 20) {
		t.Fatal("wheel outside is not handled")
	}
}
