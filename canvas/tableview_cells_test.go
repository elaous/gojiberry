package canvas

import (
	"strings"
	"testing"
)

func has(p *fakePainter, sub string) bool {
	for _, c := range p.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func mergedView() (*TableView, *fakePainter) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"A", "B", "C"}, {"d", "e", "f"}, {"g", "h", "i"}}), p)
	v.X, v.Y = 10, 10
	v.SetMode(ModeEdit)
	return v, p
}

// Columns are 80 wide (10/90/170/250) and rows 32 tall (10/42/74/106).
func TestMergedCellIsOneCellForHitsAndDrawing(t *testing.T) {
	v, p := mergedView()
	v.Table.MergeRange(0, 0, 1, 1)
	if got := v.HitTest(100, 60); got != (Hit{HitCell, 0, 0}) {
		t.Errorf("a click on a covered cell lands on its owner: %+v", got)
	}
	v.Draw()
	// The owner covers 2x2 cells: one fill of 160 x 64 at (10,10).
	if !has(p, "fillRect 10 10 160 64") {
		t.Errorf("no 160x64 fill; calls: %v", p.calls)
	}
	// And the covered cells are not drawn on their own.
	if has(p, "fillRect 90 10 80 32") || has(p, "fillRect 10 42 80 32") {
		t.Error("a covered cell was drawn")
	}
	v.edit(1, 1)
	if v.row != 0 || v.col != 0 {
		t.Errorf("editing a covered cell edits its owner: %d,%d", v.row, v.col)
	}
}

func TestTabAndEnterSkipCoveredCells(t *testing.T) {
	v, _ := mergedView()
	v.Table.MergeRange(0, 1, 1, 1) // B and e: a tall cell in column 1
	v.edit(0, 0)
	v.HandleKey("Tab", false)
	if v.row != 0 || v.col != 1 {
		t.Fatalf("tab to the merged cell: %d,%d", v.row, v.col)
	}
	v.HandleKey("Tab", false)
	if v.row != 0 || v.col != 2 {
		t.Errorf("tab past it goes to C: %d,%d", v.row, v.col)
	}
	v.HandleKey("Tab", true)
	v.HandleKey("Enter", false)
	if v.row != 2 || v.col != 1 {
		t.Errorf("enter from a two-row cell goes below it: %d,%d", v.row, v.col)
	}
	v.HandleKey("ArrowUp", false)
	if v.row != 0 || v.col != 1 {
		t.Errorf("up from below lands on the owner: %d,%d", v.row, v.col)
	}
}

func TestShiftArrowsSelectABlockAndMergeIt(t *testing.T) {
	v, _ := mergedView()
	v.edit(0, 0)
	v.HandleKey("ArrowDown", true)
	v.HandleKey("ArrowRight", true)
	r0, c0, r1, c1, ok := v.Range()
	if !ok || r0 != 0 || c0 != 0 || r1 != 1 || c1 != 1 {
		t.Fatalf("range %d,%d - %d,%d %v", r0, c0, r1, c1, ok)
	}
	var changes int
	v.OnChange = func(*Table) { changes++ }
	if !v.MergeSelection() || changes != 1 || len(v.Table.Merges) != 1 {
		t.Fatalf("merge: %v %d %+v", changes, changes, v.Table.Merges)
	}
	if v.Table.Cell(0, 0) != "A B d e" {
		t.Errorf("text %q", v.Table.Cell(0, 0))
	}
	if !v.SplitSelection() || len(v.Table.Merges) != 0 {
		t.Error("split")
	}
	if v.MergeSelection() {
		t.Error("one cell cannot be merged")
	}
}

func TestShiftClickExtendsFromTheActiveCell(t *testing.T) {
	v, _ := mergedView()
	v.edit(0, 0)
	v.MouseDownMods(100, 60, Mods{Shift: true}) // cell (1,1)
	if r0, c0, r1, c1, _ := v.Range(); r0 != 0 || c0 != 0 || r1 != 1 || c1 != 1 {
		t.Errorf("shift-click range %d,%d - %d,%d", r0, c0, r1, c1)
	}
	v.MouseDownMods(250-5, 90, Mods{}) // a plain click edits (and drops the block)
	if v.rng.on {
		t.Error("a plain click clears the block")
	}
}

func TestCellStylesAreDrawn(t *testing.T) {
	v, p := mergedView()
	v.edit(1, 1)
	v.StyleSelection(func(st *CellStyle) { st.Background, st.Bold, st.Align = "#ff0", true, AlignCenter })
	p.calls = nil
	v.Draw()
	if !has(p, "fillStyle #ff0") || !has(p, "font bold") || !has(p, "textAlign center") {
		t.Errorf("style not drawn: %v", p.calls)
	}
	if v.Table.StyleAt(1, 1).Background != "#ff0" || !v.Table.StyleAt(1, 1).Bold {
		t.Error("style not stored")
	}
}

func TestTableMenuOffersMergeSplitAndStyles(t *testing.T) {
	v, _ := mergedView()
	labels := func() map[string]MenuItem {
		m := map[string]MenuItem{}
		for _, it := range v.tableMenu() {
			m[strings.TrimLeft(strings.TrimPrefix(it.Label, "✓"), " ")] = it
		}
		return m
	}
	l := labels()
	if !l["Merge cells"].Disabled || !l["Split cell"].Disabled || !l["Cell bold"].Disabled {
		t.Error("with no active cell the cell items are off")
	}
	v.edit(0, 0)
	v.HandleKey("ArrowRight", true)
	l = labels()
	if l["Merge cells"].Disabled || !l["Split cell"].Disabled || l["Cell bold"].Disabled {
		t.Errorf("with a block selected: %+v", l)
	}
	l["Merge cells"].Action()
	l = labels()
	if !l["Merge cells"].Disabled && len(v.Table.Merges) != 1 || l["Split cell"].Disabled {
		t.Errorf("after merging: %+v", v.Table.Merges)
	}
}
