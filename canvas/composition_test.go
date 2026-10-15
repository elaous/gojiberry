package canvas

import (
	"strings"
	"testing"
)

// The preedit of an input method is drawn at the caret, underlined, and the stage says where the caret is so
// that the candidate window can be put there; it is not in the document.
func TestPreeditIsDrawnAtTheCaretAndNotInTheDocument(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello wor", Format: Format{FontSize: 20}})
	f.SetMode(ModeEdit)
	s := NewStage(p, 400, 400, f)
	f.MouseDown(10+8*9, 40)
	f.MouseUp(0, 0)
	f.SetCaretVisible(true)
	if _, _, _, ok := s.CompositionAnchor(); ok {
		t.Error("an anchor without a composition")
	}
	if !s.SetComposition("ni") {
		t.Fatal("the text did not take the preedit")
	}
	x, y, h, ok := s.CompositionAnchor()
	cx, cy, ch, _ := f.CaretRect(f.Cursors()[0].Caret)
	if !ok || x != cx || y != cy || h != ch {
		t.Errorf("anchor %v %v %v %v, the caret is at %v %v %v", x, y, h, ok, cx, cy, ch)
	}
	p.calls = nil
	f.Draw()
	var text, line bool
	for _, c := range p.calls {
		text = text || strings.HasPrefix(c, `fillText "ni"`)
		line = line || (strings.HasPrefix(c, "fillRect") && strings.HasSuffix(c, " 16 1"))
	}
	if !text {
		t.Errorf("the preedit was not drawn: %v", p.calls)
	}
	if !line {
		t.Errorf("the preedit has no underline: %v", p.calls)
	}
	if strings.Contains(f.Text, "ni") {
		t.Error("the preedit is in the document")
	}
	// It ends with the composition.
	s.SetComposition("")
	if _, _, _, ok := s.CompositionAnchor(); ok {
		t.Error("an anchor after the composition")
	}
	p.calls = nil
	f.Draw()
	for _, c := range p.calls {
		if strings.HasPrefix(c, `fillText "ni"`) {
			t.Error("the preedit is still drawn")
		}
	}
}

// A text that is not being edited takes no preedit.
func TestNoPreeditWhenNotEditing(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello"})
	s := NewStage(p, 400, 400, f)
	if s.SetComposition("ni") {
		t.Error("a text in view mode took a preedit")
	}
}
