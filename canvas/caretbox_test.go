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
