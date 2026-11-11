package canvas

import "testing"

func newMenu(log *[]string) (*Menu, *fakePainter) {
	p := &fakePainter{}
	m := NewMenu(p)
	act := func(n string) func() { return func() { *log = append(*log, n) } }
	m.Open([]MenuItem{
		{Label: "One", Action: act("one")},
		{Separator: true},
		{Label: "Two", Disabled: true, Action: act("two")},
		{Label: "Three", Action: act("three"), Danger: true},
	}, 50, 60, 0, 0)
	return m, p
}

func TestMenuHitTestAndActivate(t *testing.T) {
	var log []string
	m, _ := newMenu(&log)
	for _, tt := range []struct {
		x, y float64
		want int
	}{{60, 70, 0}, {60, 90, 1}, {60, 100, 2}, {60, 140, 3}, {60, 160, -1}, {40, 70, -1}, {230, 70, -1}} {
		if got := m.HitTest(tt.x, tt.y); got != tt.want {
			t.Errorf("HitTest(%g,%g)=%d, want %d", tt.x, tt.y, got, tt.want)
		}
	}
	if !m.MouseDown(60, 100) || !m.IsOpen() || len(log) != 0 {
		t.Fatal("disabled item is consumed but does nothing and keeps the menu open")
	}
	if !m.MouseDown(60, 90) || !m.IsOpen() {
		t.Fatal("separator is consumed and keeps the menu open")
	}
	if !m.MouseDown(60, 140) || m.IsOpen() || len(log) != 1 || log[0] != "three" {
		t.Fatalf("log=%v open=%v", log, m.IsOpen())
	}
	if m.MouseDown(60, 70) {
		t.Fatal("closed menu consumes nothing")
	}
}

func TestMenuOutsideClickClosesAndIsNotConsumed(t *testing.T) {
	var log []string
	m, _ := newMenu(&log)
	if m.MouseDown(500, 500) || m.IsOpen() {
		t.Fatal("outside click should close and not be consumed")
	}
}

func TestMenuHoverSkipsDisabled(t *testing.T) {
	var log []string
	m, _ := newMenu(&log)
	if !m.MouseMove(60, 70) || m.hover != 0 {
		t.Fatal("hover item 0")
	}
	if m.MouseMove(61, 71) {
		t.Fatal("same item is no change")
	}
	m.MouseMove(60, 100)
	if m.hover != -1 {
		t.Fatal("disabled items are not hoverable")
	}
	m.Close()
	if m.MouseMove(60, 70) {
		t.Fatal("closed menu ignores moves")
	}
}

func TestMenuKeyboard(t *testing.T) {
	var log []string
	m, _ := newMenu(&log)
	m.HandleKey("ArrowDown") // One
	m.HandleKey("ArrowDown") // skips separator + disabled -> Three
	m.HandleKey("ArrowDown") // wraps -> One
	m.HandleKey("ArrowUp")   // wraps back -> Three
	if m.hover != 3 {
		t.Fatalf("hover = %d, want 3", m.hover)
	}
	if !m.HandleKey("x") || !m.IsOpen() {
		t.Fatal("unknown keys are swallowed while open")
	}
	m.HandleKey("Enter")
	if len(log) != 1 || log[0] != "three" || m.IsOpen() {
		t.Fatalf("log=%v", log)
	}
	if m.HandleKey("Enter") {
		t.Fatal("closed menu handles no keys")
	}
	m, _ = newMenu(&log)
	m.HandleKey("ArrowUp") // from nothing -> last enabled
	if m.hover != 3 {
		t.Fatalf("hover = %d", m.hover)
	}
	m.HandleKey("Escape")
	if m.IsOpen() {
		t.Fatal("Escape closes")
	}
}

func TestMenuClampsToBounds(t *testing.T) {
	p := &fakePainter{}
	m := NewMenu(p)
	m.Open([]MenuItem{{Label: "Only"}}, 290, 190, 300, 200)
	if m.x != 300-m.w || m.y != 200-28 {
		t.Fatalf("x,y = %g,%g", m.x, m.y)
	}
	m.Open([]MenuItem{{Label: "Only"}}, -5, -5, 0, 0)
	if m.x != 0 || m.y != 0 {
		t.Fatalf("x,y = %g,%g", m.x, m.y)
	}
}

func TestMenuWidthGrowsWithLabel(t *testing.T) {
	p := &fakePainter{}
	m := NewMenu(p)
	label := "a very long menu label indeed, yes"
	m.Open([]MenuItem{{Label: label}}, 0, 0, 0, 0)
	if m.w != float64(len(label))*8+24 {
		t.Fatalf("w = %g", m.w)
	}
}

func TestMenuDraw(t *testing.T) {
	var log []string
	m, p := newMenu(&log)
	m.MouseMove(60, 70)
	p.calls = nil
	m.Draw()
	for _, want := range []string{`fillText "One"`, `fillText "Two"`, "fillStyle #a7abb2", "fillStyle #d1383d", "fillStyle #eceef1"} {
		if !p.has(want) {
			t.Errorf("missing %q", want)
		}
	}
	m.Close()
	p.calls = nil
	m.Draw()
	if len(p.calls) != 0 {
		t.Fatal("closed menu draws nothing")
	}
}

func TestMenuOpenWithinKeepsInsideBounds(t *testing.T) {
	p := &fakePainter{}
	m := NewMenu(p)
	items := []MenuItem{{Label: "Only"}, {Label: "Two"}}
	h := 2 * 28.0
	m.OpenWithin(items, 500, 500, [4]float64{100, 200, 300, 150})
	if m.x != 400-m.w || m.y != 350-h {
		t.Fatalf("x,y = %g,%g", m.x, m.y)
	}
	m.OpenWithin(items, 0, 0, [4]float64{100, 200, 300, 150})
	if m.x != 100 || m.y != 200 {
		t.Fatalf("x,y = %g,%g", m.x, m.y)
	}
	if x, y, w, hh := m.Rect(); x != 100 || y != 200 || w != m.w || hh != h {
		t.Fatal("Rect")
	}
	if got := m.Labels(); len(got) != 2 || got[0] != "Only" {
		t.Fatalf("labels = %v", got)
	}
	m.Close()
	if x, y, w, hh := m.Rect(); x != 0 || y != 0 || w != 0 || hh != 0 {
		t.Fatal("closed menus have no rect")
	}
}

func TestMenusInsideAScrollViewStayInTheVisiblePart(t *testing.T) {
	p := &fakePainter{}
	sv := NewScrollView(0, 0, 300, 500)
	sv.SetPainter(p)
	ps := docPages()
	ps.SetPainter(p)
	ps.Pages[0].PageNumber = &PageNumberConfig{Show: true, Start: 1}
	w, h := ps.Size()
	sv.Add(ps, w, h)
	sv.SetContentSize(w, h+1000) // tall enough to scroll to 50
	sv.SetContentMode(ModeEdit)
	sv.ScrollTo(0, 50)
	sv.Draw() // tells the content its visible part
	x, y, rw, rh, _ := ps.PageNumberRect(0)
	ps.MouseDown(x+rw/2, y+rh/2)
	mx, my, mw, mh := ps.Menu().Rect()
	if mx+mw > 300 || my < 50 || my+mh > 550 {
		t.Fatalf("menu %g,%g %gx%g is outside the visible part (0,50)-(300,550)", mx, my, mw, mh)
	}
}
