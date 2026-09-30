package canvas

import (
	"testing"
)

// newView returns a 2x2 table view at (10,10). With the fake painter's
// 8px runes, MinColWidth (80) wins for short text, so columns are 80
// wide: col edges 10/90/170, rows 32 tall: row edges 10/42/74. The
// "+" bars are 22 thick, 4px away: column bar x in [174,196), row bar
// y in [78,100).
func newView() (*TableView, *fakePainter) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"A", "B"}, {"c", "d"}}), p)
	return v, p
}

func TestHitTest(t *testing.T) {
	v, _ := newView()
	tests := []struct {
		x, y float64
		want Hit
	}{
		{20, 20, Hit{HitCell, 0, 0}},
		{100, 20, Hit{HitCell, 0, 1}},
		{100, 60, Hit{HitCell, 1, 1}},
		{180, 40, Hit{Kind: HitAddColumn}},
		{50, 90, Hit{Kind: HitAddRow}},
		{172, 40, Hit{}}, // gap between table and bar
		{5, 20, Hit{}},
		{50, 200, Hit{}},
	}
	for _, tt := range tests {
		if got := v.HitTest(tt.x, tt.y); got != tt.want {
			t.Errorf("HitTest(%g,%g) = %+v, want %+v", tt.x, tt.y, got, tt.want)
		}
	}
}

func TestColumnWidthGrowsWithText(t *testing.T) {
	v, _ := newView()
	v.Table.SetCell(1, 0, "a long cell value here") // 22 runes * 8 + 20 = 196
	if got := v.HitTest(10+195, 60); got.Col != 0 {
		t.Fatalf("x=205 should still be col 0, got %+v", got)
	}
	if got := v.HitTest(10+197, 60); got.Col != 1 {
		t.Fatalf("x=207 should be col 1, got %+v", got)
	}
}

func TestClickPlusBarsAddColumnAndRow(t *testing.T) {
	v, _ := newView()
	changes := 0
	v.OnChange = func(*Table) { changes++ }

	v.MouseDown(180, 40)
	if v.Table.Cols() != 3 || v.Table.Rows() != 2 {
		t.Fatalf("after column click: %dx%d", v.Table.Rows(), v.Table.Cols())
	}
	if r, c, ok := v.Active(); !ok || r != 0 || c != 2 {
		t.Fatalf("active = %d,%d,%v, want header of new column", r, c, ok)
	}

	// Table is now 3 cols wide (right edge 250); row bar is below it.
	v.MouseDown(50, 90)
	if v.Table.Rows() != 3 {
		t.Fatalf("after row click: %d rows", v.Table.Rows())
	}
	if r, c, ok := v.Active(); !ok || r != 2 || c != 0 {
		t.Fatalf("active = %d,%d,%v, want first cell of new row", r, c, ok)
	}
	if changes != 2 {
		t.Fatalf("OnChange fired %d times, want 2", changes)
	}
}

func TestClickCellPlacesCaret(t *testing.T) {
	v, _ := newView()
	v.Table.SetCell(0, 0, "abcd") // text starts at x=20, 8px per rune
	v.MouseDown(20+8*2+1, 20)     // just past the 2nd rune's left edge
	if _, _, ok := v.Active(); !ok {
		t.Fatal("expected editing")
	}
	if v.Cursor() != 2 {
		t.Fatalf("cursor = %d, want 2", v.Cursor())
	}
	v.MouseDown(20+8*3+5, 20) // nearer the far side of the 4th rune
	if v.Cursor() != 4 {
		t.Fatalf("cursor = %d, want 4", v.Cursor())
	}
}

func TestClickOutsideStopsEditing(t *testing.T) {
	v, _ := newView()
	v.MouseDown(20, 20)
	v.MouseDown(400, 400)
	if _, _, ok := v.Active(); ok {
		t.Fatal("expected editing to stop")
	}
}

func TestTyping(t *testing.T) {
	v, _ := newView()
	if v.InsertText("x") {
		t.Fatal("InsertText with no active cell should do nothing")
	}
	v.MouseDown(70, 20) // right of the text: caret at the end
	v.InsertText("héy")
	v.HandleKey("ArrowLeft", false)
	v.InsertText("\n!")
	if got := v.Table.Cell(0, 0); got != "A"+"hé!y" {
		t.Fatalf("got %q", got)
	}
	v.HandleKey("Backspace", false)
	v.HandleKey("Home", false)
	v.HandleKey("Delete", false) // removes "A"
	if got := v.Table.Cell(0, 0); got != "héy" {
		t.Fatalf("got %q", got)
	}
	v.HandleKey("End", false)
	if v.Cursor() != 3 {
		t.Fatalf("cursor = %d", v.Cursor())
	}
}

func TestTabNavigationAddsRowAtEnd(t *testing.T) {
	v, _ := newView()
	v.MouseDown(20, 20) // (0,0)
	v.HandleKey("Tab", false)
	assertActive(t, v, 0, 1)
	v.HandleKey("Tab", false)
	assertActive(t, v, 1, 0)
	v.HandleKey("Tab", true)
	assertActive(t, v, 0, 1)
	v.HandleKey("Tab", false)
	v.HandleKey("Tab", false)
	v.HandleKey("Tab", false) // past last cell
	if v.Table.Rows() != 3 {
		t.Fatalf("rows = %d, want 3", v.Table.Rows())
	}
	assertActive(t, v, 2, 0)
}

