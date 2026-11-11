package canvas

import (
	"reflect"
	"testing"
)

// 8px runes, 22px lines, padding 8, toolbar x 100..332
func newFloat(text string) (*FloatingText, *fakePainter) {
	p := &fakePainter{}
	ft := NewFloatingText(text, 100, 100, 200, 100)
	ft.SetPainter(p)
	ft.SetMode(ModeEdit)
	return ft, p
}

func click(ft *FloatingText, i int) {
	ft.MouseDown(108+float64(i)*8, 112)
	ft.MouseUp(0, 0)
}

func TestFloatingBasics(t *testing.T) {
	ft := NewFloatingText("hello", 1, 2, 30, 40)
	if ft.Text() != "hello" || ft.Caret() != 5 || ft.Mode() != ModeView || ft.Editing() {
		t.Fatalf("%+v", ft)
	}
	if x, y, w, h := ft.Box(); x != 1 || y != 2 || w != 30 || h != 40 {
		t.Fatal("Box")
	}
	ft.SetText("é")
	if ft.Text() != "é" || ft.Caret() != 1 {
		t.Fatal("SetText")
	}
}

func TestFloatingItemsGroupEqualFormats(t *testing.T) {
	ft, _ := newFloat("abcd")
	ft.runes[1].f = Format{FontWeight: 700}
	ft.runes[2].f = Format{FontWeight: 700}
	want := []Item{Chars{Text: "a"}, Chars{Text: "bc", Format: Format{FontWeight: 700}}, Chars{Text: "d"}}
	if got := ft.Items(); !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %+v", got)
	}
	if ft.FormatAt(1).FontWeight != 700 || ft.FormatAt(-1) != (Format{}) || ft.FormatAt(9) != (Format{}) {
		t.Fatal("FormatAt")
	}
}

func TestFloatingViewModeIsReadOnly(t *testing.T) {
	ft, p := newFloat("hello")
	ft.SetMode(ModeView)
	if ft.MouseDown(120, 112) || ft.MouseMove(120, 112) || ft.InsertText("x") || ft.HandleKey("Enter", false) || ft.WantsFocus() || ft.CSSCursor() != "" {
		t.Fatal("view mode ignores input")
	}
	p.calls = nil
	ft.Draw()
	if !p.has(`fillText "hello"`) || !p.has("fillStyle #fff8dc") {
		t.Fatalf("view mode draws the box and text: %v", p.calls)
	}
	if p.has(`fillText "Move"`) || p.has("strokeStyle #7c5cff") {
		t.Fatal("view mode draws no chrome")
	}
}

func TestFloatingClickPlacesCaretAndStartsEditing(t *testing.T) {
	ft, _ := newFloat("hello world")
	click(ft, 3)
	if !ft.Editing() || ft.Caret() != 3 || !ft.WantsFocus() {
		t.Fatalf("editing=%v caret=%d", ft.Editing(), ft.Caret())
	}
	if from, to := ft.Selection(); from != to {
		t.Fatal("a click leaves no selection")
	}
	if ft.MouseDown(500, 500) != true || ft.Editing() {
		t.Fatal("pressing outside stops editing (and asks for a redraw)")
	}
	if ft.MouseDown(500, 500) {
		t.Fatal("nothing to redraw the second time")
	}
}

func TestFloatingTyping(t *testing.T) {
	ft, _ := newFloat("held")
	if ft.InsertText("x") {
		t.Fatal("not editing yet")
	}
	click(ft, 2)
	ft.InsertText("l\r")
	ft.InsertText("")
	if ft.Text() != "hel"+"ld" || ft.Caret() != 3 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	ft.HandleKey("End", false)
	ft.InsertText("o")
	if ft.Text() != "helldo" {
		t.Fatalf("text=%q", ft.Text())
	}
}

