package canvas

import (
	"reflect"
	"testing"
)

// typeText types s one character at a time, as the browser delivers it.
func typeText(ft *FloatingText, s string) {
	for _, r := range s {
		ft.InsertText(string(r))
	}
}

// caretEnd puts the caret at the end of the text (a click only reaches
// the end of a short line).
func caretEnd(ft *FloatingText) { ft.caret, ft.anchor = len(ft.runes), len(ft.runes) }

func editing(t *testing.T, text string) *FloatingText {
	t.Helper()
	ft, _ := newFloat(text)
	click(ft, 0)
	caretEnd(ft)
	if !ft.Editing() {
		t.Fatal("not editing")
	}
	return ft
}

func TestAutoCloseInsertsPairsAndOvertypes(t *testing.T) {
	ft := editing(t, "")
	typeText(ft, "(")
	if ft.Text() != "()" || ft.Caret() != 1 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	typeText(ft, "x")
	typeText(ft, ")") // overtypes the closer already there
	if ft.Text() != "(x)" || ft.Caret() != 3 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	typeText(ft, " [{")
	if ft.Text() != "(x) [{}]" || ft.Caret() != 6 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	typeText(ft, "}]")
	if ft.Text() != "(x) [{}]" || ft.Caret() != 8 {
		t.Fatalf("overtyping a nested pair: text=%q caret=%d", ft.Text(), ft.Caret())
	}
}

func TestAutoCloseQuotes(t *testing.T) {
	ft := editing(t, "")
	typeText(ft, `"`)
	typeText(ft, "hi")
	typeText(ft, `"`)
	if ft.Text() != `"hi"` || ft.Caret() != 4 {
		t.Fatalf("a typed quote pair: text=%q caret=%d", ft.Text(), ft.Caret())
	}
	// An apostrophe after a letter is just an apostrophe.
	ft = editing(t, "")
	typeText(ft, "don't")
	if ft.Text() != "don't" {
		t.Fatalf("text = %q", ft.Text())
	}
	// A quote after a space opens a pair.
	typeText(ft, " '")
	if ft.Text() != "don't ''" || ft.Caret() != 7 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	// Typing a third quote right after an empty pair doesn't nest.
	ft = editing(t, "")
	typeText(ft, `""`)
	if ft.Text() != `""` || ft.Caret() != 2 {
		t.Fatalf("typing the quote again overtypes: text=%q caret=%d", ft.Text(), ft.Caret())
	}
	typeText(ft, `"`)
	if ft.Text() != `"""` {
		t.Fatalf("no auto-close right after a quote: %q", ft.Text())
	}
}

func TestAutoCloseNotInFrontOfAWord(t *testing.T) {
	ft := editing(t, "word")
	click(ft, 0)
	typeText(ft, "(")
	if ft.Text() != "(word" {
		t.Fatalf("an opener in front of a word doesn't auto-close: %q", ft.Text())
	}
	// But in front of a space or the end it does.
	ft = editing(t, "a b")
	click(ft, 1)
	typeText(ft, "(")
	if ft.Text() != "a() b" {
		t.Fatalf("text = %q", ft.Text())
	}
}

func TestAutoCloseWrapsTheSelection(t *testing.T) {
	ft := editing(t, "hello world")
	click(ft, 0)
	for i := 0; i < 5; i++ {
		ft.HandleKey("ArrowRight", true)
	}
	typeText(ft, "(")
	if ft.Text() != "(hello) world" {
		t.Fatalf("text = %q", ft.Text())
	}
	if from, to := ft.Selection(); from != 1 || to != 6 {
		t.Fatalf("the wrapped text stays selected: %d..%d", from, to)
	}
	typeText(ft, "[") // wrap again
	if ft.Text() != "([hello]) world" {
		t.Fatalf("text = %q", ft.Text())
	}
	// A non-opener replaces the selection as usual.
	typeText(ft, "X")
	if ft.Text() != "([X]) world" {
		t.Fatalf("text = %q", ft.Text())
	}
}

