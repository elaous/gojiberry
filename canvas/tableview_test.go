package canvas

import (
	"strings"
	"testing"
)

// newView returns a 2x2 table view at (10,10).
func newView() (*TableView, *fakePainter) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"A", "B"}, {"c", "d"}}), p)
	v.X, v.Y = 10, 10
	v.SetMode(ModeEdit)
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
		{8, 20, Hit{}},   // gap between table and row handles
		{3, 20, Hit{Kind: HitRowHandle}},
		{20, 3, Hit{Kind: HitColumnHandle}},
		{100, 3, Hit{Kind: HitColumnHandle, Col: 1}},
		{3, 60, Hit{Kind: HitRowHandle, Row: 1}},
		{3, 3, Hit{Kind: HitTableHandle}},
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

	// Table is now 3 columns wide.
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
	if p.has("moveTo 28.5 16") { // (the eye of the formula switch is a path too)
		t.Error("caret should be hidden in its off blink phase")
	}
}

func TestSize(t *testing.T) {
	v, _ := newView()
	w, h := v.Size()
	if w != 10+160+4+22 || h != 10+64+4+22 {
		t.Fatalf("Size = %g x %g", w, h)
	}
}

func assertActive(t *testing.T, v *TableView, row, col int) {
	t.Helper()
	if r, c, ok := v.Active(); !ok || r != row || c != col {
		t.Fatalf("active = (%d,%d,%v), want (%d,%d)", r, c, ok, row, col)
	}
}

func TestDefaultsToViewMode(t *testing.T) {
	v := NewTableView(NewTable(1, 1), &fakePainter{})
	if v.Mode() != ModeView || ModeView.String() != "view" || ModeEdit.String() != "edit" {
		t.Fatalf("mode = %v", v.Mode())
	}
}

func TestViewModeIsReadOnly(t *testing.T) {
	v, p := newView()
	v.MouseDown(70, 20)
	if !v.SetMode(ModeView) {
		t.Fatal("switching mode needs a redraw")
	}
	if v.SetMode(ModeView) {
		t.Fatal("same mode is a no-op")
	}
	if _, _, ok := v.Active(); ok {
		t.Fatal("leaving edit mode must stop editing")
	}
	if v.MouseMove(20, 20) || v.MouseDown(20, 20) || v.MouseDown(180, 40) {
		t.Fatal("pointer input must be ignored in view mode")
	}
	if v.InsertText("x") || v.HandleKey("Tab", false) {
		t.Fatal("keyboard input must be ignored in view mode")
	}
	if v.Table.Cols() != 2 || v.Table.Cell(0, 0) != "A" {
		t.Fatalf("table changed in view mode: %v", v.Table.Cells)
	}
	p.calls = nil
	v.Draw()
	if p.has(`fillText "+"`) || p.has("moveTo") || p.has("strokeStyle #7c5cff") {
		t.Fatalf("view mode drew edit affordances: %v", p.calls)
	}
	if !p.has(`fillText "A"`) {
		t.Fatal("view mode must still draw content")
	}
	v.SetMode(ModeEdit)
	v.MouseDown(20, 20)
	if _, _, ok := v.Active(); !ok {
		t.Fatal("edit mode works again after switching back")
	}
}

func TestNewTableViewReservesHandleGutter(t *testing.T) {
	v := NewTableView(NewTable(1, 1), &fakePainter{})
	if v.X < v.Style.HandleSize+v.Style.BarGap || v.Y < v.Style.HandleSize+v.Style.BarGap {
		t.Fatalf("origin (%g,%g) leaves no room for handles", v.X, v.Y)
	}
}

// clickMenuItem opens the menu and clicks the named item.
func clickMenuItem(t *testing.T, v *TableView, label string) {
	t.Helper()
	m := v.Menu()
	if !m.IsOpen() {
		t.Fatal("menu is not open")
	}
	top := m.y
	for i, it := range m.items {
		if !it.Separator && it.Label == label {
			if it.Disabled {
				t.Fatalf("%q is disabled", label)
			}
			v.MouseDown(m.x+5, top+m.itemHeight(m.items[i])/2)
			return
		}
		top += m.itemHeight(it)
	}
	t.Fatalf("no menu item %q", label)
}

