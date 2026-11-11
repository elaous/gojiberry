package canvas

import (
	"strings"
	"testing"
)

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

func TestNoPreeditWhenNotEditing(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello"})
	s := NewStage(p, 400, 400, f)
	if s.SetComposition("ni") {
		t.Error("a text in view mode took a preedit")
	}
}

func TestReadMarksAreDrawnUnderTheText(t *testing.T) {
	f, p := newFlow("one two three", 400, 100)
	if sf, _, wf, _ := f.ReadMarks(); sf >= 0 || wf >= 0 {
		t.Fatal("marks before any were set")
	}
	f.SetReadMarks(0, 13, 4, 7)
	f.Draw()
	var sent, word bool
	for _, c := range p.calls {
		sent = sent || c == "fillStyle "+ReadSentenceColor
		word = word || c == "fillStyle "+ReadWordColor
	}
	if !sent || !word {
		t.Errorf("sentence %v word %v in %v", sent, word, p.calls)
	}
	if !p.has("fillRect 50 ") && !p.has("fillRect 42 ") { // "two" begins after "one " (8px per rune).
		t.Errorf("no rectangle at the word: %v", p.calls)
	}
	if c := f.Cursors()[0]; c.Caret != 0 {
		t.Errorf("the marks moved the caret: %+v", c)
	}
	f.ClearReadMarks()
	p.calls = nil
	f.Draw()
	for _, c := range p.calls {
		if c == "fillStyle "+ReadWordColor {
			t.Error("the marks stayed after they were cleared")
		}
	}
	f.Reveal(8)
	if x, y, h, ok := f.TakeReveal(); !ok || h <= 0 || x < 0 || y < 0 {
		t.Errorf("reveal: %v %v %v %v", x, y, h, ok)
	}
}