func TestBackspaceBetweenAPairDeletesBoth(t *testing.T) {
	ft := editing(t, "")
	typeText(ft, "({")
	ft.HandleKey("Backspace", false)
	if ft.Text() != "()" || ft.Caret() != 1 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	ft.HandleKey("Backspace", false)
	if ft.Text() != "" {
		t.Fatalf("text = %q", ft.Text())
	}
	// Not between a pair: a normal backspace.
	ft = editing(t, "(a)")
	click(ft, 2)
	ft.HandleKey("Backspace", false)
	if ft.Text() != "()" {
		t.Fatalf("text = %q", ft.Text())
	}
}

func TestAutoCloseOffCustomAndPaste(t *testing.T) {
	ft := editing(t, "")
	ft.AutoClose = nil
	typeText(ft, "(")
	if ft.Text() != "(" {
		t.Fatalf("auto-close is off with a nil map: %q", ft.Text())
	}
	ft = editing(t, "")
	ft.AutoClose = map[rune]rune{'<': '>'}
	typeText(ft, "<")
	if ft.Text() != "<>" {
		t.Fatalf("custom pairs: %q", ft.Text())
	}
	typeText(ft, "(")
	if ft.Text() != "<(>" {
		t.Fatalf("(is not in the custom map: %q", ft.Text())
	}
	// Pasting and multi-character input are inserted as is.
	ft = editing(t, "")
	ft.PasteText("(a")
	ft.InsertText("[b")
	ft.InsertText("\n")
	if ft.Text() != "(a[b\n" {
		t.Fatalf("text = %q", ft.Text())
	}
	if ft.InsertText("") {
		t.Fatal("empty text")
	}
}

func TestTypedPairsKeepTheTypingFormat(t *testing.T) {
	ft := editing(t, "")
	ft.ToggleBold()
	typeText(ft, "(")
	if ft.FormatAt(0).FontWeight != 700 || ft.FormatAt(1).FontWeight != 700 {
		t.Fatalf("both halves are bold: %+v %+v", ft.FormatAt(0), ft.FormatAt(1))
	}
}

// --- completion ---

func TestSuggestionsFromTheTextByFrequency(t *testing.T) {
	ft := editing(t, "banana band banana bandana ")
	typeText(ft, "b")
	if len(ft.Suggestions()) != 0 {
		t.Fatal("one letter is too short to complete")
	}
	typeText(ft, "a")
	if got := ft.Suggestions(); !reflect.DeepEqual(got, []string{"banana", "band", "bandana"}) {
		t.Fatalf("suggestions = %v (most frequent first, then alphabetical)", got)
	}
	typeText(ft, "nd")
	if got := ft.Suggestions(); !reflect.DeepEqual(got, []string{"bandana"}) {
		t.Fatalf("narrowed: %v (a word equal to the prefix isn't offered)", got)
	}
	typeText(ft, "x")
	if len(ft.Suggestions()) != 0 {
		t.Fatal("no matches: popup closes")
	}
}

func TestAcceptingASuggestion(t *testing.T) {
	ft := editing(t, "banana ")
	typeText(ft, "ba")
	if !ft.HandleKey("Tab", false) {
		t.Fatal("Tab accepts")
	}
	if ft.Text() != "banana banana" || ft.Caret() != 13 || len(ft.Suggestions()) != 0 {
		t.Fatalf("text=%q caret=%d", ft.Text(), ft.Caret())
	}
	typeText(ft, " ba")
	ft.HandleKey("Enter", false)
	if ft.Text() != "banana banana banana" {
		t.Fatalf("Enter accepts too (and doesn't add a line): %q", ft.Text())
	}
}

