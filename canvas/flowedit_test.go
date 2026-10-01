package canvas

import (
	"strings"
	"testing"
)

// editFlow is selFlow in edit mode with the caret placed at offset o of
// the first line (the press lands in the first line's text area).
func editFlow(t *testing.T, o int, items ...Item) (*TextFlow, *Stage) {
	t.Helper()
	f, p := selFlow(items...)
	f.SetMode(ModeEdit)
	st := NewStage(p, 400, 400, f)
	f.MouseDown(10+8*float64(o), 40)
	f.MouseUp(0, 0)
	if !f.Editing() {
		t.Fatal("click in edit mode should put the caret in the text")
	}
	return f, st
}

func flowText(f *TextFlow) string { return strings.ReplaceAll(f.EditText(), string(objectRune), "#") }

func TestFlowTypingAndNavigation(t *testing.T) {
	f, st := editFlow(t, 5, Chars{Text: "hello world"})
	st.InsertText("!")
	if flowText(f) != "hello! world" || f.Cursors()[0] != At(6) {
		t.Fatalf("typed: %q %v", flowText(f), f.Cursors())
	}
	st.HandleKey("Backspace", false)
	st.HandleKey("Backspace", false)
	if flowText(f) != "hell world" {
		t.Fatalf("backspace: %q", flowText(f))
	}
	st.HandleKey("Delete", false)
	if flowText(f) != "hellworld" {
		t.Fatalf("delete: %q", flowText(f))
	}
	st.HandleKey("Home", false)
	st.InsertText(">")
	st.HandleKey("End", false)
	st.InsertText("<")
	if flowText(f) != ">hellworld<" {
		t.Fatalf("home/end: %q", flowText(f))
	}
	st.HandleKey("ArrowLeft", false)
	st.HandleKey("ArrowLeft", true) // extend
	if from, to := f.Selection(); from != 9 || to != 10 {
		t.Fatalf("shift+left: %d..%d", from, to)
	}
	st.HandleKey("ArrowRight", false)
	st.HandleKey("Enter", false)
	if !strings.Contains(flowText(f), "\n") {
		t.Fatalf("enter: %q", flowText(f))
	}
	st.HandleKey("ArrowUp", false)
	st.HandleKey("ArrowDown", false)
	if st.HandleKey("F5", false) {
		t.Error("unhandled keys fall through")
	}
}

func TestFlowEditKeepsFormattingAndStructure(t *testing.T) {
	bold := Format{FontWeight: 700}
	f, st := editFlow(t, 2,
		Chars{Text: "ab", Format: bold}, Chars{Text: "cd"},
		Break{Kind: BreakPage}, Chars{Text: "ef"})
	st.InsertText("X") // takes the format before the caret
	items := f.ContentItems()
	if len(items) != 4 || items[0].(Chars).Text != "abX" || items[0].(Chars).Format != bold {
		t.Fatalf("items = %+v", items)
	}
	if _, ok := items[2].(Break); !ok {
		t.Fatalf("the page break survives typing: %+v", items)
	}
	// Select across the break: it goes with the deleted range.
	f.SetCursors([]Cursor{{4, 6}})
	st.HandleKey("Delete", false)
	if flowText(f) != "abXcf" {
		t.Fatalf("after deleting across the break: %q", flowText(f))
	}
	for _, it := range f.ContentItems() {
		if _, ok := it.(Break); !ok {
			continue
		}
		t.Errorf("a break strictly inside the deleted range must go: %+v", f.ContentItems())
	}
}

func TestFlowDeleteRemovesBreakInFront(t *testing.T) {
	f, st := editFlow(t, 2, Chars{Text: "ab"}, Break{Kind: BreakColumn}, Chars{Text: "cd"})
	st.HandleKey("Delete", false)
	if flowText(f) != "abcd" || len(f.ContentItems()) != 1 {
		t.Fatalf("Delete in front of a break removes it: %+v", f.ContentItems())
	}
	st.HandleKey("Delete", false)
	if flowText(f) != "abd" {
		t.Fatalf("then deletes text: %q", flowText(f))
	}
}

