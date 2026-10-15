package canvas

import (
	"strings"
	"testing"
)

// verticalFlow is vertical text in a text area 160 wide and 80 tall at (10, 30): a column holds ten letters of
// 8px, the columns follow one another to the left from x = 170.
func verticalFlow(text string) (*TextFlow, *fakePainter) {
	p := &fakePainter{}
	f := NewTextFlow(text, 10, 30, 160, 80)
	f.Vertical = true
	f.SetPainter(p)
	return f, p
}

// startEditing puts a vertical flow in edit mode with the caret in it, by a click on the first column.
func startEditing(f *TextFlow) {
	f.SetMode(ModeEdit)
	NewStage(f.Painter.(*fakePainter), 400, 400, f)
	f.MouseDown(166, 35)
	f.MouseUp(0, 0)
	f.SetCaretVisible(true)
}

func TestVerticalLinesAreColumns(t *testing.T) {
	f, _ := verticalFlow(strings.Repeat("x", 25))
	lay := f.Layout()
	if got := lineTexts(lay); len(got) != 3 || len(got[0]) != 10 || len(got[2]) != 5 {
		t.Fatalf("columns %q: ten letters fit in 80px", got)
	}
}

func TestVerticalTextIsDrawnTurned(t *testing.T) {
	f, p := verticalFlow("abc")
	f.Draw()
	// the text area's top right corner is the origin, the plane turned a quarter turn clockwise
	want := []string{"save", "translate 170 30", "rotate 1.571", "translate -10 0"}
	var got []string
	for _, c := range p.calls {
		for _, w := range want {
			if c == w {
				got = append(got, c)
			}
		}
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("transform calls %q, want %q (all calls %q)", got, want, p.calls)
	}
}

func TestVerticalIdeographsStandUpright(t *testing.T) {
	f, p := verticalFlow("a日本")
	f.Draw()
	var seq []string
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillText") || c == "rotate -1.571" {
			seq = append(seq, c)
		}
	}
	// "a" along the line; each ideograph turned back about its centre
	joined := strings.Join(seq, "\n")
	for _, want := range []string{`fillText "a"`, "rotate -1.571", `fillText "日" 0 0`, `fillText "本" 0 0`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `"日本"`) {
		t.Error("ideographs are drawn one by one, not as a string turned with the line")
	}
}

// A point on the screen and the offset it is at agree with the caret box: clicking where the caret is finds the
// offset.
func TestVerticalCaretAndClicksAgree(t *testing.T) {
	f, _ := verticalFlow(strings.Repeat("x", 25))
	startEditing(f)
	var prev struct{ x, y float64 }
	for _, o := range []int{0, 1, 5, 11, 20, 24} {
		f.SetCursors([]Cursor{At(o)})
		x, y, _, ok := f.CaretBox()
		if !ok {
			t.Fatalf("no caret box at %d", o)
		}
		if got := f.OffsetAt(x, y); got != o {
			t.Errorf("the caret of %d is at (%v, %v); a click there finds %d", o, x, y, got)
		}
		if o == 1 && !(y > prev.y && x == prev.x) {
			t.Errorf("the second letter is below the first: %v,%v after %v,%v", x, y, prev.x, prev.y)
		}
		if o == 0 {
			prev.x, prev.y = x, y
		}
	}
	// the second column is to the left of the first
	f.SetCursors([]Cursor{At(0)})
	x0, _, _, _ := f.CaretBox()
	f.SetCursors([]Cursor{At(11)})
	x10, y10, _, _ := f.CaretBox()
	if !(x10 < x0) {
		t.Errorf("the second column is at x %v, not left of the first at %v", x10, x0)
	}
	if y10 > 40 {
		t.Errorf("the second column starts at the top again: y %v", y10)
	}
}

// Down the column is forward along the line, left is the next column.
func TestVerticalArrowKeys(t *testing.T) {
	f, _ := verticalFlow(strings.Repeat("x", 25))
	startEditing(f)
	f.SetCursors([]Cursor{At(3)})
	f.HandleKey("ArrowDown", false)
	if c := f.Cursors()[0].Caret; c != 4 {
		t.Errorf("down moved to %d, want 4", c)
	}
	f.HandleKey("ArrowUp", false)
	if c := f.Cursors()[0].Caret; c != 3 {
		t.Errorf("up moved to %d, want 3", c)
	}
	f.HandleKey("ArrowLeft", false)
	if c := f.Cursors()[0].Caret; c != 13 {
		t.Errorf("left moved to %d, want 13 (the same place in the next column)", c)
	}
	f.HandleKey("ArrowRight", false)
	if c := f.Cursors()[0].Caret; c != 3 {
		t.Errorf("right moved to %d, want 3", c)
	}
}

func TestHorizontalFlowIsUnchangedByTheVerticalCode(t *testing.T) {
	f, p := newFlow("abc", 160, 80)
	f.Draw()
	if p.has("rotate") {
		t.Error("a horizontal flow was turned")
	}
	if x, y, _, _ := f.CaretBox(); x != 0 || y != 0 {
		t.Errorf("caret box %v %v", x, y)
	}
}