func TestSuggestionNavigationAndDismiss(t *testing.T) {
	ft := editing(t, "alpha alpine alps ")
	typeText(ft, "al")
	if !reflect.DeepEqual(ft.Suggestions(), []string{"alpha", "alpine", "alps"}) || ft.sugg.Hover() != 0 {
		t.Fatalf("%v hover=%d", ft.Suggestions(), ft.sugg.Hover())
	}
	ft.HandleKey("ArrowDown", false)
	ft.HandleKey("ArrowDown", false)
	if ft.sugg.Hover() != 2 {
		t.Fatalf("hover = %d", ft.sugg.Hover())
	}
	ft.HandleKey("ArrowDown", false)
	if ft.sugg.Hover() != 0 {
		t.Fatal("wraps around")
	}
	ft.HandleKey("ArrowUp", false)
	if ft.sugg.Hover() != 2 {
		t.Fatal("wraps the other way")
	}
	ft.HandleKey("Tab", false)
	if ft.Text() != "alpha alpine alps alps" {
		t.Fatalf("accepted the highlighted one: %q", ft.Text())
	}
	typeText(ft, " al")
	if !ft.HandleKey("Escape", false) || len(ft.Suggestions()) != 0 || !ft.Editing() {
		t.Fatal("Escape dismisses the popup but keeps editing")
	}
	// Other keys dismiss it too.
	typeText(ft, "p")
	if len(ft.Suggestions()) == 0 {
		t.Fatal("typing reopens")
	}
	ft.HandleKey("ArrowLeft", false)
	if len(ft.Suggestions()) != 0 {
		t.Fatal("moving the caret closes the popup")
	}
}

func TestBackspaceUpdatesSuggestions(t *testing.T) {
	ft := editing(t, "cat catalog ")
	typeText(ft, "cata")
	if len(ft.Suggestions()) != 1 {
		t.Fatalf("%v", ft.Suggestions())
	}
	ft.HandleKey("Backspace", false)
	ft.HandleKey("Backspace", false)
	if got := ft.Suggestions(); !reflect.DeepEqual(got, []string{"cat", "catalog"}) {
		t.Fatalf("after backspacing to 'ca': %v", got)
	}
	ft.HandleKey("Backspace", false)
	if len(ft.Suggestions()) != 0 {
		t.Fatal("a one-letter prefix closes the popup")
	}
}

func TestVocabularyAndCustomCompleter(t *testing.T) {
	ft := editing(t, "")
	ft.Vocabulary = []string{"gojiberry", "gopher", "go"}
	typeText(ft, "go")
	if got := ft.Suggestions(); !reflect.DeepEqual(got, []string{"gojiberry", "gopher"}) {
		t.Fatalf("vocabulary words longer than the prefix: %v", got)
	}
	ft.Completer = CompleterFunc(func(prefix string) []string { return []string{prefix + "-one", prefix + "-two"} })
	typeText(ft, "x")
	if got := ft.Suggestions(); !reflect.DeepEqual(got, []string{"gox-one", "gox-two"}) {
		t.Fatalf("custom completer: %v", got)
	}
	ft.sugg.Activate(1)
	if ft.Text() != "gox-two" {
		t.Fatalf("text = %q", ft.Text())
	}
}

func TestSuggestionsLimitAndNoPopupWithASelectionOrView(t *testing.T) {
	ft := editing(t, "")
	var words string
	for _, w := range []string{"aaa1", "aaa2", "aaa3", "aaa4", "aaa5", "aaa6", "aaa7", "aaa8"} {
		words += w + " "
	}
	ft.SetText(words)
	caretEnd(ft)
	typeText(ft, "aa")
	if len(ft.Suggestions()) != 6 {
		t.Fatalf("at most 6 suggestions: %v", ft.Suggestions())
	}
	ft.HandleKey("Escape", false)
	ft.HandleKey("ArrowLeft", true) // a selection
	typeText(ft, "q")
	if len(ft.Suggestions()) != 0 {
		t.Fatal("no popup while typing over a selection")
	}
	ft.SetMode(ModeView)
	if ft.updateSuggestions(); len(ft.Suggestions()) != 0 {
		t.Fatal("none in view mode")
	}
}

func TestClickingASuggestionAcceptsIt(t *testing.T) {
	ft := editing(t, "banana ")
	typeText(ft, "ba")
	x, y, _, _ := ft.sugg.Rect()
	ft.MouseDown(x+5, y+5)
	if ft.Text() != "banana banana" {
		t.Fatalf("text = %q", ft.Text())
	}
}