func TestFlowMaterializesTransformAndText(t *testing.T) {
	f, p := selFlow()
	f.Transform = Markdown{}
	f.Source = "# Title\n\nbody"
	f.SetMode(ModeEdit)
	st := NewStage(p, 400, 400, f)
	f.MouseDown(10+8, 40)
	f.MouseUp(0, 0)
	st.InsertText("Z")
	if !f.edited || f.Text != "" || !strings.Contains(flowText(f), "TZitle") {
		t.Fatalf("edited=%v text=%q", f.edited, flowText(f))
	}
	f.Source = "something else" // the source is ignored once edited
	if !strings.Contains(flowText(f), "Zitle") {
		t.Fatalf("an edited flow keeps its items: %q", flowText(f))
	}
	f.Source = "fresh"
	f.ResetContent()
	if flowText(f) != "fresh" {
		t.Fatalf("ResetContent brings the source back: %q", flowText(f))
	}

	// A flow with plain Text edits too, and can be emptied.
	g, p2 := selFlow()
	g.Text = "ab"
	g.SetMode(ModeEdit)
	st2 := NewStage(p2, 400, 400, g)
	g.MouseDown(10+16, 40)
	g.MouseUp(0, 0)
	st2.HandleKey("Backspace", false)
	st2.HandleKey("Backspace", false)
	if flowText(g) != "" || g.Text != "" {
		t.Fatalf("emptied: %q / %q", flowText(g), g.Text)
	}
	st2.HandleKey("Backspace", false) // nothing left: still fine
	st2.InsertText("n")
	if flowText(g) != "n" {
		t.Fatalf("typing into an emptied flow: %q", flowText(g))
	}
}

func TestFlowInlineObjects(t *testing.T) {
	obj := Inline{Object: &Rule{}, Block: true}
	f, st := editFlow(t, 0, Chars{Text: "ab"}, obj, Chars{Text: "cd"})
	if f.EditText() != "ab"+string(objectRune)+"cd" {
		t.Fatalf("text = %q", f.EditText())
	}
	sl := f.Slice(1, 4)
	if len(sl) != 3 || sl[1] != Item(obj) {
		t.Fatalf("slice keeps the object: %+v", sl)
	}
	f.SetCursors([]Cursor{{2, 3}})
	st.HandleKey("Backspace", false)
	if flowText(f) != "abcd" {
		t.Fatalf("deleting the object: %q", flowText(f))
	}
}

func TestFlowClipboardAndPaste(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "one two"})
	f.SetCursors([]Cursor{{0, 3}})
	if text, ok := st.Cut(); !ok || text != "one" || flowText(f) != " two" {
		t.Fatalf("cut %q -> %q", text, flowText(f))
	}
	if !st.Paste("1\n2") || flowText(f) != "1\n2 two" {
		t.Fatalf("paste: %q", flowText(f))
	}
	f.SetCursors([]Cursor{At(0), At(2)})
	if !st.Paste("A\nB") || flowText(f) != "A1\nB2 two" {
		t.Fatalf("one line per cursor: %q", flowText(f))
	}
	f.SetCursors([]Cursor{{0, 2}, {3, 5}})
	if text, ok := st.Copy(); !ok || text != "A1\nB2" {
		t.Fatalf("multi copy: %q", text)
	}
	if text, ok := st.Cut(); !ok || text != "A1\nB2" || flowText(f) != "\n two" {
		t.Fatalf("multi cut %q -> %q", text, flowText(f))
	}
	f.SetCursors([]Cursor{At(0)})
	if _, ok := st.Cut(); ok {
		t.Error("nothing selected: nothing to cut")
	}
	view, _ := selFlow(Chars{Text: "x"})
	if view.InsertText("y") || view.PasteText("y") {
		t.Error("view mode flows are not editable")
	}
	if _, ok := view.CutText(); ok {
		t.Error("cut in view mode")
	}
}