func TestFloatingBackspaceDeleteEnter(t *testing.T) {
	ft, _ := newFloat("abcd")
	click(ft, 2)
	ft.HandleKey("Backspace", false)
	if ft.Text() != "acd" || ft.Caret() != 1 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	ft.HandleKey("Delete", false)
	if ft.Text() != "ad" {
		t.Fatalf("text=%q", ft.Text())
	}
	ft.HandleKey("Home", false)
	ft.HandleKey("Backspace", false) // at the start: nothing
	ft.HandleKey("End", false)
	ft.HandleKey("Delete", false) // at the end: nothing
	if ft.Text() != "ad" {
		t.Fatalf("text=%q", ft.Text())
	}
	ft.HandleKey("ArrowLeft", false)
	ft.HandleKey("Enter", false)
	if ft.Text() != "a\nd" || ft.Caret() != 2 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	ft.sync()
	if lines := ft.inner.Layout().Lines; len(lines) != 2 {
		t.Fatalf("Enter makes a new line: %d lines", len(lines))
	}
}

func TestFloatingArrowsAndShiftSelection(t *testing.T) {
	ft, _ := newFloat("abcdef")
	click(ft, 2)
	ft.HandleKey("ArrowRight", true)
	ft.HandleKey("ArrowRight", true)
	if from, to := ft.Selection(); from != 2 || to != 4 || ft.Caret() != 4 {
		t.Fatalf("selection = %d..%d caret=%d", from, to, ft.Caret())
	}
	ft.HandleKey("ArrowLeft", false) // collapses to the start
	if from, to := ft.Selection(); from != 2 || to != 2 || ft.Caret() != 2 {
		t.Fatalf("selection = %d..%d caret=%d", from, to, ft.Caret())
	}
	ft.HandleKey("End", true)
	ft.HandleKey("ArrowRight", false) // collapses to the end
	if from, to := ft.Selection(); from != to || ft.Caret() != 6 {
		t.Fatalf("caret=%d", ft.Caret())
	}
	ft.HandleKey("Home", true)
	if from, to := ft.Selection(); from != 0 || to != 6 {
		t.Fatalf("select all: %d..%d", from, to)
	}
	ft.InsertText("Z")
	if ft.Text() != "Z" {
		t.Fatalf("typing replaces the selection: %q", ft.Text())
	}
	ft.HandleKey("ArrowLeft", false)
	ft.HandleKey("ArrowLeft", false) // clamps at 0
	if ft.Caret() != 0 {
		t.Fatal("clamps")
	}
	if ft.HandleKey("F5", false) {
		t.Fatal("unknown keys are not consumed")
	}
}

func TestFloatingUpDownKeepColumn(t *testing.T) {
	ft, _ := newFloat("abcd\nef\nghij")
	click(ft, 3)
	ft.HandleKey("ArrowDown", false) // "ef" is shorter: clamps to its end
	if ft.Caret() != 7 {
		t.Fatalf("caret = %d, want 7", ft.Caret())
	}
	ft.HandleKey("ArrowDown", false)
	if ft.Caret() < 8 || ft.Caret() > 12 {
		t.Fatalf("caret = %d should be on the third line", ft.Caret())
	}
	ft.HandleKey("ArrowUp", false)
	ft.HandleKey("ArrowUp", false)
	if ft.Caret() > 4 {
		t.Fatalf("caret = %d should be back on the first line", ft.Caret())
	}
	ft.HandleKey("ArrowUp", false) // already at the top
}

func TestFloatingEscapeStopsEditing(t *testing.T) {
	ft, _ := newFloat("abc")
	click(ft, 1)
	if !ft.HandleKey("Escape", false) || ft.Editing() || ft.HandleKey("a", false) {
		t.Fatal("Escape stops editing; keys are then ignored")
	}
}