func TestSuggestionsStayInTheViewport(t *testing.T) {
	ft := editing(t, "banana ")
	ft.SetViewport(0, 0, 500, 150)
	typeText(ft, "ba")
	_, y, _, h := ft.sugg.Rect()
	if y+h > 150 {
		t.Fatalf("popup %g+%g is outside the 150px viewport", y, h)
	}
}

// --- color palette ---

func TestColorPaletteColorsTheSelection(t *testing.T) {
	ft := editing(t, "hello world")
	ft.HandleKey("Home", false)
	for i := 0; i < 5; i++ {
		ft.HandleKey("ArrowRight", true)
	}
	b := ft.buttonRect(6)
	ft.MouseDown(b[0]+3, b[1]+3)
	if !ft.palOpen() || !ft.WantsFocus() {
		t.Fatal("the Color button opens the palette")
	}
	labels := ft.pal.Labels()
	if labels[0] != "Black" || labels[len(labels)-1] != "Default color" {
		t.Fatalf("labels = %v", labels)
	}
	// Pick "Red".
	m := ft.pal
	idx := 2
	top := m.y + float64(idx)*28
	ft.MouseDown(m.x+5, top+5)
	if ft.palOpen() {
		t.Fatal("choosing closes the palette")
	}
	for i := 0; i < 5; i++ {
		if ft.FormatAt(i).Color != "#d93025" {
			t.Fatalf("char %d color = %q", i, ft.FormatAt(i).Color)
		}
	}
	if ft.FormatAt(5).Color != "" {
		t.Fatal("only the selection is colored")
	}
	if from, to := ft.Selection(); from != 0 || to != 5 || !ft.Editing() {
		t.Fatal("the selection survives")
	}
}

func TestHighlightPaletteAndClearing(t *testing.T) {
	ft := editing(t, "abc")
	ft.SelectAll()
	b := ft.buttonRect(7)
	ft.MouseDown(b[0]+3, b[1]+3)
	if !ft.palOpen() || ft.palKind != "highlight" || ft.pal.Labels()[0] != "Yellow" {
		t.Fatalf("labels = %v", ft.pal.Labels())
	}
	ft.pal.Activate(0)
	if ft.FormatAt(0).Background != "#fff475" {
		t.Fatalf("bg = %q", ft.FormatAt(0).Background)
	}
	ft.MouseDown(b[0]+3, b[1]+3)
	last := len(ft.pal.items) - 1
	ft.pal.Activate(last) // "No highlight"
	if ft.FormatAt(0).Background != "" {
		t.Fatal("highlight removed")
	}
	ft.SetTextColor("#123456")
	ft.SetHighlight("#abcdef")
	if f := ft.FormatAt(2); f.Color != "#123456" || f.Background != "#abcdef" {
		t.Fatalf("%+v", f)
	}
}

func TestPaletteKeyboardTogglingAndClosing(t *testing.T) {
	ft := editing(t, "abc")
	ft.SelectAll()
	b := ft.buttonRect(6)
	ft.MouseDown(b[0]+3, b[1]+3)
	ft.HandleKey("ArrowDown", false)
	ft.HandleKey("ArrowDown", false)
	if !ft.HandleKey("Enter", false) || ft.palOpen() {
		t.Fatal("Enter picks")
	}
	if ft.FormatAt(0).Color != "#5f6368" {
		t.Fatalf("color = %q (the first Down highlights Black, the second Gray)", ft.FormatAt(0).Color)
	}
	ft.MouseDown(b[0]+3, b[1]+3)
	ft.MouseDown(b[0]+3, b[1]+3) // the button again closes it
	if ft.palOpen() {
		t.Fatal("toggled closed")
	}
	ft.MouseDown(b[0]+3, b[1]+3)
	ft.HandleKey("Escape", false)
	if ft.palOpen() {
		t.Fatal("Escape closes")
	}
	// Switching palettes, then leaving edit mode, closes it.
	ft.MouseDown(b[0]+3, b[1]+3)
	ft.SetMode(ModeView)
	if ft.palOpen() {
		t.Fatal("view mode closes popups")
	}
	// Pressing elsewhere closes it and carries on.
	ft.SetMode(ModeEdit)
	ft.MouseDown(b[0]+3, b[1]+3)
	ft.MouseDown(500, 500)
	if ft.palOpen() {
		t.Fatal("a press outside closes it")
	}
}