func TestFlowFormatting(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "hello world"})
	f.SetCursors([]Cursor{{0, 5}})
	st.HandleChord("Mod+B")
	items := f.ContentItems()
	if items[0].(Chars).Text != "hello" || items[0].(Chars).Format.FontWeight != 700 || items[1].(Chars).Format.FontWeight != 0 {
		t.Fatalf("bold: %+v", items)
	}
	if got := f.Cursors()[0]; got != (Cursor{0, 5}) {
		t.Errorf("the selection stays: %v", got)
	}
	st.HandleChord("Mod+B")
	if f.ContentItems()[0].(Chars).Format.FontWeight != 400 {
		t.Errorf("bold toggles off: %+v", f.ContentItems())
	}
	for chord, check := range map[string]func(Format) bool{
		"Mod+I":           func(x Format) bool { return x.FontStyle == StyleItalic },
		"Mod+U":           func(x Format) bool { return x.Underline },
		"Mod+Alt+Shift+X": nil,
		"Mod+Shift+X":     func(x Format) bool { return x.Strike },
		"Mod+.":           func(x Format) bool { return x.Superscript },
		"Mod+,":           func(x Format) bool { return x.Subscript && !x.Superscript },
	} {
		if check == nil {
			continue
		}
		st.HandleChord(chord)
		if !check(f.ContentItems()[0].(Chars).Format) {
			t.Errorf("%s did not apply: %+v", chord, f.ContentItems()[0])
		}
	}
	f.SetTextColor("#f00")
	f.SetHighlight("#ff0")
	if fm := f.ContentItems()[0].(Chars).Format; fm.Color != "#f00" || fm.Background != "#ff0" {
		t.Errorf("color/highlight: %+v", fm)
	}

	// No selection: the format is for the next typed text.
	f.SetCursors([]Cursor{At(11)})
	st.HandleChord("Mod+B")
	st.InsertText("!")
	last := f.ContentItems()
	if c := last[len(last)-1].(Chars); c.Text != "!" || c.Format.FontWeight != 700 {
		t.Errorf("typed text takes the pending format: %+v", last)
	}

	// Format painter: copy from one place, paste on another.
	f.SetCursors([]Cursor{{0, 5}})
	st.HandleChord("Mod+Alt+C")
	f.SetCursors([]Cursor{{6, 11}})
	st.HandleChord("Mod+Alt+V")
	if fm := f.Slice(6, 7)[0].(Chars).Format; fm.Color != "#f00" {
		t.Errorf("pasted format: %+v", fm)
	}
	f.SetCursors([]Cursor{At(3)})
	st.HandleChord("Mod+Alt+C") // caret only: copies the typing format
	st.HandleChord("Mod+A")
	if from, to := f.Selection(); from != 0 || to != len(f.flatten().runes) {
		t.Errorf("select all: %d..%d", from, to)
	}
	st.Formats = &FormatClipboard{}
	st.SetFormatClipboard(st.Formats)
	if st.HandleChord("Mod+Alt+V") {
		t.Error("nothing on the format clipboard")
	}
	if f.HandleCommand("nope") {
		t.Error("unknown command")
	}
}

func TestFlowViewModeSelectionCommands(t *testing.T) {
	f, p := selFlow(Chars{Text: "hello"})
	st := NewStage(p, 400, 400, f)
	if st.HandleChord("Mod+A") {
		t.Error("no selection: select-all is not ours in view mode")
	}
	f.SetSelection(0, 2)
	if from, to := f.Selection(); !st.HandleChord("Mod+A") || from != 0 || to != 2 {
		t.Error("select all with a selection")
	}
	if from, to := f.Selection(); to <= from || to != 5 {
		t.Errorf("select all selects everything: %d..%d", from, to)
	}
	if st.HandleChord("Mod+B") {
		t.Error("view mode cannot format")
	}
	st.HandleChord("Mod+Alt+C")
	if _, ok := st.Formats.Get(); !ok {
		t.Error("copy-format works on a view-mode selection")
	}
	if !st.HandleKey("Escape", false) || f.HasSelection() {
		t.Error("Escape clears a selection")
	}
	if st.HandleKey("Escape", false) {
		t.Error("Escape with nothing to do")
	}
}