func TestFloatingDragSelectsText(t *testing.T) {
	ft, _ := newFloat("hello world")
	ft.MouseDown(108+8, 112)
	if !ft.Dragging() {
		t.Fatal("press in the text starts a selection drag")
	}
	ft.MouseMove(108+8*5, 112)
	if from, to := ft.Selection(); from != 1 || to != 5 {
		t.Fatalf("selection = %d..%d", from, to)
	}
	if ft.MouseMove(108+8*5, 112) {
		t.Fatal("no change")
	}
	ft.MouseUp(0, 0)
	if ft.Dragging() {
		t.Fatal("released")
	}
}

func TestFloatingBoldItalicOnSelection(t *testing.T) {
	ft, _ := newFloat("abcdef")
	click(ft, 1)
	ft.HandleKey("ArrowRight", true)
	ft.HandleKey("ArrowRight", true) // selects "bc"
	n := 0
	ft.OnChange = func(*FloatingText) { n++ }
	ft.MouseDown(150, 80) // the B button
	if ft.FormatAt(1).FontWeight != 700 || ft.FormatAt(2).FontWeight != 700 || ft.FormatAt(0).FontWeight != 0 || ft.FormatAt(3).FontWeight != 0 {
		t.Fatalf("only the selection is bold: %+v", ft.runes)
	}
	want := []Item{Chars{Text: "a"}, Chars{Text: "bc", Format: Format{FontWeight: 700}}, Chars{Text: "def"}}
	if !reflect.DeepEqual(ft.Items(), want) {
		t.Fatalf("items = %+v", ft.Items())
	}
	if from, to := ft.Selection(); from != 1 || to != 3 || !ft.Editing() {
		t.Fatal("toolbar clicks keep the selection")
	}
	ft.MouseDown(150, 80) // again: back to regular
	if ft.FormatAt(1).FontWeight != 400 {
		t.Fatalf("toggled off: %+v", ft.FormatAt(1))
	}
	ft.MouseDown(190, 80) // I
	if ft.FormatAt(2).FontStyle != StyleItalic {
		t.Fatal("italic")
	}
	if n != 3 {
		t.Fatalf("OnChange fired %d times, want 3", n)
	}
	ft.MouseDown(190, 80)
	if ft.FormatAt(2).FontStyle != StyleNormal {
		t.Fatal("italic off")
	}
}

func TestFloatingChangeSize(t *testing.T) {
	ft, _ := newFloat("abc")
	click(ft, 0)
	ft.HandleKey("End", true) // select all
	ft.MouseDown(260, 80)     // A+
	if ft.FormatAt(0).FontSize != 18 {
		t.Fatalf("size = %g", ft.FormatAt(0).FontSize)
	}
	ft.MouseDown(220, 80) // A-
	ft.MouseDown(220, 80)
	if ft.FormatAt(0).FontSize != 14 {
		t.Fatalf("size = %g", ft.FormatAt(0).FontSize)
	}
	for range 10 {
		ft.ChangeSize(-2)
	}
	if ft.FormatAt(0).FontSize != 6 {
		t.Fatalf("minimum size = %g", ft.FormatAt(0).FontSize)
	}
}

func TestFloatingPendingFormatAppliesToNextTypedText(t *testing.T) {
	ft, _ := newFloat("ab")
	click(ft, 2)
	ft.MouseDown(150, 80) // B with no selection
	if ft.FormatAt(1).FontWeight != 0 {
		t.Fatal("nothing selected: existing text is unchanged")
	}
	ft.InsertText("c")
	if ft.FormatAt(2).FontWeight != 700 {
		t.Fatalf("typed text is bold: %+v", ft.runes)
	}
	ft.InsertText("d")
	if ft.FormatAt(3).FontWeight != 700 {
		t.Fatal("text typed after bold text stays bold")
	}
	ft.ToggleBold()
	ft.InsertText("e")
	if ft.FormatAt(4).FontWeight != 400 {
		t.Fatal("toggling off applies to the next text too")
	}
	click(ft, 0)
	ft.InsertText("Z")
	if ft.FormatAt(0).FontWeight != 0 {
		t.Fatal("clicking elsewhere drops the pending format")
	}
}

