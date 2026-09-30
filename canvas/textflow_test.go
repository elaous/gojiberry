package canvas

import (
	"reflect"
	"testing"
)

// Fake runes are 8px wide; DefaultTextStyle has a 22px line.

func newFlow(text string, w, h float64) (*TextFlow, *fakePainter) {
	p := &fakePainter{}
	f := NewTextFlow(text, 10, 30, w, h)
	f.SetPainter(p)
	return f, p
}

func lineTexts(l Layout) []string {
	var out []string
	for _, ln := range l.Lines {
		out = append(out, ln.Text)
	}
	return out
}

func TestFlowWordWrap(t *testing.T) {
	f, _ := newFlow("aaa bbb ccc", 80, 200) // 10 runes per line
	l := f.Layout()
	if got, want := lineTexts(l), []string{"aaa bbb", "ccc"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
	if l.Overflow || l.End != 11 {
		t.Fatalf("overflow=%v end=%d", l.Overflow, l.End)
	}
	a, b := l.Lines[0], l.Lines[1]
	if a.X != 10 || a.Y != 30 || b.Y != 52 || a.W != 56 || a.Start != 0 || a.End != 7 || b.Start != 8 || b.End != 11 {
		t.Fatalf("lines = %+v", l.Lines)
	}
}

func TestFlowHardBreaksAndBlankLines(t *testing.T) {
	f, _ := newFlow("one\n\ntwo", 200, 200)
	l := f.Layout()
	if got, want := lineTexts(l), []string{"one", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q", got)
	}
	if l.Lines[1].Y != 30+2*22 {
		t.Fatalf("blank line should take a line: y=%g", l.Lines[1].Y)
	}
}

func TestFlowBreaksLongWords(t *testing.T) {
	f, _ := newFlow("abcdefghijkl", 40, 200) // 5 runes per line
	if got, want := lineTexts(f.Layout()), []string{"abcde", "fghij", "kl"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestFlowOverflowReportsWhereItStopped(t *testing.T) {
	f, _ := newFlow("aaaa bbbb cccc dddd", 40, 44) // 2 lines fit
	l := f.Layout()
	if !l.Overflow || len(l.Lines) != 2 || l.End != 10 {
		t.Fatalf("overflow=%v lines=%q end=%d", l.Overflow, lineTexts(l), l.End)
	}
}

func TestFlowEmptyText(t *testing.T) {
	f, _ := newFlow("", 100, 100)
	if l := f.Layout(); len(l.Lines) != 0 || l.Overflow {
		t.Fatalf("%+v", l)
	}
}

func TestFlowStyleMetricsAreUsed(t *testing.T) {
	f, p := newFlow("hi", 100, 100)
	f.Style = TextStyle{FontFamily: "Georgia", FontSize: 20, FontWeight: 700, FontStyle: StyleItalic, LineHeight: 40}
	f.Text = "a b c d e f g h i j k l m n o p"
	f.Width = 50
	l := f.Layout()
	if !p.has("font italic 700 20px Georgia") {
		t.Fatalf("font not set: %v", p.calls)
	}
	if len(l.Lines) < 2 || l.Lines[1].Y-l.Lines[0].Y != 40 {
		t.Fatalf("line height ignored: %+v", l.Lines)
	}
}

func TestFlowColumns(t *testing.T) {
	f, _ := newFlow("aaaa bbbb cccc dddd", 180, 22) // two 80px columns, 20 gap, one line each
	f.Gap = 20
	f.SetColumnCount(2)
	if !reflect.DeepEqual(f.Columns, []float64{80, 80}) {
		t.Fatalf("columns = %v", f.Columns)
	}
	l := f.Layout()
	if got, want := lineTexts(l), []string{"aaaa bbbb", "cccc dddd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q", got)
	}
	if l.Lines[0].Col != 0 || l.Lines[1].Col != 1 || l.Lines[1].X != 10+80+20 || l.Lines[1].Y != 30 {
		t.Fatalf("lines = %+v", l.Lines)
	}
	if l.Overflow {
		t.Fatal("everything fits")
	}
}

func TestFlowUnequalColumns(t *testing.T) {
	f, _ := newFlow("aaaaaaaa bbbb cccc", 200, 22)
	f.Gap = 20
	f.Columns = []float64{100, 60} // 100 fits "aaaaaaaa" (64) + not more; 60 fits "bbbb"
	l := f.Layout()
	if got, want := lineTexts(l), []string{"aaaaaaaa", "bbbb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q", got)
	}
	if !l.Overflow {
		t.Fatal("cccc has nowhere to go")
	}
}

func TestSetColumnCountMinimumOne(t *testing.T) {
	f, _ := newFlow("x", 100, 100)
	f.SetColumnCount(0)
	if !reflect.DeepEqual(f.Columns, []float64{100}) {
		t.Fatalf("columns = %v", f.Columns)
	}
}

// block is a fixed Obstacle: x0..x1 for bands overlapping y0..y1.
type block struct{ x0, x1, y0, y1 float64 }

func (b block) Exclusion(y0, y1 float64) (float64, float64, bool) {
	if y1 <= b.y0 || y0 >= b.y1 {
		return 0, 0, false
	}
	return b.x0, b.x1, true
}

func TestFlowStartsRightOfLeftObstacle(t *testing.T) {
	f, _ := newFlow("aaaa bbbb cccc dddd eeee", 200, 200)
	// Obstacle covers x 10..60 for the first two lines (y 30..74):
	// the free space is 60..210 (150px = 18 runes).
	f.AddObstacle(block{10, 60, 30, 74})
	l := f.Layout()
	if l.Lines[0].X != 60 {
		t.Fatalf("first line should start right of the obstacle: %+v", l.Lines[0])
	}
}

func TestFlowObstacleInMiddlePicksWiderSide(t *testing.T) {
	f, _ := newFlow("aaaa bbbb", 200, 200)
	f.AddObstacle(block{150, 180, 0, 500}) // left side 10..150 (140), right 180..210 (30)
	l := f.Layout()
	if l.Lines[0].X != 10 || l.Lines[0].Text != "aaaa bbbb" {
		t.Fatalf("lines = %+v", l.Lines)
	}
}

func TestFlowSkipsBandsTooNarrowForText(t *testing.T) {
	f, _ := newFlow("aaaa", 100, 200)
	f.AddObstacle(block{10, 100, 30, 52}) // leaves 10px: narrower than 2*fontSize
	l := f.Layout()
	if l.Lines[0].Y != 52 {
		t.Fatalf("text should drop below the obstacle: %+v", l.Lines[0])
	}
}

func TestFlowObstacleAddRemove(t *testing.T) {
	f, _ := newFlow("aaaa", 100, 200)
	b := &imgObstacle{}
	f.AddObstacle(b)
	f.AddObstacle(b)
	if len(f.obstacles) != 1 {
		t.Fatalf("duplicate not ignored: %d", len(f.obstacles))
	}
	f.RemoveObstacle(b)
	if len(f.obstacles) != 0 {
		t.Fatal("not removed")
	}
}

type imgObstacle struct{}

func (*imgObstacle) Exclusion(float64, float64) (float64, float64, bool) { return 0, 0, false }

// --- dragging column gaps ---

func dragFlow() (*TextFlow, *int) {
	f, _ := newFlow("aaaa bbbb", 180, 100)
	f.Gap = 20
	f.SetColumnCount(2)
	f.SetMode(ModeEdit)
	n := 0
	f.OnChange = func(*TextFlow) { n++ }
	return f, &n
}

// Gap 0's grip is centered at x = 10+80+10 = 100, above the page top
// (y = 30-2-16 .. 30-2).

func TestGripHitTest(t *testing.T) {
	f, _ := dragFlow()
	if f.GripAt(100, 20) != 0 || f.GripAt(92, 20) != 0 {
		t.Fatal("grip should be hit")
	}
	if f.GripAt(100, 40) != -1 || f.GripAt(50, 20) != -1 || f.GripAt(100, 5) != -1 {
		t.Fatal("only the grip is draggable")
	}
}

func TestDragGapResizesNeighbours(t *testing.T) {
	f, n := dragFlow()
	if !f.MouseDown(100, 20) || !f.Dragging() {
		t.Fatal("press on the grip starts a drag")
	}
	if !f.MouseMove(130, 20) || !reflect.DeepEqual(f.Columns, []float64{110, 50}) {
		t.Fatalf("columns = %v", f.Columns)
	}
	f.MouseMove(70, 20)
	if !reflect.DeepEqual(f.Columns, []float64{50, 110}) {
		t.Fatalf("columns = %v", f.Columns)
	}
	if !f.MouseUp(70, 20) || f.Dragging() || f.MouseUp(0, 0) {
		t.Fatal("release ends the drag exactly once")
	}
	if *n != 2 {
		t.Fatalf("OnChange fired %d times, want 2", *n)
	}
}

func TestDragClampsToMinimumColumnWidth(t *testing.T) {
	f, _ := dragFlow()
	f.MouseDown(100, 20)
	f.MouseMove(1000, 20)
	if !reflect.DeepEqual(f.Columns, []float64{120, 40}) {
		t.Fatalf("columns = %v", f.Columns)
	}
	if f.MouseMove(2000, 20) {
		t.Fatal("no change, no redraw")
	}
	f.MouseMove(-1000, 20)
	if !reflect.DeepEqual(f.Columns, []float64{40, 120}) {
		t.Fatalf("columns = %v", f.Columns)
	}
}

func TestDragDoesNotMutateSharedColumnsSlice(t *testing.T) {
	f, _ := dragFlow()
	shared := []float64{80, 80}
	f.Columns = shared
	f.MouseDown(100, 20)
	f.MouseMove(130, 20)
	if shared[0] != 80 {
		t.Fatal("caller's slice was modified")
	}
}

func TestDragThreeColumnsOnlyMovesNeighbours(t *testing.T) {
	f, _ := newFlow("x", 300, 100)
	f.Gap = 20
	f.SetColumnCount(3) // 86.67 each
	f.SetMode(ModeEdit)
	total := f.Columns[1] + f.Columns[2]
	first := f.Columns[0]
	g := f.gripRect(1)
	f.MouseDown(g[0]+g[2]/2, g[1]+2)
	f.MouseMove(g[0]+g[2]/2+20, g[1]+2)
	if f.Columns[0] != first || f.Columns[1]+f.Columns[2] != total {
		t.Fatalf("columns = %v", f.Columns)
	}
}

func TestFlowViewModeIgnoresDrag(t *testing.T) {
	f, _ := dragFlow()
	f.SetMode(ModeView)
	if f.MouseDown(100, 20) || f.MouseMove(100, 20) || f.Dragging() || f.CSSCursor() != "" {
		t.Fatal("view mode is read-only")
	}
}

func TestFlowSwitchingToViewCancelsDrag(t *testing.T) {
	f, _ := dragFlow()
	f.MouseDown(100, 20)
	if f.SetMode(ModeEdit) {
		t.Fatal("same mode is a no-op")
	}
	f.SetMode(ModeView)
	if f.Dragging() {
		t.Fatal("drag should be cancelled")
	}
}

func TestFlowHoverAndCursor(t *testing.T) {
	f, _ := dragFlow()
	if !f.MouseMove(100, 20) || f.CSSCursor() != "col-resize" {
		t.Fatal("hovering a grip shows col-resize")
	}
	if f.MouseMove(101, 20) {
		t.Fatal("no change")
	}
	if !f.MouseLeave() || f.CSSCursor() != "" || f.MouseLeave() {
		t.Fatal("leave clears hover")
	}
}

func TestFlowDrawViewVersusEdit(t *testing.T) {
	f, _ := dragFlow()
	p := f.Painter.(*fakePainter)
	f.SetMode(ModeView)
	f.Draw()
	if !p.has(`fillText "aaaa bbbb"`) {
		t.Fatalf("text not drawn: %v", p.calls)
	}
	if p.has("fillStyle #b9a9ff") {
		t.Fatal("view mode draws no guides")
	}
	f.SetMode(ModeEdit)
	p.calls = nil
	f.Draw()
	if !p.has("fillStyle #b9a9ff") || !p.has("fillRect 92 12 16 16") {
		t.Fatalf("edit mode should draw guides and grips: %v", p.calls)
	}
	f.MouseDown(100, 20)
	p.calls = nil
	f.Draw()
	if !p.has("fillStyle #7c5cff") {
		t.Fatal("active grip is highlighted")
	}
}
