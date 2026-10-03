package canvas

import "testing"

// newFloatTable returns a 2x2 table view at (10, 10) in edit mode (cells
// 80x32, so the table is x 10..170, y 10..74).
func newFloatTable() (*TableView, *fakePainter) {
	v, p := newView()
	return v, p
}

func TestTableExclusionOnlyWhileWrapping(t *testing.T) {
	v, _ := newFloatTable()
	if _, _, ok := v.Exclusion(0, 1000); ok {
		t.Fatal("no exclusion while Wrap is off")
	}
	v.Wrap = true
	x0, x1, ok := v.Exclusion(20, 30)
	if !ok || x0 != 2 || x1 != 178 {
		t.Fatalf("exclusion = %g..%g %v, want the table +/- 8", x0, x1, ok)
	}
	if _, _, ok := v.Exclusion(0, 2); ok {
		t.Fatal("above the table and its margin")
	}
	if _, _, ok := v.Exclusion(82, 90); ok {
		t.Fatal("below the table and its margin")
	}
	if x, y, w, h := v.Rect(); x != 10 || y != 10 || w != 160 || h != 64 {
		t.Fatalf("Rect = %g %g %g %g", x, y, w, h)
	}
}

func TestTextWrapsAroundAFloatingTable(t *testing.T) {
	p := &fakePainter{}
	f := NewTextFlow("aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj", 0, 0, 400, 300)
	f.SetPainter(p)
	v := NewTableView(NewTableFrom([][]string{{"a", "b"}, {"c", "d"}}), p)
	v.X, v.Y = 200, 0 // x 200..360, y 0..64
	v.SetFlow(f)
	if v.Flow() != f {
		t.Fatal("Flow")
	}
	if overlaps(f.Layout(), 22, 192, 368, 0, 72) == false {
		t.Fatal("without Wrap the text runs under the table")
	}
	v.Wrap = true
	if overlaps(f.Layout(), 22, 192, 368, 0, 72) {
		t.Fatal("with Wrap on the text keeps clear of the table (plus its margin)")
	}
	f2 := NewTextFlow("x", 0, 0, 10, 10)
	v.SetFlow(f2)
	if len(f.obstacles) != 0 || len(f2.obstacles) != 1 {
		t.Fatal("SetFlow moves the obstacle")
	}
	v.SetFlow(nil)
	if len(f2.obstacles) != 0 {
		t.Fatal("nil removes it")
	}
}

func TestTableAnchoredToAPage(t *testing.T) {
	ps := twoPages() // sheets at x=24, y=24 and y=148
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"a"}}), p)
	v.Pages = ps
	v.Anchor = &Anchor{Page: 1, X: 10, Y: 20}
	v.Wrap = true
	if x, y, _, _ := v.Rect(); x != 34 || y != 168 {
		t.Fatalf("table at %g,%g", x, y)
	}
	ps.Y = 50
	if _, y, _, _ := v.Rect(); y != 218 {
		t.Fatalf("the table follows its page: y=%g", y)
	}
	// Without any page set an anchor is ignored.
	loose := NewTableView(NewTable(1, 1), p)
	loose.Anchor = &Anchor{Page: 3}
	if x, _, _, _ := loose.Rect(); x != 36 {
		t.Fatalf("x = %g", x)
	}
	// The flow's pages are used too.
	f := pagedFlow(Chars{Text: "x"})
	v2 := NewTableView(NewTable(1, 1), p)
	v2.SetFlow(f)
	v2.Anchor = &Anchor{Page: 0, X: 5, Y: 6}
	if x, y, _, _ := v2.Rect(); x != 29 || y != 30 {
		t.Fatalf("table at %g,%g", x, y)
	}
}

func TestClickingTheTableIconStillSelectsTheTable(t *testing.T) {
	v, _ := newFloatTable()
	if !v.MouseDown(3, 3) || v.Selection().Kind != SelNone || v.Menu().IsOpen() {
		t.Fatal("the press alone decides nothing")
	}
	v.MouseMove(4, 4) // under the drag threshold
	if !v.MouseUp(4, 4) || v.Selection().Kind != SelTable || !v.Menu().IsOpen() {
		t.Fatalf("a click selects the table: sel=%+v open=%v", v.Selection(), v.Menu().IsOpen())
	}
	if v.Moving() || v.X != 10 || v.Y != 10 {
		t.Fatal("it didn't move")
	}
	if v.MouseUp(0, 0) {
		t.Fatal("no press in progress")
	}
}