func TestOverlayDrawsPopups(t *testing.T) {
	ft := editing(t, "banana ")
	typeText(ft, "ba")
	p := ft.Painter.(*fakePainter)
	p.calls = nil
	ft.DrawOverlay()
	if !p.has(`fillText "banana"`) {
		t.Fatalf("completion popup: %v", p.calls)
	}
	b := ft.buttonRect(6)
	ft.MouseDown(b[0]+3, b[1]+3) // opening the palette dismisses the completions
	p.calls = nil
	ft.DrawOverlay()
	if p.has(`fillText "banana"`) || !p.has(`fillText "Black"`) || !p.has("fillStyle #d93025") {
		t.Fatalf("palette: %v", p.calls)
	}
	ft.SetMode(ModeView)
	p.calls = nil
	ft.DrawOverlay()
	if len(p.calls) != 0 {
		t.Fatal("no overlay in view mode")
	}
}

// --- commands, selection, clipboard ---

func TestToggleDecorationsAndCommands(t *testing.T) {
	ft := editing(t, "abcd")
	ft.SelectAll()
	for _, tt := range []struct {
		cmd  Command
		get  func(Format) bool
		name string
	}{
		{CmdUnderline, func(f Format) bool { return f.Underline }, "underline"},
		{CmdStrike, func(f Format) bool { return f.Strike }, "strike"},
		{CmdSuperscript, func(f Format) bool { return f.Superscript }, "superscript"},
	} {
		if !ft.HandleCommand(tt.cmd) || !tt.get(ft.FormatAt(0)) || !tt.get(ft.FormatAt(3)) {
			t.Fatalf("%s on", tt.name)
		}
		ft.HandleCommand(tt.cmd)
		if tt.get(ft.FormatAt(0)) {
			t.Fatalf("%s off again", tt.name)
		}
	}
	ft.HandleCommand(CmdSuperscript)
	ft.HandleCommand(CmdSubscript)
	if f := ft.FormatAt(0); !f.Subscript || f.Superscript {
		t.Fatalf("sub replaces super: %+v", f)
	}
	ft.HandleCommand(CmdBold)
	ft.HandleCommand(CmdItalic)
	if f := ft.FormatAt(0); f.FontWeight != 700 || f.FontStyle != StyleItalic {
		t.Fatalf("%+v", f)
	}
	// Partly formatted text: the first toggle turns it on everywhere.
	ft.SetText("abcd")
	click(ft, 0)
	ft.HandleKey("ArrowRight", true)
	ft.ToggleUnderline()
	ft.SelectAll()
	ft.ToggleUnderline()
	if !ft.FormatAt(3).Underline || !ft.FormatAt(0).Underline {
		t.Fatal("mixed selection: on everywhere")
	}
	if ft.HandleCommand("nonsense") {
		t.Fatal("unknown command")
	}
}

func TestSelectAllCommandAndCopyCut(t *testing.T) {
	ft := editing(t, "hello")
	if _, ok := ft.CopyText(); ok {
		t.Fatal("nothing selected")
	}
	if !ft.HandleCommand(CmdSelectAll) {
		t.Fatal("select all")
	}
	if text, ok := ft.CopyText(); !ok || text != "hello" {
		t.Fatalf("copy = %q %v", text, ok)
	}
	if ft.Text() != "hello" {
		t.Fatal("copy leaves the text")
	}
	click(ft, 1)
	ft.HandleKey("ArrowRight", true)
	ft.HandleKey("ArrowRight", true)
	if text, ok := ft.CutText(); !ok || text != "el" || ft.Text() != "hlo" {
		t.Fatalf("cut = %q, text %q", text, ft.Text())
	}
	if _, ok := ft.CutText(); ok {
		t.Fatal("nothing left selected")
	}
	if !ft.PasteText("ELL") || ft.Text() != "hELLlo" {
		t.Fatalf("text = %q", ft.Text())
	}
	ft.SetMode(ModeView)
	if _, ok := ft.CopyText(); ok || ft.HandleCommand(CmdBold) {
		t.Fatal("view mode")
	}
}