func TestFloatingTypingAtStartInheritsFirstCharFormat(t *testing.T) {
	ft, _ := newFloat("ab")
	ft.runes[0].f = Format{FontWeight: 700}
	click(ft, 0)
	ft.InsertText("x")
	if ft.FormatAt(0).FontWeight != 700 {
		t.Fatal("inherits from the first character at the start")
	}
	empty, _ := newFloat("")
	empty.MouseDown(112, 112)
	empty.InsertText("y")
	if empty.Text() != "y" || empty.FormatAt(0) != (Format{}) {
		t.Fatal("typing into an empty box")
	}
}

func TestFloatingMoveDragSnapsBothAxes(t *testing.T) {
	ft, _ := newFloat("hi")
	ft.BoundsW = 400
	if !ft.MouseDown(120, 80) || !ft.Dragging() { // the Move button
		t.Fatal("Move button starts a drag")
	}
	ft.MouseMove(120+13, 80+22)
	if ft.X != 115 || ft.Y != 120 {
		t.Fatalf("X,Y = %g,%g, want 115,120", ft.X, ft.Y)
	}
	if ft.MouseMove(120+14, 80+22) {
		t.Fatal("snapped to the same spot: no change")
	}
	ft.MouseMove(5000, 5000)
	if ft.X != 200 { // BoundsW - Width
		t.Fatalf("X = %g", ft.X)
	}
	ft.MouseMove(-5000, -5000)
	if ft.X != 0 || ft.Y != 0 {
		t.Fatalf("X,Y = %g,%g", ft.X, ft.Y)
	}
	ft.MouseUp(0, 0)
}

func TestFloatingResizeHandle(t *testing.T) {
	ft, _ := newFloat("hi")
	if !ft.MouseDown(300, 200) || !ft.Dragging() { // bottom-right handle
		t.Fatal("handle starts a resize")
	}
	ft.MouseMove(300+33, 200-47)
	if ft.Width != 235 || ft.Height != 55 {
		t.Fatalf("size = %gx%g, want 235x55", ft.Width, ft.Height)
	}
	ft.MouseMove(-1000, -1000)
	if ft.Width != 20 || ft.Height != 20 {
		t.Fatalf("minimum size = %gx%g", ft.Width, ft.Height)
	}
}

func TestFloatingTextBeyondTheBoxIsNotShown(t *testing.T) {
	ft, _ := newFloat("1\n2\n3\n4\n5\n6")
	ft.Height = 66 // 50px of text area: two lines
	ft.sync()
	if lay := ft.inner.Layout(); len(lay.Lines) != 2 || !lay.Overflow {
		t.Fatalf("lines = %d overflow=%v", len(lay.Lines), lay.Overflow)
	}
}

func TestFloatingWrapAroundText(t *testing.T) {
	p := &fakePainter{}
	flow := NewTextFlow("aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj", 0, 0, 400, 400)
	flow.SetPainter(p)
	ft := NewFloatingText("note", 300, 10, 100, 44)
	ft.SetPainter(p)
	ft.SetFlow(flow)
	if _, _, ok := ft.Exclusion(0, 400); ok {
		t.Fatal("no exclusion while Wrap is off")
	}
	ft.Wrap = true
	x0, x1, ok := ft.Exclusion(20, 30)
	if !ok || x0 != 292 || x1 != 408 {
		t.Fatalf("exclusion = %g %g %v", x0, x1, ok)
	}
	if _, _, ok := ft.Exclusion(100, 200); ok {
		t.Fatal("below the box")
	}
	if overlaps(flow.Layout(), 22, 292, 408, 2, 62) {
		t.Fatal("text should wrap around the floating box")
	}
	ft.SetMode(ModeEdit)
	ft.MouseDown(310, 5) // the Move button (toolbar clamped to y 0..22)
	ft.MouseMove(310-200, 5)
	if ft.X != 100 || overlaps(flow.Layout(), 22, 92, 208, 2, 62) {
		t.Fatalf("X = %g: text must avoid the moved box", ft.X)
	}
	ft.MouseUp(0, 0)
	ft.MouseDown(ft.X+284-100+5, max(ft.Y-17, 5))
	if ft.Wrap {
		t.Fatal("Wrap button toggles wrapping off")
	}
	f2 := NewTextFlow("x", 0, 0, 10, 10)
	ft.SetFlow(f2)
	if len(flow.obstacles) != 0 || len(f2.obstacles) != 1 || ft.Flow() != f2 {
		t.Fatal("SetFlow moves the obstacle")
	}
	ft.SetFlow(nil)
	if len(f2.obstacles) != 0 {
		t.Fatal("nil removes it")
	}
}