func TestDraggingTheTableIconMovesTheTable(t *testing.T) {
	v, _ := newFloatTable()
	moves := 0
	v.OnMove = func(*TableView) { moves++ }
	v.MouseDown(3, 3)
	if v.MouseMove(5, 3) {
		t.Fatal("under the threshold nothing happens")
	}
	if !v.MouseMove(3+43, 3+22) || !v.Moving() {
		t.Fatal("past the threshold the table moves")
	}
	if v.X != 55 || v.Y != 30 { // 10+43=53 -> 55, 10+22=32 -> 30
		t.Fatalf("X,Y = %g,%g, want 55,30 (snapped to 5)", v.X, v.Y)
	}
	if v.MouseMove(3+44, 3+22) {
		t.Fatal("same snapped spot: no change")
	}
	v.MouseUp(0, 0)
	if v.Selection().Kind != SelNone || v.Menu().IsOpen() || v.Moving() {
		t.Fatal("a drag doesn't select or open the menu")
	}
	if moves != 1 {
		t.Fatalf("OnMove fired %d times", moves)
	}
	// Hit testing follows the table.
	if v.HitTest(60, 40).Kind != HitCell {
		t.Fatal("cells moved with the table")
	}
	// Snap can be turned off.
	v.Snap = 0
	v.MouseDown(55-7, 30-7) // the icon is now at X-20, Y-20
	v.MouseMove(55-7+3, 30-7+7)
	v.MouseMove(55-7+13, 30-7+17)
	if v.X != 55+13 || v.Y != 30+17 {
		t.Fatalf("unsnapped: %g,%g", v.X, v.Y)
	}
}

func TestDraggingAnAnchoredTableReanchorsAcrossPages(t *testing.T) {
	ps := twoPages()
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"a"}}), p)
	v.Pages = ps
	v.Anchor = &Anchor{Page: 0, X: 30, Y: 40}
	v.SetMode(ModeEdit)
	// The icon is at X-20, Y-20 = (34, 44).
	v.MouseDown(40, 50)
	v.MouseMove(40, 50+112) // the table's top goes from y=64 to y=176 -> page 1, y=28
	if v.Anchor.Page != 1 || v.Anchor.Y != 30 || v.Anchor.X != 30 {
		t.Fatalf("anchor = %+v: re-anchored to page 1 and snapped relative to it", *v.Anchor)
	}
	if x, y, _, _ := v.Rect(); x != 54 || y != 178 {
		t.Fatalf("table at %g,%g", x, y)
	}
	v.MouseUp(0, 0)
}

func TestTableMenuWrapToggle(t *testing.T) {
	v, _ := newFloatTable()
	clickTableHandle(v)
	for _, it := range v.Menu().items {
		if it.Label == "   Wrap text around" && !it.Disabled {
			t.Fatal("wrapping needs a flow")
		}
	}
	v.Menu().Close()
	p := &fakePainter{}
	f := NewTextFlow("x", 0, 0, 100, 100)
	f.SetPainter(p)
	v.SetFlow(f)
	clickTableHandle(v)
	clickMenuItemTrim(t, v, "Wrap text around")
	if !v.Wrap {
		t.Fatal("Wrap on")
	}
	clickTableHandle(v)
	found := false
	for _, it := range v.Menu().items {
		found = found || it.Label == "✓ Wrap text around"
	}
	if !found {
		t.Fatalf("the menu shows the toggle is on: %v", v.Menu().Labels())
	}
	clickMenuItemTrim(t, v, "Wrap text around")
	if v.Wrap {
		t.Fatal("Wrap off")
	}
}

func clickMenuItemTrim(t *testing.T, v *TableView, label string) {
	t.Helper()
	m := v.Menu()
	top := m.y
	for _, it := range m.items {
		if !it.Separator && (it.Label == label || it.Label == "   "+label || it.Label == "✓ "+label) {
			v.MouseDown(m.x+5, top+m.itemHeight(it)/2)
			return
		}
		top += m.itemHeight(it)
	}
	t.Fatalf("no item %q in %v", label, m.Labels())
}

func TestTableContainsPoint(t *testing.T) {
	v, _ := newFloatTable()
	if !v.ContainsPoint(50, 50) || v.ContainsPoint(300, 300) {
		t.Fatal("cells")
	}
	if !v.ContainsPoint(3, 3) { // the table icon, in edit mode
		t.Fatal("handles belong to the table in edit mode")
	}
	v.SetMode(ModeView)
	if v.ContainsPoint(3, 3) || !v.ContainsPoint(50, 50) {
		t.Fatal("in view mode only the cells")
	}
}
