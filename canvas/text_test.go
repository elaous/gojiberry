package canvas

import (
	"strings"
	"testing"
)

func runTexts(f *TextFlow) []string {
	var out []string
	for _, ln := range f.Layout().Lines {
		for _, r := range ln.Runs {
			out = append(out, r.Text)
		}
	}
	return out
}

func TestCaseDisplay(t *testing.T) {
	f, _ := paraFlow(
		Chars{Text: "hello wORLD ", Format: Format{Case: CaseUpper}},
		Chars{Text: "Hello wORLD ", Format: Format{Case: CaseLower}},
		Chars{Text: "it's a tITLE-case", Format: Format{Case: CaseTitle}},
	)
	got := strings.Join(runTexts(f), "|")
	for _, want := range []string{"HELLO WORLD ", "hello", "world ", "It's A", "Title-Case"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from %q", want, got)
		}
	}
	f.SelectAll()
	if !strings.HasPrefix(f.SelectedText(), "hello wORLD Hello wORLD it's a tITLE-case") {
		t.Errorf("selected text = %q", f.SelectedText())
	}
	f, p := paraFlow(Chars{Text: "Ab", Format: Format{Case: CaseSmallCaps}})
	lines := f.Layout().Lines
	runs := lines[0].Runs
	if len(runs) != 2 || runs[0].Text != "A" || runs[1].Text != "B" || runs[0].Size <= runs[1].Size {
		t.Fatalf("small caps runs: %+v", runs)
	}
	if runs[1].X != 8 {
		t.Errorf("second run follows the first: x=%g", runs[1].X)
	}
	f.Draw()
	_ = p
}

func TestCaseCommands(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "hello world"})
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 5}})
	for chord, want := range map[string]CaseKind{"Mod+Alt+U": CaseUpper, "Mod+Alt+L": CaseLower, "Mod+Alt+T": CaseTitle, "Mod+Alt+M": CaseSmallCaps} {
		if !st.HandleChord(chord) || f.Slice(0, 1)[0].(Chars).Format.Case != want {
			t.Errorf("%s: %+v", chord, f.ContentItems())
		}
		st.HandleChord(chord) // again: back to normal
		if f.Slice(0, 1)[0].(Chars).Format.Case != CaseNormal {
			t.Errorf("%s toggles off", chord)
		}
	}
	bst, ft := stageWith(t, "text")
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 4}})
	if !bst.HandleChord("Mod+Alt+U") || ft.FormatAt(0).Case != CaseUpper {
		t.Error("a box applies case too")
	}
}

func linkFlow(t *testing.T) (*TextFlow, *Stage, *[]string) {
	t.Helper()
	f, st := editFlow(t, 0,
		Chars{Text: "see "}, Chars{Text: "docs", Format: Format{Link: "http://x.y"}}, Chars{Text: " now"})
	var opened []string
	st.OpenLink = func(u string) { opened = append(opened, u) }
	f.stageLink = st.OpenLink
	return f, st, &opened
}

func TestLinkStyleHoverAndClick(t *testing.T) {
	f, st, opened := linkFlow(t)
	f.SetMode(ModeView)
	ln := f.Layout().Lines[0]
	var link Run
	for _, r := range ln.Runs {
		if r.Link != "" {
			link = r
		}
	}
	if link.Text != "docs" || link.Color != LinkColor || !link.Underline {
		t.Fatalf("a link is drawn as one: %+v", link)
	}
	x, y := ln.X+link.X+4, ln.Y+5
	var hovered []string
	f.OnHoverLink = func(u string) { hovered = append(hovered, u) }
	if !st.MouseMove(x, y) || f.HoveredLink() != "http://x.y" || len(hovered) != 1 {
		t.Fatalf("hover: %q %v", f.HoveredLink(), hovered)
	}
	if f.CSSCursor() != "pointer" {
		t.Error("pointer cursor over a link")
	}
	p := st.Painter.(*fakePainter)
	st.Draw()
	if !p.has(`fillText "http://x.y"`) || !p.has(`fillText "Click to open"`) {
		t.Errorf("the tip shows the URL: %v", p.calls)
	}
	st.MouseMove(x+1, y) // moving along the link keeps the tip following
	if len(hovered) != 1 {
		t.Error("no new hover event along the same link")
	}
	st.MouseDown(x, y)
	if len(*opened) != 1 || (*opened)[0] != "http://x.y" {
		t.Errorf("a click in view mode opens it: %v", *opened)
	}
	st.MouseMove(2, 2)
	if f.HoveredLink() != "" || len(hovered) != 2 || hovered[1] != "" {
		t.Errorf("leaving the link: %q %v", f.HoveredLink(), hovered)
	}
	st.MouseMove(x, y)
	st.MouseLeave()
	if f.HoveredLink() != "" {
		t.Error("the pointer leaving the canvas clears the hover")
	}

	f.SetMode(ModeEdit)
	st.MouseDown(x, y)
	st.MouseUp(0, 0)
	if len(*opened) != 1 || !f.Editing() {
		t.Errorf("a plain click in edit mode puts the caret there: %v", *opened)
	}
	st.MouseDownMods(x, y, Mods{Ctrl: true})
	if len(*opened) != 2 {
		t.Errorf("Ctrl+click opens: %v", *opened)
	}
	st.MouseMove(x, y)
	st.Draw()
	if !p.has(`fillText "Ctrl+click to open"`) {
		t.Error("the edit-mode tip says Ctrl+click")
	}
}