func TestCopyFormatAndPasteFormat(t *testing.T) {
	ft := editing(t, "abcdef")
	st := NewStage(&fakePainter{}, 1, 1, ft)
	// Make "abc" bold red underlined, then copy the format from "bc".
	click(ft, 0)
	for i := 0; i < 3; i++ {
		ft.HandleKey("ArrowRight", true)
	}
	ft.ToggleBold()
	ft.ToggleUnderline()
	ft.SetTextColor("#f00")
	click(ft, 1)
	ft.HandleKey("ArrowRight", true)
	ft.HandleKey("ArrowRight", true)
	if !st.RunCommand(CmdCopyFormat) {
		t.Fatal("copy format")
	}
	got, ok := st.Formats.Get()
	if !ok || got != (Format{FontWeight: 700, Underline: true, Color: "#f00"}) {
		t.Fatalf("copied %+v", got)
	}
	// Paste it onto "ef".
	click(ft, 4)
	ft.HandleKey("ArrowRight", true)
	ft.HandleKey("ArrowRight", true)
	if !st.RunCommand(CmdPasteFormat) {
		t.Fatal("paste format")
	}
	if ft.FormatAt(4) != got || ft.FormatAt(5) != got || ft.FormatAt(3) != (Format{}) {
		t.Fatalf("formats: %+v %+v %+v", ft.FormatAt(3), ft.FormatAt(4), ft.FormatAt(5))
	}
	// The minimum common formatting of mixed text.
	ft.SelectAll()
	if c := ft.CommonFormat(); c != (Format{}) {
		// "abc" and "ef" share bold/underline/red, "d" is plain: nothing is common.
		t.Fatalf("common = %+v", c)
	}
	click(ft, 0)
	for i := 0; i < 3; i++ {
		ft.HandleKey("ArrowRight", true)
	}
	if c := ft.CommonFormat(); c != got {
		t.Fatalf("common of 'abc' = %+v", c)
	}
	// Pasting a plain format clears formatting.
	st.Formats.Set(Format{})
	ft.SelectAll()
	st.RunCommand(CmdPasteFormat)
	if !ft.CommonFormat().IsZero() || !ft.FormatAt(0).IsZero() {
		t.Fatal("plain format painted over everything")
	}
	// With no selection, the format applies to the next typed text.
	st.Formats.Set(Format{FontStyle: StyleItalic})
	click(ft, 6)
	st.RunCommand(CmdPasteFormat)
	typeText(ft, "Z")
	if ft.FormatAt(6).FontStyle != StyleItalic {
		t.Fatalf("typed text takes the pasted format: %+v", ft.FormatAt(6))
	}
}

func TestFormatCommandsDeclineWithoutAClipboardOrContent(t *testing.T) {
	ft := editing(t, "abc") // never added to a stage: no format clipboard
	if ft.HandleCommand(CmdCopyFormat) || ft.HandleCommand(CmdPasteFormat) {
		t.Fatal("no clipboard")
	}
	ft.SetFormatClipboard(&FormatClipboard{})
	ft.SelectAll()
	if ft.HandleCommand(CmdPasteFormat) {
		t.Fatal("nothing copied yet")
	}
	if !ft.HandleCommand(CmdCopyFormat) {
		t.Fatal("copying a plain format works")
	}
	if !ft.HandleCommand(CmdPasteFormat) {
		t.Fatal("and so does pasting it back")
	}
}
