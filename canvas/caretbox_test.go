package canvas

import (
	"strings"
	"testing"
)

func TestScrollRevealsCaret(t *testing.T) {
	f, p := selFlow(Chars{Text: strings.Repeat("line\n", 30)})
	f.SetMode(ModeEdit)
	f.X, f.Y, f.Width, f.Height = 0, 0, 200, 600
	sv := NewScrollView(0, 0, 200, 100)
	sv.Add(f, 200, 600)
	NewStage(p, 200, 100, sv)
	f.MouseDown(5, 10)
	f.MouseUp(0, 0)
	if !f.Editing() {
		t.Fatal("caret should be in the text")
	}
	if _, _, _, ok := sv.CaretBox(); !ok {
		t.Fatal("an editing flow reports its caret")
	}
	if sv.ScrollY != 0 {
		t.Fatalf("starts at the top: %v", sv.ScrollY)
	}
	sv.HandleKey("End", false)
	for i := 0; i < 20; i++ {
		sv.HandleKey("ArrowDown", false)
	}
	_, y, h, _ := f.CaretBox()
	if sv.ScrollY == 0 || y < sv.ScrollY || y+h > sv.ScrollY+sv.Height {
		t.Fatalf("caret y=%v h=%v outside the view at %v", y, h, sv.ScrollY)
	}
	for i := 0; i < 25; i++ {
		sv.HandleKey("ArrowUp", false)
	}
	if _, y, _, _ := f.CaretBox(); y < sv.ScrollY {
		t.Fatalf("scrolled back up: caret %v view %v", y, sv.ScrollY)
	}
	sv.ScrollTo(0, 0)
	sv.InsertText("x")
	if sv.RevealCaret() {
		t.Fatal("already in view")
	}
}

func TestCaretBoxNotEditing(t *testing.T) {
	f, p := selFlow(Chars{Text: "abc"})
	st := NewStage(p, 100, 100, f)
	if _, _, _, ok := st.CaretBox(); ok {
		t.Fatal("no caret when not editing")
	}
	sv := NewScrollView(0, 0, 50, 50)
	sv.Add(f, 100, 100)
	if sv.RevealCaret() {
		t.Fatal("nothing to reveal")
	}
}

func TestTypewriterKeepsTheLineAtOneHeight(t *testing.T) {
	f, p := selFlow(Chars{Text: strings.Repeat("line\n", 60)})
	f.SetMode(ModeEdit)
	f.X, f.Y, f.Width, f.Height = 0, 0, 200, 1400
	sv := NewScrollView(0, 0, 200, 200)
	sv.Add(f, 200, 1400)
	NewStage(p, 200, 200, sv)
	sv.Typewriter = true
	f.MouseDown(5, 10)
	f.MouseUp(0, 0)
	if sv.ScrollY != 0 {
		t.Fatalf("a click leaves the page where it is: %v", sv.ScrollY)
	}
	var heights []float64
	for i := 0; i < 30; i++ {
		sv.HandleKey("ArrowDown", false)
		_, y, h, _ := sv.CaretBox()
		if i >= 12 { // past the first screen, where the page can follow
			heights = append(heights, y+h/2)
		}
	}
	for _, c := range heights {
		if c < 99 || c > 101 {
			t.Fatalf("the caret stays in the middle of a 200 px view: %v", heights)
		}
	}
	before := sv.ScrollY
	sv.InsertText("abc")
	if sv.ScrollY != before {
		t.Fatalf("typing on a line keeps the page: %v -> %v", before, sv.ScrollY)
	}
	for i := 0; i < 60; i++ {
		sv.HandleKey("ArrowUp", false)
	}
	if sv.ScrollY != 0 {
		t.Fatalf("at the top the page is at its start: %v", sv.ScrollY)
	}
	sv.TypewriterAt = 0.25
	for i := 0; i < 30; i++ {
		sv.HandleKey("ArrowDown", false)
	}
	if _, y, h, _ := sv.CaretBox(); y+h/2 < 49 || y+h/2 > 51 {
		t.Fatalf("a quarter down the view: %v", y+h/2)
	}
	sv.Typewriter = false
	sv.ScrollTo(0, 0)
	sv.HandleKey("ArrowUp", false)
	if _, y, h, _ := f.CaretBox(); y+h > sv.ScrollY+sv.Height || y < sv.ScrollY || y-sv.ScrollY < 150 {
		t.Fatalf("off, the page moves only as far as it must (the caret at the foot of the view): y=%v view=%v", y, sv.ScrollY)
	}
}

func TestTypewriterCarriageKeepsTheLetterInOnePlace(t *testing.T) {
	f, p := selFlow(Chars{Text: "start"})
	f.SetMode(ModeEdit)
	f.X, f.Y, f.Width, f.Height = 0, 0, 2000, 400
	sv := NewScrollView(0, 0, 200, 200)
	sv.Add(f, 2000, 400)
	NewStage(p, 200, 200, sv)
	sv.Typewriter, sv.TypewriterX = true, true
	f.MouseDown(5, 10)
	f.MouseUp(0, 0)
	sv.HandleKey("End", false)
	atCaret := func() float64 { x, _, _, _ := sv.CaretBox(); return x }
	if x := atCaret(); x < 99 || x > 101 {
		t.Fatalf("the caret is in the middle across the view: %v", x)
	}
	sx := sv.ScrollX
	for i := 0; i < 10; i++ {
		sv.InsertText("m")
		if x := atCaret(); x < 99 || x > 101 {
			t.Fatalf("after %d letters the caret is still in its place: %v", i+1, x)
		}
	}
	if sv.ScrollX <= sx {
		t.Fatalf("the page slid left under the carriage: %v -> %v", sx, sv.ScrollX)
	}
	sv.HandleKey("Enter", false)
	if x := atCaret(); x < 99 || x > 101 {
		t.Fatalf("after the return the start of the new line is in the place: %v", x)
	}
	if sv.ScrollX >= sx {
		t.Fatalf("the page slid back right, past its left edge: %v", sv.ScrollX)
	}
	if sv.ScrollX >= 0 {
		t.Fatalf("at the start of a line the page is past its left edge: %v", sv.ScrollX)
	}
	sv.TypewriterX = false
	sv.ScrollTo(0, 0)
	if sv.ScrollX != 0 {
		t.Fatalf("a normal view does not run past its edge: %v", sv.ScrollX)
	}
}
