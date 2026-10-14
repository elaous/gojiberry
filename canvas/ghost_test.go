package canvas

import (
	"strings"
	"testing"
)

func ghostCalls(t *testing.T, text string, click float64, lang string) []string {
	t.Helper()
	f, p := selFlow(Chars{Text: text, Format: Format{Lang: lang, FontSize: 20}})
	f.SetMode(ModeEdit)
	NewStage(p, 400, 400, f)
	f.MouseDown(click, 40)
	f.MouseUp(0, 0)
	f.SetCaretVisible(true)
	f.Ghost = "bra"
	p.calls = nil
	f.drawGhost(f.Layout())
	return p.calls
}

func TestGhostIsDrawnAfterTheCaretInTheRunsFont(t *testing.T) {
	calls := ghostCalls(t, "hello wor", 10+8*9, "")
	var font, text string
	for _, c := range calls {
		if strings.HasPrefix(c, "font ") {
			font = c
		}
		if strings.HasPrefix(c, "fillText") {
			text = c
		}
	}
	if !strings.Contains(font, "20px") || !strings.Contains(text, `"bra"`) {
		t.Errorf("at the end of the line: font %q, text %q (%v)", font, text, calls)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "fillRect") {
			t.Errorf("a tag was drawn at the end of the line: %v", calls)
		}
	}
}

func TestGhostInTheMiddleOfALineIsATagUnderTheCaret(t *testing.T) {
	calls := ghostCalls(t, "hello world and more", 10+8*3, "")
	var tag, rect bool
	for _, c := range calls {
		tag = tag || strings.Contains(c, `"bra  Tab"`)
		rect = rect || strings.HasPrefix(c, "fillRect")
	}
	if !tag || !rect {
		t.Errorf("mid-line: want a tag with a Tab hint: %v", calls)
	}
}

func TestNoGhostWhenNotEditingOrNoSuggestion(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello"})
	NewStage(p, 400, 400, f)
	f.Ghost = "x"
	p.calls = nil
	f.drawGhost(f.Layout())
	if p.has("fillText") || p.has("fillRect") {
		t.Errorf("drawn while not editing: %v", p.calls)
	}
	f.SetMode(ModeEdit)
	f.Ghost = ""
	f.drawGhost(f.Layout())
	if p.has("fillText") || p.has("fillRect") {
		t.Errorf("drawn with nothing to show: %v", p.calls)
	}
}

func TestLangAtIsTheLanguageOfTheTextBeforeTheOffset(t *testing.T) {
	f, p := selFlow(Chars{Text: "bonjour ", Format: Format{Lang: "fr"}}, Chars{Text: "hello"})
	NewStage(p, 400, 400, f)
	if got := f.LangAt(3); got != "fr" {
		t.Errorf("LangAt(3) = %q", got)
	}
	if got := f.LangAt(11); got != "" {
		t.Errorf("LangAt(11) = %q", got)
	}
}