func TestShiftTabAtFirstCellStays(t *testing.T) {
	v, _ := newView()
	v.MouseDown(20, 20)
	v.HandleKey("Tab", true)
	assertActive(t, v, 0, 0)
}

func TestEnterMovesDownAndAddsRow(t *testing.T) {
	v, _ := newView()
	v.MouseDown(100, 20) // (0,1)
	v.HandleKey("Enter", false)
	assertActive(t, v, 1, 1)
	v.HandleKey("Enter", false)
	assertActive(t, v, 2, 1)
	if v.Table.Rows() != 3 {
		t.Fatalf("rows = %d, want 3", v.Table.Rows())
	}
}

func TestArrowsCrossCells(t *testing.T) {
	v, _ := newView()
	v.Table.SetCell(0, 0, "ab")
	v.Table.SetCell(0, 1, "xy")
	v.MouseDown(100, 20) // (0,1)
	v.HandleKey("Home", false)
	v.HandleKey("ArrowLeft", false) // at start: into previous cell's end
	assertActive(t, v, 0, 0)
	if v.Cursor() != 2 {
		t.Fatalf("cursor = %d, want 2", v.Cursor())
	}
	v.HandleKey("ArrowRight", false) // at end: into next cell's start
	assertActive(t, v, 0, 1)
	if v.Cursor() != 0 {
		t.Fatalf("cursor = %d, want 0", v.Cursor())
	}
	v.HandleKey("ArrowDown", false)
	assertActive(t, v, 1, 1)
	v.HandleKey("ArrowDown", false) // last row: stays
	assertActive(t, v, 1, 1)
	v.HandleKey("ArrowUp", false)
	assertActive(t, v, 0, 1)
	v.HandleKey("End", false)
	v.HandleKey("ArrowRight", false)
	v.HandleKey("ArrowRight", false)
	v.HandleKey("ArrowRight", false) // reaches last cell and stops; never grows
	if v.Table.Rows() != 2 {
		t.Fatalf("rows = %d, arrows must not add rows", v.Table.Rows())
	}
}

func TestEscapeAndUnhandledKeys(t *testing.T) {
	v, _ := newView()
	if v.HandleKey("Tab", false) {
		t.Fatal("keys are ignored while not editing")
	}
	v.MouseDown(20, 20)
	if v.HandleKey("F5", false) {
		t.Fatal("unknown key must not be consumed")
	}
	v.HandleKey("Escape", false)
	if _, _, ok := v.Active(); ok {
		t.Fatal("Escape should stop editing")
	}
}

func TestHoverRevealsBars(t *testing.T) {
	v, p := newView()
	v.Draw()
	if p.has("fillText \"+\"") {
		t.Fatal("bars must be hidden when not hovered")
	}
	if !v.MouseMove(20, 20) {
		t.Fatal("entering the table should need a redraw")
	}
	if v.MouseMove(21, 21) {
		t.Fatal("moving within the same cell should not need a redraw")
	}
	p.calls = nil
	v.Draw()
	if !p.has("fillText \"+\"") {
		t.Fatal("bars should show while hovered")
	}
	// Moving across the gap to a bar keeps them visible.
	v.MouseMove(172, 40)
	if !v.inRegion {
		t.Fatal("gap should count as part of the hover region")
	}
	if !v.MouseLeave() {
		t.Fatal("leaving should need a redraw")
	}
	if v.MouseLeave() {
		t.Fatal("second leave is a no-op")
	}
}

func TestDrawPaintsCellsHeaderAndCaret(t *testing.T) {
	v, p := newView()
	v.MouseDown(70, 20) // caret at end of "A"
	p.calls = nil
	v.Draw()
	for _, want := range []string{
		`fillText "A" 20 26`, `fillText "d" 100 58`,
		"fillRect 10 10 80 32", "strokeRect 10.5 10.5 79 31",
		"moveTo 28.5 16", // caret after the 1 rune "A"
	} {
		if !p.has(want) {
			t.Errorf("missing draw call %q in %v", want, p.calls)
		}
	}
	if !p.has("fillText \"+\"") {
		t.Error("bars show while editing")
	}
	p.calls = nil
	v.SetCaretVisible(false)
	v.Draw()
	if p.has("moveTo") {
		t.Error("caret should be hidden in its off blink phase")
	}
}

func TestSize(t *testing.T) {
	v, _ := newView()
	w, h := v.Size()
	if w != 160+4+22 || h != 64+4+22 {
		t.Fatalf("Size = %g x %g", w, h)
	}
}

func assertActive(t *testing.T, v *TableView, row, col int) {
	t.Helper()
	if r, c, ok := v.Active(); !ok || r != row || c != col {
		t.Fatalf("active = (%d,%d,%v), want (%d,%d)", r, c, ok, row, col)
	}
}
