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
	if got, want := lineTexts(l), []string{"one", "", "two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lines = %q", got)
	}
	if l.Lines[2].Y != 30+2*22 || l.Lines[1].Start != 4 || l.Lines[1].End != 4 {
		t.Fatalf("blank line should take a line and know its offset: %+v", l.Lines)
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
	l := f.Layout()
	if len(l.Lines) != 1 || l.Lines[0].Text != "" || l.Overflow {
		t.Fatalf("empty text is one empty line (for a caret): %+v", l)
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
	for _, x := range []float64{100, 92} {
		if b, g := f.GripAt(x, 20); b != 0 || g != 0 {
			t.Fatalf("grip at x=%g: band %d gap %d", x, b, g)
		}
	}
	for _, pt := range [][2]float64{{100, 40}, {50, 20}, {100, 5}} {
		if b, g := f.GripAt(pt[0], pt[1]); b != -1 || g != -1 {
			t.Fatalf("(%g,%g) is not a grip: %d,%d", pt[0], pt[1], b, g)
		}
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
	lay := f.Layout()
	g := f.gripRect(lay.Bands[0], 1)
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

// --- pages, breaks, column configs, formats ---

// Two 200x100 pages with 10px margins: text areas are 180x80, which
// holds three 22px lines (66px). Page 0's area starts at y=34, page 1's
// at y=158. 180px is 22 fake runes.
func pagedFlow(items ...Item) *TextFlow {
	p := &fakePainter{}
	f := NewTextFlow("", 0, 0, 0, 0)
	ps := NewPageSet(
		Page{Width: 200, Height: 100, Margin: Insets{10, 10, 10, 10}},
		Page{Width: 200, Height: 100, Margin: Insets{10, 10, 10, 10}},
	)
	f.Pages, f.Items = ps, items
	f.SetPainter(p)
	ps.SetPainter(p)
	return f
}

func linesOnPage(l Layout, page int) []string {
	var out []string
	for _, ln := range l.Lines {
		if ln.Page == page {
			out = append(out, ln.Text)
		}
	}
	return out
}

func TestFlowContinuesOnNextPageWithNaturalBreak(t *testing.T) {
	f := pagedFlow(Chars{Text: "aaaa\nbbbb\ncccc\ndddd\neeee"})
	l := f.Layout()
	if got := linesOnPage(l, 0); !reflect.DeepEqual(got, []string{"aaaa", "bbbb", "cccc"}) {
		t.Fatalf("page 0 = %q", got)
	}
	if got := linesOnPage(l, 1); !reflect.DeepEqual(got, []string{"dddd", "eeee"}) {
		t.Fatalf("page 1 = %q", got)
	}
	if l.Lines[3].Y != 158 || l.Lines[3].X != 34 {
		t.Fatalf("first line on page 1 = %+v", l.Lines[3])
	}
	if len(l.Breaks) != 1 || l.Breaks[0] != (BreakMark{Kind: BreakNatural, Page: 0, Col: 0, Offset: 15}) {
		t.Fatalf("breaks = %+v", l.Breaks)
	}
	if l.Overflow || l.Pages != 2 {
		t.Fatalf("overflow=%v pages=%d", l.Overflow, l.Pages)
	}
}

func TestFlowOverflowsWhenPagesRunOut(t *testing.T) {
	f := pagedFlow(Chars{Text: "a\nb\nc\nd\ne\nf\ng"}) // 7 lines, room for 6
	l := f.Layout()
	if !l.Overflow || len(l.Lines) != 6 || l.End != 12 {
		t.Fatalf("overflow=%v lines=%d end=%d", l.Overflow, len(l.Lines), l.End)
	}
}

func TestFlowWithNoPagesOverflows(t *testing.T) {
	f := pagedFlow(Chars{Text: "x"})
	f.Pages.Pages = nil
	if l := f.Layout(); !l.Overflow || len(l.Lines) != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestUserPageBreak(t *testing.T) {
	f := pagedFlow(Chars{Text: "one"}, Break{Kind: BreakPage}, Chars{Text: "two"})
	l := f.Layout()
	if !reflect.DeepEqual(linesOnPage(l, 0), []string{"one"}) || !reflect.DeepEqual(linesOnPage(l, 1), []string{"two"}) {
		t.Fatalf("lines = %+v", l.Lines)
	}
	if l.Lines[1].Y != 158 {
		t.Fatalf("text after the break starts at the top of the next page: y=%g", l.Lines[1].Y)
	}
	if len(l.Breaks) != 1 || l.Breaks[0].Kind != BreakPage || l.Breaks[0].Page != 0 || l.Breaks[0].Offset != 3 {
		t.Fatalf("breaks = %+v (a user break is not also a natural one)", l.Breaks)
	}
}

func TestPageBreakOnTheLastPageOverflows(t *testing.T) {
	f := pagedFlow(Chars{Text: "a"}, Break{Kind: BreakPage}, Chars{Text: "b"}, Break{Kind: BreakPage}, Chars{Text: "c"})
	l := f.Layout()
	if !l.Overflow || len(l.Lines) != 2 {
		t.Fatalf("overflow=%v lines=%d", l.Overflow, len(l.Lines))
	}
}

func TestNaturalBreakInputItemIsIgnored(t *testing.T) {
	f := pagedFlow(Chars{Text: "one"}, Break{Kind: BreakNatural}, Chars{Text: "two"})
	l := f.Layout()
	if len(linesOnPage(l, 1)) != 0 || len(l.Breaks) != 0 {
		t.Fatalf("a natural break can't be forced: %+v", l)
	}
}

func TestUserColumnBreak(t *testing.T) {
	f := pagedFlow(
		ColumnConfig{Widths: []float64{80, 80}, Gap: 20},
		Chars{Text: "left"}, Break{Kind: BreakColumn}, Chars{Text: "right"},
	)
	l := f.Layout()
	if len(l.Lines) != 2 || l.Lines[0].Col != 0 || l.Lines[1].Col != 1 || l.Lines[1].Page != 0 {
		t.Fatalf("lines = %+v", l.Lines)
	}
	if l.Lines[1].X != 34+80+20 || l.Lines[1].Y != l.Lines[0].Y {
		t.Fatalf("right column starts at the top of column 1: %+v", l.Lines[1])
	}
	if l.Breaks[0].Kind != BreakColumn || l.Breaks[0].Col != 0 {
		t.Fatalf("breaks = %+v", l.Breaks)
	}
}

func TestColumnBreakInLastColumnActsAsPageBreak(t *testing.T) {
	f := pagedFlow(
		ColumnConfig{Widths: []float64{80, 80}, Gap: 20},
		Chars{Text: "a"}, Break{Kind: BreakColumn}, Chars{Text: "b"}, Break{Kind: BreakColumn}, Chars{Text: "c"},
	)
	l := f.Layout()
	if len(l.Lines) != 3 {
		t.Fatalf("lines = %+v", l.Lines)
	}
	c := l.Lines[2]
	if c.Page != 1 || c.Col != 0 || c.Y != 158 {
		t.Fatalf("c should be at the top of page 1's first column: %+v", c)
	}
}

func TestColumnConfigPersistsAcrossPages(t *testing.T) {
	// 2 columns of 80 (10 runes): text flows col 0, col 1, then page 1 col 0 ...
	text := "a\nb\nc\nd\ne\nf\ng"
	f := pagedFlow(ColumnConfig{Widths: []float64{80, 80}, Gap: 20}, Chars{Text: text})
	l := f.Layout()
	var got [][2]int
	for _, ln := range l.Lines {
		got = append(got, [2]int{ln.Page, ln.Col})
	}
	want := [][2]int{{0, 0}, {0, 0}, {0, 0}, {0, 1}, {0, 1}, {0, 1}, {1, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("(page,col) = %v, want %v", got, want)
	}
	if len(l.Bands) != 2 || l.Bands[0].Page != 0 || l.Bands[1].Page != 1 || l.Bands[1].Cfg != 0 || len(l.Bands[1].Xs) != 2 {
		t.Fatalf("bands = %+v: the new page keeps the column config", l.Bands)
	}
}

func TestColumnConfigChangeStartsABandBelow(t *testing.T) {
	f := pagedFlow(
		Chars{Text: "intro"},
		ColumnConfig{Widths: []float64{80, 80}, Gap: 20},
		Chars{Text: "left"},
		ColumnConfig{}, // back to one column
		Chars{Text: "outro"},
	)
	l := f.Layout()
	var texts []string
	for _, ln := range l.Lines {
		texts = append(texts, ln.Text)
	}
	if !reflect.DeepEqual(texts, []string{"intro", "left", "outro"}) {
		t.Fatalf("lines = %q", texts)
	}
	ys := []float64{34, 56, 78}
	for i, ln := range l.Lines {
		if ln.Y != ys[i] {
			t.Errorf("line %d y = %g, want %g", i, ln.Y, ys[i])
		}
	}
	if len(l.Bands) != 3 || l.Bands[0].Cfg != -1 || l.Bands[1].Cfg != 1 || l.Bands[2].Cfg != 3 {
		t.Fatalf("bands = %+v", l.Bands)
	}
	if len(l.Bands[1].Xs) != 2 || len(l.Bands[2].Xs) != 1 {
		t.Fatalf("bands = %+v", l.Bands)
	}
}

func TestNewBandStartsBelowTheTallestColumn(t *testing.T) {
	f := pagedFlow(
		ColumnConfig{Widths: []float64{80, 80}, Gap: 20},
		Chars{Text: "a"}, Break{Kind: BreakColumn}, Chars{Text: "b\nc"},
		ColumnConfig{},
		Chars{Text: "z"},
	)
	l := f.Layout()
	z := l.Lines[len(l.Lines)-1]
	if z.Text != "z" || z.Y != 34+2*22 { // the right column held two lines
		t.Fatalf("z = %+v", z)
	}
}

func TestColumnConfigTooWideIsScaledToFit(t *testing.T) {
	f := pagedFlow(ColumnConfig{Widths: []float64{300, 100}, Gap: 20}, Chars{Text: "x"})
	b := f.Layout().Bands[0]
	// Room is 180-20 = 160, split 3:1.
	if b.Widths[0] != 120 || b.Widths[1] != 40 || b.Xs[1] != 34+120+20 {
		t.Fatalf("band = %+v", b)
	}
	f = pagedFlow(ColumnConfig{Widths: []float64{100, 100}, Gap: 400}, Chars{Text: "x"})
	if b := f.Layout().Bands[0]; len(b.Widths) != 1 || b.Widths[0] != 180 {
		t.Fatalf("gaps leave no room: falls back to one column: %+v", b)
	}
}

func TestEmptyBandsAreNotReported(t *testing.T) {
	f := pagedFlow(ColumnConfig{Widths: []float64{80, 80}, Gap: 20}, ColumnConfig{}, Chars{Text: "x"})
	l := f.Layout()
	if len(l.Bands) != 1 || l.Bands[0].Cfg != 1 {
		t.Fatalf("bands = %+v", l.Bands)
	}
}

func TestCharFormats(t *testing.T) {
	f, _ := newFlow("", 400, 200)
	f.Items = []Item{
		Chars{Text: "plain "},
		Chars{Text: "bold", Format: Format{FontWeight: 700}},
		Chars{Text: " big", Format: Format{FontSize: 32, FontStyle: StyleItalic, Color: "#ff0000", FontFamily: "Georgia"}},
	}
	l := f.Layout()
	if len(l.Lines) != 1 {
		t.Fatalf("lines = %+v", l.Lines)
	}
	ln := l.Lines[0]
	if ln.Text != "plain bold big" || len(ln.Runs) != 3 {
		t.Fatalf("line = %+v", ln)
	}
	wantFonts := []string{"normal 400 16px sans-serif", "normal 700 16px sans-serif", "italic 400 32px Georgia"}
	for i, r := range ln.Runs {
		if r.Font != wantFonts[i] {
			t.Errorf("run %d font = %q, want %q", i, r.Font, wantFonts[i])
		}
	}
	if ln.Runs[2].Color != "#ff0000" || ln.Runs[0].Color != DefaultTextStyle().Color {
		t.Fatalf("colors = %+v", ln.Runs)
	}
	if ln.Runs[1].X != 48 || ln.Runs[2].X != 48+32 {
		t.Fatalf("run x = %g, %g", ln.Runs[1].X, ln.Runs[2].X)
	}
	if ln.H != 44 { // 22px leading at 16px scales to 44 at 32px
		t.Fatalf("line height = %g, want the tallest run's 44", ln.H)
	}
}

func TestMixedFormatWordWrapsAsOneWord(t *testing.T) {
	f, _ := newFlow("", 40, 200) // 5 runes per line
	f.Items = []Item{Chars{Text: "ab"}, Chars{Text: "cd", Format: Format{FontWeight: 700}}, Chars{Text: " ef"}}
	got := lineTexts(f.Layout())
	if !reflect.DeepEqual(got, []string{"abcd", "ef"}) {
		t.Fatalf("lines = %q: a word split across formats must stay together", got)
	}
}

func TestTallerLineMovesDownWhenItWouldNotFit(t *testing.T) {
	f := pagedFlow(
		Chars{Text: "a\nb"}, // 2 lines: y 34, 56; 12px remain on page 0
		Chars{Text: "\nBIG", Format: Format{FontSize: 32}},
	)
	f.Style = DefaultTextStyle()
	l := f.Layout()
	big := l.Lines[len(l.Lines)-1]
	if big.Text != "BIG" || big.Page != 1 || big.H != 44 {
		t.Fatalf("a 44px line doesn't fit in the 22px left: %+v", big)
	}
}

func TestDragEditsTheColumnConfigItsBandUses(t *testing.T) {
	f := pagedFlow(
		Chars{Text: "intro"},
		ColumnConfig{Widths: []float64{80, 80}, Gap: 20},
		Chars{Text: "x"},
	)
	f.SetMode(ModeEdit)
	// Band 1 starts at y=56 (below "intro"); its gap-0 grip is centred at x=34+80+10.
	lay := f.Layout()
	g := f.gripRect(lay.Bands[1], 0)
	if !f.MouseDown(g[0]+g[2]/2, g[1]+2) {
		t.Fatal("grip of a mid-page band")
	}
	f.MouseMove(g[0]+g[2]/2+30, g[1]+2)
	cfg := f.Items[1].(ColumnConfig)
	if !reflect.DeepEqual(cfg.Widths, []float64{110, 50}) || cfg.Gap != 20 {
		t.Fatalf("config = %+v", cfg)
	}
	if f.Columns != nil {
		t.Fatal("the base config is untouched")
	}
}

func TestCaretRectAndOffsetAt(t *testing.T) {
	f, _ := newFlow("ab cd\n\nef", 40, 200) // lines: "ab cd" is 40px = exactly fits; blank; "ef"
	x, y, h, ok := f.CaretRect(2)
	if !ok || x != 10+16 || y != 30 || h != 22 {
		t.Fatalf("caret(2) = %g,%g,%g ok=%v", x, y, h, ok)
	}
	if _, y, _, _ := f.CaretRect(6); y != 30+22 { // the blank line
		t.Fatalf("caret on the blank line y = %g", y)
	}
	if x, y, _, _ := f.CaretRect(7); x != 10 || y != 30+44 {
		t.Fatalf("caret(7) = %g,%g", x, y)
	}
	if x, y, _, _ := f.CaretRect(999); x != 10+16 || y != 30+44 {
		t.Fatalf("past the end clamps to the last line's end: %g,%g", x, y)
	}
	for _, tt := range []struct {
		x, y float64
		want int
	}{{10, 30, 0}, {10 + 17, 40, 2}, {10 + 100, 40, 5}, {0, 1000, 7}, {1000, 1000, 9}, {10 + 7, 60, 6}, {30, 0, 3}} {
		if got := f.OffsetAt(tt.x, tt.y); got != tt.want {
			t.Errorf("OffsetAt(%g,%g) = %d, want %d", tt.x, tt.y, got, tt.want)
		}
	}
}

func TestCaretOnEmptyFlow(t *testing.T) {
	f, _ := newFlow("", 100, 100)
	if x, y, h, ok := f.CaretRect(0); !ok || x != 10 || y != 30 || h != 22 {
		t.Fatalf("caret = %g,%g,%g ok=%v", x, y, h, ok)
	}
	f.Pages = NewPageSet()
	if _, _, _, ok := f.CaretRect(0); ok {
		t.Fatal("no pages: no caret")
	}
	if f.OffsetAt(1, 1) != 0 {
		t.Fatal("no lines: offset 0")
	}
}

func TestFormatResolveAndZero(t *testing.T) {
	if !(Format{}).IsZero() || (Format{FontWeight: 700}).IsZero() {
		t.Fatal("IsZero")
	}
	base := TextStyle{FontSize: 10, LineHeight: 20, FontWeight: 400}
	got := Format{FontSize: 20, FontWeight: 700, FontStyle: StyleItalic, Color: "red", FontFamily: "X"}.resolve(base)
	if got.FontSize != 20 || got.LineHeight != 40 || got.FontWeight != 700 || got.FontStyle != StyleItalic || got.Color != "red" || got.FontFamily != "X" {
		t.Fatalf("resolved = %+v", got)
	}
	if got := (Format{FontSize: 20}).resolve(TextStyle{FontSize: 10}); got.LineHeight != 0 {
		t.Fatal("an unset base line height stays unset (so it follows the size)")
	}
	if BreakPage.String() != "page" || BreakColumn.String() != "column" || BreakNatural.String() != "natural" {
		t.Fatal("BreakKind.String")
	}
}

func TestDrawPagedFlowDrawsEachRun(t *testing.T) {
	f := pagedFlow(Chars{Text: "ab "}, Chars{Text: "cd", Format: Format{FontWeight: 700, Color: "#00f"}})
	f.Draw()
	p := f.Painter.(*fakePainter)
	for _, want := range []string{`fillText "ab " 34`, `fillText "cd" 58`, "font normal 700 16px sans-serif", "fillStyle #00f"} {
		if !p.has(want) {
			t.Errorf("missing %q in %v", want, p.calls)
		}
	}
	if p.has("fillRect 0 0 0 0") {
		t.Fatal("a paged flow doesn't draw its own page; the PageSet does")
	}
}