func TestFlowMultiCursorAndFeatures(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "foo bar foo\nline two"})
	f.SetCursors([]Cursor{At(1)})
	for range 2 {
		st.HandleChord("Mod+D")
	}
	if len(f.Cursors()) != 2 {
		t.Fatalf("cursors: %v", f.Cursors())
	}
	st.InsertText("X")
	if flowText(f) != "X bar X\nline two" {
		t.Fatalf("typing at two cursors: %q", flowText(f))
	}
	st.HandleKey("Escape", false)
	if len(f.Cursors()) != 1 || !f.Editing() {
		t.Error("Escape: one cursor, still editing")
	}
	f.SetCursors([]Cursor{At(10)})
	st.HandleChord("Alt+ArrowUp")
	if flowText(f) != "line two\nX bar X" {
		t.Fatalf("move line: %q", flowText(f))
	}
	st.HandleChord("Alt+Shift+ArrowDown")
	if flowText(f) != "line two\nline two\nX bar X" {
		t.Fatalf("copy line: %q", flowText(f))
	}
	// Ctrl-click adds a cursor.
	x, y, h, _ := f.CaretRect(1)
	st.MouseDownMods(x, y+h/2, Mods{Ctrl: true})
	if len(f.Cursors()) < 2 {
		t.Errorf("ctrl+click: %v", f.Cursors())
	}
	st.MouseDownMods(-50, -50, Mods{Ctrl: true}) // outside the text: a plain press
	if f.Editing() {
		t.Error("a press outside the text stops editing")
	}
}

func TestFlowMarkdownPasteKeepsStructure(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "ab"})
	fc := &fakeClip{content: "**x**\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"}
	st.Clipboard = fc.clipboard()
	f.SetCursors([]Cursor{At(1)})
	st.HandleChord("Mod+Shift+V")
	hasTable := false
	for _, it := range f.ContentItems() {
		if _, ok := it.(Inline); ok {
			hasTable = true
		}
	}
	if !hasTable || !strings.HasPrefix(flowText(f), "ax") {
		t.Fatalf("a table pastes into a flow: %q %+v", flowText(f), f.ContentItems())
	}
	fc.written = ""
	f.SetCursors([]Cursor{{0, 3}})
	st.HandleChord("Mod+Shift+C")
	if !strings.Contains(fc.written, "**x**") {
		t.Errorf("copy as Markdown: %q", fc.written)
	}
	// Without object support, structure flattens to text.
	if got := flattenItems([]Item{Chars{Text: "a"}, Break{Kind: BreakPage}, Chars{Text: "b"}, Inline{}}, false); plainOf(got) != "a\nb" {
		t.Errorf("flatten without objects: %+v", got)
	}
}

func TestFlowCaretDrawn(t *testing.T) {
	f, st := editFlow(t, 3, Chars{Text: "hello"})
	p := st.Painter.(*fakePainter)
	st.Draw()
	if !p.has("fillRect 34 ") && !p.has("fillRect 33 ") {
		t.Errorf("caret should be drawn at x=34: %v", p.calls)
	}
	f.SetCaretVisible(false)
	p.calls = nil
	st.Draw()
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillRect 34 ") && strings.HasSuffix(c, "1.5 18") {
			t.Error("blinked-off caret still drawn")
		}
	}
	if !f.SetCaretVisible(true) {
		t.Error("blink toggles redraw while editing")
	}
	f.SetMode(ModeView)
	if f.Editing() || f.SetCaretVisible(false) || f.WantsFocus() {
		t.Error("leaving edit mode ends editing")
	}
}

func TestFlowOnChangeAndSelectionDraw(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "abc"})
	n := 0
	f.OnChange = func(*TextFlow) { n++ }
	st.InsertText("x")
	st.HandleKey("Backspace", false)
	if n != 2 {
		t.Errorf("OnChange calls = %d", n)
	}
	f.SetCursors([]Cursor{{0, 1}, {2, 3}})
	p := st.Painter.(*fakePainter)
	st.Draw()
	cnt := 0
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillRect") && strings.Contains(c, " 8 22") {
			cnt++
		}
	}
	if cnt < 2 {
		t.Errorf("both selections tinted: %v", p.calls)
	}
}