func TestFloatingDrawEditChrome(t *testing.T) {
	ft, p := newFloat("hello world")
	click(ft, 5)
	ft.HandleKey("Home", true) // select 0..5? caret at 0 anchor 5
	p.calls = nil
	ft.Draw()
	for _, want := range []string{
		`fillText "hello world"`, "fillStyle rgba(124, 92, 255, 0.28)", "fillRect 108 108 40 22", // selection of "hello"
		`fillText "Move"`, `fillText "B"`, `fillText "I"`, `fillText "A-"`, `fillText "A+"`, `fillText "Wrap"`,
		"fillRect 108 110 1.5 18", // caret at offset 0
		"fillRect 295 195 10 10",  // resize handle
	} {
		if !p.has(want) {
			t.Errorf("missing %q in %v", want, p.calls)
		}
	}
	ft.SetCaretVisible(false)
	p.calls = nil
	ft.Draw()
	if p.has("fillRect 108 110 1.5 18") {
		t.Fatal("caret hidden in its off phase")
	}
	if ft.SetCaretVisible(false) || !ft.SetCaretVisible(true) {
		t.Fatal("blink reports redraw only on change")
	}
}

func TestFloatingCursorAndHover(t *testing.T) {
	ft, _ := newFloat("hi")
	for _, tt := range []struct {
		x, y float64
		want string
	}{{150, 130, "text"}, {120, 80, "move"}, {300, 200, "nwse-resize"}, {150, 80, "pointer"}, {500, 500, ""}} {
		ft.MouseMove(tt.x, tt.y)
		if ft.CSSCursor() != tt.want {
			t.Errorf("cursor at (%g,%g) = %q, want %q", tt.x, tt.y, ft.CSSCursor(), tt.want)
		}
	}
	ft.MouseDown(120, 80)
	if ft.CSSCursor() != "grabbing" {
		t.Fatal("dragging the box")
	}
	ft.MouseUp(0, 0)
	ft.MouseDown(300, 200)
	if ft.CSSCursor() != "nwse-resize" {
		t.Fatal("resizing")
	}
	ft.MouseUp(0, 0)
	ft.MouseDown(150, 130)
	if ft.CSSCursor() != "text" {
		t.Fatal("selecting")
	}
	ft.MouseUp(0, 0)
	ft.MouseMove(150, 130)
	if !ft.MouseLeave() || ft.MouseLeave() {
		t.Fatal("leave clears hover once")
	}
}

func TestFloatingSwitchingToViewStopsEverything(t *testing.T) {
	ft, _ := newFloat("hello")
	click(ft, 2)
	ft.HandleKey("End", true)
	ft.SetMode(ModeView)
	if ft.Editing() || ft.Dragging() {
		t.Fatal("view mode stops editing")
	}
	if from, to := ft.Selection(); from != to {
		t.Fatal("selection cleared")
	}
	if ft.SetMode(ModeView) {
		t.Fatal("no-op")
	}
}
