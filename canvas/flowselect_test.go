package canvas

import (
	"reflect"
	"testing"
)

// Single-page flow at (10, 30), 160x200, 8px runes, 22px lines.
func selFlow(items ...Item) (*TextFlow, *fakePainter) {
	p := &fakePainter{}
	f := NewTextFlow("", 10, 30, 160, 200)
	f.Items = items
	f.SetPainter(p)
	return f, p
}

func TestDragSelectsText(t *testing.T) {
	f, _ := selFlow(Chars{Text: "hello world\nsecond line"})
	if f.MouseDown(10+8*2, 40) {
		t.Fatal("a press on text with nothing selected needs no redraw")
	}
	if f.HasSelection() {
		t.Fatal("a plain press selects nothing yet")
	}
	if !f.MouseMove(10+8*8, 40) {
		t.Fatal("dragging changes the selection")
	}
	if from, to := f.Selection(); from != 2 || to != 8 || f.SelectedText() != "llo wo" {
		t.Fatalf("selection %d..%d %q", from, to, f.SelectedText())
	}
	if !f.WantsFocus() {
		t.Fatal("a selection needs the keyboard so Copy reaches it")
	}
	// Drag on across lines, and backwards.
	f.MouseMove(10+8*3, 62)
	if f.SelectedText() != "llo world\nsec" {
		t.Fatalf("across lines: %q", f.SelectedText())
	}
	f.MouseMove(10, 40)
	if from, to := f.Selection(); from != 0 || to != 2 {
		t.Fatalf("backwards: %d..%d", from, to)
	}
	if !f.MouseUp(0, 0) || f.MouseUp(0, 0) {
		t.Fatal("release ends the drag once")
	}
	if f.MouseMove(10+8*5, 40) {
		t.Fatal("after release, moves don't extend the selection")
	}
}

func TestSelectionIsDrawnAndWorksInViewMode(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello world"})
	f.MouseDown(10, 40)
	f.MouseMove(10+8*5, 40)
	p.calls = nil
	f.Draw()
	if !p.has("fillStyle rgba(124, 92, 255, 0.28)") || !p.has("fillRect 10 30 40 22") {
		t.Fatalf("selection tint: %v", p.calls)
	}
}

func TestPressElsewhereClearsTheSelection(t *testing.T) {
	f, _ := selFlow(Chars{Text: "hello"})
	f.SetSelection(1, 3)
	if !f.MouseDown(500, 500) || f.HasSelection() {
		t.Fatal("a press outside the page clears it (and asks for a redraw)")
	}
	f.SetSelection(1, 3)
	if !f.MouseDown(10, 40) || f.HasSelection() {
		t.Fatal("a new press starts afresh")
	}
	if f.MouseDown(500, 500) {
		t.Fatal("nothing to clear: no redraw")
	}
	f.SetSelection(1, 3)
	if !f.HandleKey("Escape", false) || f.HasSelection() || f.HandleKey("Escape", false) || f.HandleKey("a", false) {
		t.Fatal("Escape clears")
	}
}

func TestPressesOnObjectsAboveTheTextDontSelectIt(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello world hello world hello world"})
	box := NewFloatingText("note", 20, 30, 80, 40) // overlaps the first line; not wrapping
	box.SetPainter(p)
	box.SetFlow(f)
	box.SetMode(ModeEdit)
	f.MouseDown(30, 40) // on the box
	f.MouseMove(60, 40)
	if f.HasSelection() {
		t.Fatal("a press on a floating box must not start selecting the text under it")
	}
	// A wrapping image excludes its area too.
	img := NewImage(fakeImage{40, 20}, 100, 30)
	img.SetPainter(p)
	img.SetFlow(f)
	img.Wrap = true
	f.MouseDown(120, 40)
	f.MouseMove(150, 40)
	if f.HasSelection() {
		t.Fatal("nor on an image")
	}
	// Away from both it works.
	f.MouseDown(10, 100)
	f.MouseMove(10+8*3, 100)
	if !f.HasSelection() {
		t.Fatal("selecting elsewhere still works")
	}
	// A press on a table (Hitter) is kept away too.
	tbl := NewTableView(NewTable(1, 1), p)
	tbl.X, tbl.Y = 10, 150
	tbl.SetFlow(f)
	f.ClearSelection()
	f.MouseDown(20, 160)
	f.MouseMove(60, 160)
	if f.HasSelection() {
		t.Fatal("nor on a table")
	}
}