func TestColumnHandleSelectsAndOpensMenu(t *testing.T) {
	v, p := newView()
	v.MouseDown(100, 3) // column 1's handle
	if v.Selection() != (Selection{SelColumn, 1}) || !v.Menu().IsOpen() {
		t.Fatalf("sel=%+v open=%v", v.Selection(), v.Menu().IsOpen())
	}
	p.calls = nil
	v.Draw()
	if !p.has("fillRect 90 10 80 64") || !p.has(`fillText "Delete column"`) {
		t.Fatalf("selection tint or menu not drawn: %v", p.calls)
	}
}

func TestColumnMenuActions(t *testing.T) {
	v, _ := newView()
	changes := 0
	v.OnChange = func(*Table) { changes++ }

	v.MouseDown(100, 3)
	clickMenuItem(t, v, "Insert column left")
	if v.Table.Cols() != 3 || v.Table.Cell(0, 2) != "B" || v.Menu().IsOpen() {
		t.Fatalf("got %v", v.Table.Cells)
	}

	v.MouseDown(20, 3) // column 0 ("A")
	clickMenuItem(t, v, "Move column right")
	if v.Table.Cell(0, 1) != "A" || v.Selection() != (Selection{SelColumn, 1}) {
		t.Fatalf("got %v sel=%+v", v.Table.Cells, v.Selection())
	}

	v.MouseDown(100, 3) // column 1 again
	clickMenuItem(t, v, "Delete column")
	if v.Table.Cols() != 2 || v.Selection().Kind != SelNone {
		t.Fatalf("got %v sel=%+v", v.Table.Cells, v.Selection())
	}
	if changes != 3 {
		t.Fatalf("OnChange fired %d times, want 3", changes)
	}
}

func TestColumnSortMenu(t *testing.T) {
	v, _ := newView()
	v.Table.SetCell(1, 0, "b")
	v.Table.AddRow()
	v.Table.SetCell(2, 0, "a")
	v.MouseDown(20, 3)
	clickMenuItem(t, v, "Sort A to Z")
	if v.Table.Cell(1, 0) != "a" || v.Table.Cell(2, 0) != "b" {
		t.Fatalf("got %v", v.Table.Cells)
	}
	v.MouseDown(20, 3)
	clickMenuItem(t, v, "Sort Z to A")
	if v.Table.Cell(1, 0) != "b" {
		t.Fatalf("got %v", v.Table.Cells)
	}
}

func TestRowMenuHeaderRestrictions(t *testing.T) {
	v, _ := newView()
	v.MouseDown(3, 20) // header row handle
	disabled := map[string]bool{}
	for _, it := range v.Menu().items {
		disabled[it.Label] = it.Disabled
	}
	for _, l := range []string{"Insert row above", "Move row up", "Move row down", "Duplicate row", "Delete row"} {
		if !disabled[l] {
			t.Errorf("%q should be disabled for the header", l)
		}
	}
	if disabled["Insert row below"] {
		t.Error("Insert row below should work on the header")
	}
	v.Menu().Close()

	v.MouseDown(3, 60) // body row 1 (the last row)
	clickMenuItem(t, v, "Duplicate row")
	if v.Table.Rows() != 3 || v.Table.Cell(2, 0) != "c" {
		t.Fatalf("got %v", v.Table.Cells)
	}
	v.MouseDown(3, 60)
	clickMenuItem(t, v, "Insert row above")
	if v.Table.Rows() != 4 || v.Table.Cell(1, 0) != "" || v.Table.Cell(2, 0) != "c" {
		t.Fatalf("got %v", v.Table.Cells)
	}
}

func TestRowMoveAndDelete(t *testing.T) {
	v, _ := newView()
	v.Table.AddRow()
	v.Table.SetCell(2, 0, "e")
	v.MouseDown(3, 90) // row 2
	clickMenuItem(t, v, "Move row up")
	if v.Table.Cell(1, 0) != "e" || v.Selection() != (Selection{SelRow, 1}) {
		t.Fatalf("got %v sel=%+v", v.Table.Cells, v.Selection())
	}
	v.MouseDown(3, 60)
	clickMenuItem(t, v, "Delete row")
	if v.Table.Rows() != 2 || v.Table.Cell(1, 0) != "c" {
		t.Fatalf("got %v", v.Table.Cells)
	}
}