func TestLinkCommands(t *testing.T) {
	f, st, opened := linkFlow(t)
	var asked []string
	answer, cancel := "", false
	st.Prompt = func(msg, initial string) (string, bool) {
		asked = append(asked, initial)
		return answer, !cancel
	}
	linkOf := func(i int) string { return f.Slice(i, i+1)[0].(Chars).Format.Link }

	f.SetCursors([]Cursor{At(6)})
	answer = "http://new"
	if !st.HandleChord("Mod+K") || asked[0] != "http://x.y" || linkOf(4) != "http://new" || linkOf(7) != "http://new" || linkOf(3) != "" || linkOf(8) != "" {
		t.Fatalf("edit link: %v %+v", asked, f.ContentItems())
	}
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 3}})
	answer = "http://see"
	st.HandleChord("Mod+K")
	if linkOf(0) != "http://see" || linkOf(2) != "http://see" || linkOf(3) != "" {
		t.Errorf("selection linked: %+v", f.ContentItems())
	}
	f.SetCursors([]Cursor{At(len(f.flatten().runes))})
	answer = "http://end"
	n := len(f.flatten().runes)
	st.HandleChord("Mod+K")
	if got := f.EditText(); !strings.HasSuffix(got, "http://end") || linkOf(n) != "http://end" {
		t.Errorf("address inserted: %q", got)
	}
	f.SetCursors([]Cursor{At(5)})
	st.HandleChord("Mod+Alt+O")
	if len(*opened) != 1 || (*opened)[0] != "http://new" {
		t.Errorf("open: %v", *opened)
	}
	cancel = true
	if st.HandleChord("Mod+K") {
		t.Error("cancel")
	}
	cancel = false
	st.HandleChord("Mod+Alt+K")
	if linkOf(5) != "" {
		t.Errorf("remove: %+v", f.ContentItems())
	}
	if st.HandleChord("Mod+Alt+K") || st.HandleChord("Mod+Alt+O") {
		t.Error("no link at the caret now")
	}
	answer = ""
	f.SetCursors([]Cursor{At(1)}) // in "see"
	st.HandleChord("Mod+K")
	if linkOf(0) != "" {
		t.Errorf("an empty address removes the link: %+v", f.ContentItems())
	}
	f.SetCursors([]Cursor{At(9)})
	if st.HandleChord("Mod+K") {
		t.Error("empty address, no link, nothing selected: nothing to do")
	}
	st.Prompt = nil
	if st.HandleChord("Mod+K") {
		t.Error("without a prompt there is no way to ask")
	}
}

func TestLinkMarkdown(t *testing.T) {
	items := []Item{
		Chars{Text: "see "},
		Chars{Text: "the docs", Format: Format{Link: "http://x.y", FontWeight: 700}},
	}
	md := MarkdownFromItems(items)
	if md != "see [**the docs**](http://x.y)" {
		t.Fatalf("markdown = %q", md)
	}
	back := Markdown{}.Transform(md)
	var link Format
	for _, it := range back {
		if c, ok := it.(Chars); ok && c.Format.Link != "" {
			link = c.Format
		}
	}
	if link.Link != "http://x.y" || link.FontWeight != 700 {
		t.Errorf("round trip: %+v", back)
	}
}

func TestThemeLinkColour(t *testing.T) {
	f, _, _ := linkFlow(t)
	f.SetMode(ModeView)
	f.LinkColor = "#0a6e2d"
	f.InvalidateTransform()
	linkRun := func() Run {
		for _, r := range f.Layout().Lines[0].Runs {
			if r.Link != "" {
				return r
			}
		}
		return Run{}
	}
	if got := linkRun().Color; got != "#0a6e2d" {
		t.Errorf("link colour = %q, want the theme's", got)
	}
	f.LinkColor = ""
	f.InvalidateTransform()
	if got := linkRun().Color; got != LinkColor {
		t.Errorf("link colour = %q, want the default", got)
	}
}