func TestOffsetAtPicksTheColumn(t *testing.T) {
	f := pagedFlow(ColumnConfig{Widths: []float64{80, 80}, Gap: 20}, Chars{Text: "left one\nleft two\nleft three\nright one\nright two"})
	// Page 0's text area: x 34..214, y 34..114 (three 22px lines per column).
	lay := f.Layout()
	if lay.Lines[3].Col != 1 || lay.Lines[3].ColX != 134 {
		t.Fatalf("lines = %+v", lay.Lines)
	}
	if o := f.OffsetAt(136, 40); o != lay.Lines[3].Start {
		t.Fatalf("a click at the top of column 1 lands in 'right one' (offset %d), got %d", lay.Lines[3].Start, o)
	}
	if o := f.OffsetAt(34, 40); o != 0 {
		t.Fatalf("top of column 0: %d", o)
	}
	// Selecting from column 0 into column 1 runs in reading order.
	f.MouseDown(34, 90)
	f.MouseMove(134+8*5+1, 40)
	if got := f.SelectedText(); got != "left three\nright" {
		t.Fatalf("selected %q", got)
	}
}

func TestDoubleClickSelectsAWord(t *testing.T) {
	f, _ := selFlow(Chars{Text: "foo_bar baz, qux"})
	if !f.DoubleClick(10+8*2, 40) || f.SelectedText() != "foo_bar" {
		t.Fatalf("selected %q", f.SelectedText())
	}
	f.DoubleClick(10+8*10, 40)
	if f.SelectedText() != "baz" {
		t.Fatalf("selected %q", f.SelectedText())
	}
	f.DoubleClick(10+8*7, 40) // the space
	if f.SelectedText() != " " {
		t.Fatalf("selected %q", f.SelectedText())
	}
	if f.DoubleClick(500, 500) {
		t.Fatal("outside the text")
	}
	empty, _ := selFlow()
	if empty.DoubleClick(12, 40) {
		t.Fatal("empty text")
	}
}

func TestSelectAllCopyAndCommands(t *testing.T) {
	f, _ := selFlow(Chars{Text: "alpha "}, Chars{Text: "beta", Format: Format{FontWeight: 700}})
	if f.HandleCommand(CmdSelectAll) {
		t.Fatal("a flow with no selection isn't focused: it declines")
	}
	if _, ok := f.CopyText(); ok {
		t.Fatal("nothing selected")
	}
	f.SetSelection(0, 1)
	if !f.HandleCommand(CmdSelectAll) || f.SelectedText() != "alpha beta" {
		t.Fatalf("select all: %q", f.SelectedText())
	}
	if text, ok := f.CopyText(); !ok || text != "alpha beta" {
		t.Fatalf("copy = %q", text)
	}
	f.SelectAll()
	if f.HandleCommand(CmdBold) || f.HandleCommand(CmdPasteFormat) {
		t.Fatal("a flow can't change its own content's formatting")
	}
	if f.HandleCommand(CmdCopyFormat) {
		t.Fatal("no format clipboard yet")
	}
	f.SetFormatClipboard(&FormatClipboard{})
	if !f.HandleCommand(CmdCopyFormat) {
		t.Fatal("copy format")
	}
	// "alpha " is plain and "beta" bold: nothing in common.
	if got, _ := f.formats.Get(); !got.IsZero() {
		t.Fatalf("copied %+v", got)
	}
	f.SetSelection(6, 10) // "beta"
	f.HandleCommand(CmdCopyFormat)
	if got, _ := f.formats.Get(); got != (Format{FontWeight: 700}) {
		t.Fatalf("copied %+v", got)
	}
	f.ClearSelection()
	if f.SelectedText() != "" || f.CommonFormat() != (Format{}) {
		t.Fatal("cleared")
	}
}