func TestTableHandleMenu(t *testing.T) {
	v, p := newView()
	clickTableHandle(v)
	if v.Selection().Kind != SelTable {
		t.Fatalf("sel=%+v", v.Selection())
	}
	for _, it := range v.Menu().items {
		if it.Label == "Delete table" {
			t.Fatal("Delete table needs OnDeleteTable")
		}
	}
	clickMenuItem(t, v, "Clear contents")
	if v.Table.Cell(0, 0) != "" || v.Table.Cell(1, 1) != "" {
		t.Fatalf("got %v", v.Table.Cells)
	}
	clickTableHandle(v)
	clickMenuItem(t, v, "Add column")
	clickTableHandle(v)
	clickMenuItem(t, v, "Add row")
	if v.Table.Cols() != 3 || v.Table.Rows() != 3 {
		t.Fatalf("got %v", v.Table.Cells)
	}

	deleted := false
	v.OnDeleteTable = func() { deleted = true }
	clickTableHandle(v)
	p.calls = nil
	v.Draw()
	if !p.has(`fillText "Delete table"`) {
		t.Fatal("menu should offer Delete table")
	}
	clickMenuItem(t, v, "Delete table")
	if !deleted {
		t.Fatal("OnDeleteTable not called")
	}
}

func TestClickOutsideMenuClosesAndPassesThrough(t *testing.T) {
	v, _ := newView()
	v.MouseDown(100, 3)
	v.MouseDown(20, 20) // column 1's menu starts at x=90, so this is outside it
	if v.Menu().IsOpen() {
		t.Fatal("menu should close")
	}
	if _, _, ok := v.Active(); !ok {
		t.Fatal("the click should pass through and edit the cell")
	}
}

func TestMenuKeyboardAndSelectionEscape(t *testing.T) {
	v, _ := newView()
	v.MouseDown(20, 3)
	if !v.HandleKey("ArrowDown", false) || !v.HandleKey("Enter", false) { // Insert column left
		t.Fatal("menu should consume keys")
	}
	if v.Table.Cols() != 3 {
		t.Fatalf("got %v", v.Table.Cells)
	}
	v.MouseDown(20, 3)
	v.HandleKey("Escape", false)
	if v.Menu().IsOpen() || v.Selection().Kind != SelColumn {
		t.Fatal("Escape closes the menu but keeps the selection")
	}
	if !v.HandleKey("Escape", false) || v.Selection().Kind != SelNone {
		t.Fatal("second Escape clears the selection")
	}
	if v.HandleKey("Escape", false) {
		t.Fatal("nothing left to escape from")
	}
}

func TestCellClickClearsSelection(t *testing.T) {
	v, _ := newView()
	v.MouseDown(20, 3)
	v.Menu().Close()
	v.MouseDown(100, 60)
	if v.Selection().Kind != SelNone {
		t.Fatal("clicking a cell clears the selection")
	}
}

func TestViewModeClearsSelectionAndMenu(t *testing.T) {
	v, p := newView()
	v.MouseDown(20, 3)
	v.SetMode(ModeView)
	if v.Selection().Kind != SelNone || v.Menu().IsOpen() {
		t.Fatal("view mode must clear selection and menu")
	}
	p.calls = nil
	v.Draw()
	if p.has("fillRect 70 19") || p.has("rgba") {
		t.Fatal("view mode must not draw handles or selection")
	}
}

func TestHandlesVisibleOnHover(t *testing.T) {
	v, p := newView()
	v.Draw()
	if p.has(`fillText "A" 50 -2`) {
		t.Fatal("handles must be hidden when not hovered")
	}
	v.MouseMove(3, 3) // over the table handle counts as hovering the table
	p.calls = nil
	v.Draw()
	if !p.has(`fillText "A" 50 -2`) || !p.has(`fillText "B" 130 -2`) || !p.has(`fillText "2" -2 58`) {
		t.Fatalf("grips not drawn with their names: %v", p.calls)
	}
}

func TestEdges(t *testing.T) {
	v, _ := newView()
	if got := v.ColumnEdges(); len(got) != 3 || got[0] != 10 || got[2] != 170 {
		t.Fatalf("ColumnEdges = %v", got)
	}
	if got := v.RowEdges(); len(got) != 3 || got[0] != 10 || got[2] != 74 {
		t.Fatalf("RowEdges = %v", got)
	}
}

// clickTableHandle presses and releases the table handle.
func clickTableHandle(v *TableView) {
	v.MouseDown(3, 3)
	v.MouseUp(3, 3)
}

func TestHandlesAreGripPills(t *testing.T) {
	v, p := newView()
	v.Style = DarkTableStyle()
	v.MouseMove(3, 3) // hovering the table shows its handles
	p.calls = nil
	v.Draw()
	var names, pillRows int
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillText") && strings.Contains(c, "-2") {
			names++ // the grips sit outside the table, at negative coordinates in this view
		}
		if strings.HasPrefix(c, "fillRect") && strings.HasSuffix(c, " 1") {
			pillRows++
		}
	}
	if names != 4 {
		t.Fatalf("grip names drawn: %d", names)
	}
	if pillRows < 8*4 {
		t.Fatalf("the handles are rounded pills: %d rows", pillRows)
	}
	if DarkTableStyle().CellBG == DefaultTableStyle().CellBG {
		t.Fatal("dark style differs")
	}
	v.Style.MinColWidth = 12
	v.MouseDown(40, -2)
	v.Draw()
}

func TestRenderCellDrawsTheContentOfACell(t *testing.T) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"Name", "Done"}, {"task", "70"}}), p)
	var asked []string
	v.RenderCell = func(pp Painter, r, c int, text string, x, y, w, h float64) bool {
		asked = append(asked, text)
		if c == 1 && r == 1 { // a bar, not the number
			pp.SetFillStyle("#00aa00")
			pp.FillRect(x, y, w*0.7, h)
			return true
		}
		return false
	}
	v.Draw()
	if len(asked) != 4 {
		t.Fatalf("every cell is offered: %v", asked)
	}
	if !p.has("fillStyle #00aa00") {
		t.Error("the bar is drawn")
	}
	if p.has(`fillText "70"`) {
		t.Error("the text of a cell that was rendered is not written as well")
	}
	if !p.has(`fillText "task"`) || !p.has(`fillText "Name"`) {
		t.Error("the other cells keep their text")
	}
}

func TestFormulaCellsShowTheirValue(t *testing.T) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"2", "3", "=A1+B1"}}), p)
	var asked []string
	v.RenderCell = func(pp Painter, r, c int, text string, x, y, w, h float64) bool {
		asked = append(asked, text)
		return false
	}
	v.Draw()
	if len(asked) != 3 || asked[2] != "5" {
		t.Fatalf("the value is what is drawn: %q", asked)
	}
	if v.Table.Cell(0, 2) != "=A1+B1" {
		t.Fatal("the formula stays in the cell")
	}
}

func TestFormulaSwitchInTheTopRightCorner(t *testing.T) {
	v, p := newView()
	v.MouseMove(3, 3)
	g := v.layout()
	hs, gap := v.Style.HandleSize, v.Style.BarGap
	x, y := g.right()+gap+hs/2, v.Y-gap-hs/2
	if h := v.HitTest(x, y); h.Kind != HitFormulaSwitch {
		t.Fatalf("the corner is the switch: %+v", h)
	}
	p.calls = nil
	v.Draw()
	if p.has(`fillText "f(x)"`) {
		t.Fatal("an eye while the values are shown")
	}
	v.MouseDown(x, y)
	v.MouseUp(x, y)
	if !v.ShowFormulas {
		t.Fatal("pressing it shows the formulas")
	}
	p.calls = nil
	v.MouseMove(3, 3)
	v.Draw()
	if !p.has(`fillText "f(x)"`) {
		t.Fatal("f(x) while the formulas are shown")
	}
	v.MouseDown(x, y)
	v.MouseUp(x, y)
	if v.ShowFormulas {
		t.Fatal("pressing it again shows the values")
	}
}

func formulaView() (*TableView, *fakePainter) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"2", "=A1*5"}, {"", ""}}), p)
	v.SetMode(ModeEdit)
	return v, p
}