func TestCommonFormatOfASelectionSpanningFormats(t *testing.T) {
	f, _ := selFlow(
		Chars{Text: "aa", Format: Format{FontWeight: 700, Color: "#f00", Underline: true}},
		Chars{Text: "bb", Format: Format{FontWeight: 700, Color: "#00f", Underline: true}},
		Chars{Text: "cc", Format: Format{FontWeight: 700, Strike: true}},
	)
	f.SetSelection(0, 4)
	if got := f.CommonFormat(); got != (Format{FontWeight: 700, Underline: true}) {
		t.Fatalf("common = %+v", got)
	}
	f.SetSelection(0, 6)
	if got := f.CommonFormat(); got != (Format{FontWeight: 700}) {
		t.Fatalf("common = %+v", got)
	}
}

func TestSelectedTextSkipsInlineObjects(t *testing.T) {
	f, _ := selFlow(Chars{Text: "ab"}, Inline{Object: &fakeObj{w: 10, h: 10}}, Chars{Text: "cd"})
	f.SelectAll()
	if got := f.SelectedText(); got != "abcd" {
		t.Fatalf("selected %q", got)
	}
	if got := f.CommonFormat(); !got.IsZero() {
		t.Fatalf("common = %+v", got)
	}
}

func TestSelectionRangesAreClamped(t *testing.T) {
	f, _ := selFlow(Chars{Text: "abc"})
	f.SetSelection(-5, 99)
	if f.SelectedText() != "abc" || !reflect.DeepEqual([]int{0}, []int{0}) {
		t.Fatalf("selected %q", f.SelectedText())
	}
}

func TestStageDrivesFlowSelectionAndFormatCopyToFloatingText(t *testing.T) {
	p := &fakePainter{}
	flow := NewTextFlow("", 10, 130, 160, 100)
	flow.Items = []Item{Chars{Text: "plain "}, Chars{Text: "fancy", Format: Format{FontWeight: 700, Color: "#f00"}}}
	box := NewFloatingText("target text", 10, 10, 160, 60)
	st := NewStage(p, 400, 300, flow, box)
	st.SetMode(ModeEdit)
	// Select "fancy" in the flow and copy its format.
	st.MouseDown(10+8*6, 140)
	st.MouseMove(10+8*11, 140)
	st.MouseUp(10+8*11, 140)
	if flow.SelectedText() != "fancy" {
		t.Fatalf("selected %q", flow.SelectedText())
	}
	if !st.HandleChord("Mod+Alt+C") {
		t.Fatal("Ctrl+Alt+C copies the format")
	}
	if text, ok := st.Copy(); !ok || text != "fancy" {
		t.Fatalf("copy = %q", text)
	}
	// Select "target" in the floating text and paste the format onto it.
	st.MouseDown(box.X+box.Padding, box.Y+box.Padding+5)
	st.MouseUp(0, 0)
	box.HandleKey("End", true)
	box.SetMode(ModeEdit)
	box.caret, box.anchor = 6, 0
	if !st.HandleChord("Mod+Alt+V") {
		t.Fatal("Ctrl+Alt+V pastes the format")
	}
	if got := box.FormatAt(0); got != (Format{FontWeight: 700, Color: "#f00"}) || box.FormatAt(6) != (Format{}) {
		t.Fatalf("formats: %+v / %+v", got, box.FormatAt(6))
	}
	if flow.HasSelection() {
		t.Fatal("pressing in the floating box cleared the flow's selection")
	}
}