func TestFormulaViewOpensOnCalculatedAndFormulaCellsAreReadOnlyThere(t *testing.T) {
	v, _ := formulaView()
	if v.ShowFormulas {
		t.Fatal("a table opens on the calculated view")
	}
	g := v.layout()
	cx, cy, cw, ch := g.cellRect(v.Table, 0, 1)
	v.MouseDown(cx+cw/2, cy+ch/2)
	v.InsertText("9")
	v.HandleKey("Backspace", false)
	v.HandleKey("Delete", false)
	if v.Table.Cell(0, 1) != "=A1*5" {
		t.Fatalf("the formula cell cannot be changed in the calculated view: %q", v.Table.Cell(0, 1))
	}
	v.HandleKey("Tab", false) // but it can be moved through
	if r, c, _ := v.Active(); r != 1 || c != 0 {
		t.Fatalf("Tab moves on: %d %d", r, c)
	}
	v.InsertText("=A1")
	v.InsertText("+1")
	if v.Table.Cell(1, 0) != "=A1+1" {
		t.Fatalf("typing a new formula: %q", v.Table.Cell(1, 0))
	}
	v.SetShowFormulas(true)
	v.MouseDown(cx+cw/2, cy+ch/2)
	v.InsertText("0")
	if v.Table.Cell(0, 1) == "=A1*5" {
		t.Fatalf("in the formula view the cell changes: %q", v.Table.Cell(0, 1))
	}
}

func TestFormulaTooltipAndColours(t *testing.T) {
	v, p := formulaView()
	v.SetMode(ModeView)
	g := v.layout()
	cx, cy, cw, ch := g.cellRect(v.Table, 0, 1)
	v.MouseMove(cx+cw/2, cy+ch/2)
	p.calls = nil
	v.Draw()
	if !p.has(`fillText "=A1*5"`) {
		t.Fatalf("the tooltip shows the formula: %v", p.calls)
	}
	if !p.has("fillStyle " + DefaultTableStyle().ValueColor) {
		t.Fatal("the calculated value is blue")
	}
	v.MouseMove(5, 5000)
	p.calls = nil
	v.Draw()
	if p.has(`fillText "=A1*5"`) {
		t.Fatal("no tooltip away from the cell")
	}
	v.SetShowFormulas(true)
	p.calls = nil
	v.Draw()
	if !p.has("fillStyle "+DefaultTableStyle().FormulaColor) || !p.has(`fillText "=A1*5"`) {
		t.Fatal("the formula is red in the formula view")
	}
	if DarkTableStyle().ValueColor == DefaultTableStyle().ValueColor {
		t.Fatal("the dark style has its own colours")
	}
}

func TestPasteIntoATableFillsCellsFromTheActiveOne(t *testing.T) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"A", "B"}, {"c", "d"}}), p)
	v.SetMode(ModeEdit)
	g := v.layout()
	cx, cy, cw, ch := g.cellRect(v.Table, 1, 0)
	v.MouseDown(cx+cw/2, cy+ch/2) // the cell of the second row, first column
	if !v.PasteText("| Qty | Total |\n|---|--:|\n| 3 | =A3*2 |") {
		t.Fatal("pasted")
	}
	tb := v.Table
	if tb.Rows() != 3 || tb.Cols() != 2 || tb.Cell(1, 0) != "Qty" || tb.Cell(2, 0) != "3" || tb.Cell(2, 1) != "=A3*2" {
		t.Fatalf("a Markdown table from the active cell, growing the rows: %q", tb.Cells)
	}
	if tb.Cell(0, 0) != "A" {
		t.Fatal("cells above the active one are kept")
	}
	if !v.PasteText("x\ty\tz\n1\t2\t3\n") {
		t.Fatal("pasted tab-separated")
	}
	if tb.Cols() != 3 || tb.Cell(1, 2) != "z" || tb.Cell(2, 0) != "1" {
		t.Fatalf("tabs: %q", tb.Cells)
	}
	// plain text goes in as typing, on one line
	v.HandleKey("End", false)
	if !v.PasteText("end\nof it") || !strings.HasSuffix(tb.Cell(1, 0), "endof it") {
		t.Fatalf("one line: %q", tb.Cell(1, 0))
	}
	tb.SetCell(0, 0, "=1+1")
	v.editing = false
	v.edit(0, 0)
	if v.PasteText("zzz") || tb.Cell(0, 0) != "=1+1" {
		t.Fatalf("the locked cell stays: %q", tb.Cell(0, 0))
	}
}
