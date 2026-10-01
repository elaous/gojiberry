package canvas

import (
	"reflect"
	"strings"
	"testing"
)

// stageWith returns a Stage with an editing FloatingText in it.
func stageWith(t *testing.T, text string) (*Stage, *FloatingText) {
	t.Helper()
	ft := editing(t, text)
	ft.Width, ft.Height = 400, 300
	st := NewStage(&fakePainter{}, 600, 400, ft)
	return st, ft
}

func TestRegistry(t *testing.T) {
	st := NewStage(&fakePainter{}, 1, 1)
	var names []string
	for _, f := range st.Features.Features() {
		names = append(names, f.Name())
	}
	if !reflect.DeepEqual(names, []string{"multi-cursor", "line-ops", "markdown-clipboard", "breaks"}) {
		t.Fatalf("default features = %v", names)
	}
	if !st.Features.Has(CmdMoveLineUp) || st.Features.Keymap()["Mod+D"] != CmdAddNextMatch {
		t.Error("commands and chords are registered")
	}
	found := false
	for _, c := range st.Features.Commands() {
		if c.Name == CmdDeleteLine && c.Feature == "line-ops" && c.Title != "" {
			found = true
		}
	}
	if !found {
		t.Error("Commands lists delete-line with its feature and title")
	}
	mustPanic := func(name string, fn func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s did not panic", name)
			}
		}()
		fn()
	}
	mustPanic("duplicate feature", func() { NewRegistry(LineOps{}, LineOps{}) })
	mustPanic("duplicate command", func() {
		r := NewRegistry()
		r.AddCommand("x", "X", "", nil)
		r.AddCommand("x", "X", "", nil)
	})
	// A registry without a feature has no such command.
	r := NewRegistry(LineOps{})
	if r.Has(CmdAddNextMatch) || r.Run(CmdMoveLineUp, &CommandContext{}) {
		t.Error("only registered commands run, and only on a target")
	}
	// A user keymap wins over a feature chord.
	st2, ft := stageWith(t, "a\nb")
	st2.Keymap = Keymap{"Alt+ArrowUp": CmdSelectAll}
	ft.caret, ft.anchor = 2, 2
	st2.HandleChord("Alt+ArrowUp")
	if from, to := ft.Selection(); from != 0 || to != 3 {
		t.Errorf("user binding should win: selection %d..%d", from, to)
	}
}

func TestAddNextMatchAndTyping(t *testing.T) {
	st, ft := stageWith(t, "foo bar foo baz foo")
	ft.caret, ft.anchor = 1, 1
	for i, want := range []int{1, 2, 3} {
		if !st.HandleChord("Mod+D") {
			t.Fatalf("press %d not handled", i+1)
		}
		if got := len(ft.Cursors()); got != want {
			t.Fatalf("after press %d: %d cursors, want %d", i+1, got, want)
		}
	}
	if st.HandleChord("Mod+D") {
		t.Error("with every occurrence selected there is nothing to add")
	}
	st.InsertText("X")
	if ft.Text() != "X bar X baz X" {
		t.Fatalf("typing at three cursors: %q", ft.Text())
	}
	if got := ft.Cursors(); len(got) != 3 || got[0] != At(1) || got[1] != At(7) || got[2] != At(13) {
		t.Fatalf("cursors after typing: %v", got)
	}
	st.HandleKey("Backspace", false)
	if ft.Text() != " bar  baz " {
		t.Fatalf("backspace at three cursors: %q", ft.Text())
	}
	st.HandleKey("ArrowRight", false)
	st.InsertText("-")
	if ft.Text() != " -bar  -baz -" {
		t.Fatalf("after moving all cursors: %q", ft.Text())
	}
	st.HandleKey("Escape", false)
	if len(ft.Cursors()) != 1 || !ft.Editing() {
		t.Error("Escape goes back to one cursor and keeps editing")
	}
}

func TestAddNextMatchWrapsAround(t *testing.T) {
	st, ft := stageWith(t, "ab ab")
	ft.anchor, ft.caret = 3, 5 // the second "ab"
	if !st.HandleChord("Mod+D") {
		t.Fatal("not handled")
	}
	cs := ft.Cursors()
	if len(cs) != 2 || cs[1] != (Cursor{Anchor: 0, Caret: 2}) {
		t.Fatalf("wrap-around cursor = %v", cs)
	}
	ft.caret, ft.anchor = 0, 0
	ft.extra = nil
	ft.SetText("  ")
	ft.caret, ft.anchor = 1, 1
	if st.HandleChord("Mod+D") {
		t.Error("no word at the caret: nothing to select")
	}
}

func TestSelectAllMatches(t *testing.T) {
	st, ft := stageWith(t, "a_b x a_b y a_b")
	ft.caret, ft.anchor = 1, 1
	if !st.HandleChord("Mod+Shift+L") {
		t.Fatal("not handled")
	}
	if got := ft.Cursors(); len(got) != 3 || got[2] != (Cursor{Anchor: 12, Caret: 15}) {
		t.Fatalf("cursors = %v", got)
	}
	st.InsertText("Z")
	if ft.Text() != "Z x Z y Z" {
		t.Errorf("text = %q", ft.Text())
	}
}

func TestCursorsAtLineEnds(t *testing.T) {
	st, ft := stageWith(t, "ab\ncd\nef")
	ft.anchor, ft.caret = 0, 8
	if !st.HandleChord("Alt+Shift+I") {
		t.Fatal("not handled")
	}
	want := []Cursor{At(2), At(5), At(8)}
	if got := ft.Cursors(); !reflect.DeepEqual(got, want) {
		t.Fatalf("cursors = %v, want %v", got, want)
	}
	st.InsertText(";")
	if ft.Text() != "ab;\ncd;\nef;" {
		t.Errorf("text = %q", ft.Text())
	}
	// A selection ending at the start of a line leaves that line out.
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 4}})
	st.HandleChord("Alt+Shift+I")
	if got := ft.Cursors(); len(got) != 1 || got[0] != At(3) {
		t.Errorf("cursors = %v", got)
	}
}

func TestAddCursorAboveBelow(t *testing.T) {
	st, ft := stageWith(t, "abcd\nef\nghij")
	ft.caret, ft.anchor = 3, 3
	st.HandleChord("Mod+Alt+ArrowDown")
	st.HandleChord("Mod+Alt+ArrowDown")
	if got := ft.Cursors(); len(got) != 3 || got[1] != At(7) || got[2] != At(10) {
		t.Fatalf("cursors = %v (a short line clamps the column)", got)
	}
	if st.HandleChord("Mod+Alt+ArrowDown") {
		t.Error("no line below the last")
	}
	ft.SetCursors([]Cursor{At(11)})
	st.HandleChord("Mod+Alt+ArrowUp")
	if got := ft.Cursors(); len(got) != 2 || got[0] != At(7) {
		t.Fatalf("cursors = %v", got)
	}
	ft.SetCursors([]Cursor{At(1)})
	if st.HandleChord("Mod+Alt+ArrowUp") {
		t.Error("no line above the first")
	}
	if !st.RunCommand(CmdAddCursorBelow) || !st.RunCommand(CmdClearCursors) || st.RunCommand(CmdClearCursors) {
		t.Error("clear-cursors applies only with several cursors")
	}
}

func TestCtrlClickAddsCursor(t *testing.T) {
	st, ft := stageWith(t, "hello world")
	x, y, h, _ := ft.inner.CaretRect(6)
	st.MouseDownMods(x, y+h/2, Mods{Ctrl: true})
	if got := ft.Cursors(); len(got) != 2 || got[0] != At(6) && got[1] != At(6) {
		t.Fatalf("cursors = %v", got)
	}
	st.InsertText("_")
	if strings.Count(ft.Text(), "_") != 2 {
		t.Fatalf("text = %q", ft.Text())
	}
	// A plain click goes back to one cursor.
	st.MouseDown(x, y+h/2)
	st.MouseUp(0, 0)
	if len(ft.Cursors()) != 1 {
		t.Error("plain click should drop the extra cursors")
	}
	// Cmd works like Ctrl; a Ctrl+click outside the box is not ours.
	x2, _, _, _ := ft.inner.CaretRect(2)
	st.MouseDownMods(x2, y+h/2, Mods{Meta: true})
	if len(ft.Cursors()) != 2 {
		t.Error("Cmd+click adds a cursor")
	}
}

func TestMultiCursorCopyCutPaste(t *testing.T) {
	st, ft := stageWith(t, "one two three")
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 3}, {Anchor: 4, Caret: 7}, {Anchor: 8, Caret: 13}})
	if text, ok := st.Copy(); !ok || text != "one\ntwo\nthree" {
		t.Fatalf("copy = %q, %v", text, ok)
	}
	if !st.Paste("A\nB\nC") || ft.Text() != "A B C" {
		t.Fatalf("one line per cursor: %q", ft.Text())
	}
	ft.SetCursors([]Cursor{At(1), At(3), At(5)})
	st.Paste("-")
	if ft.Text() != "A- B- C-" {
		t.Fatalf("same text at each cursor: %q", ft.Text())
	}
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 2}, {Anchor: 3, Caret: 5}})
	if text, ok := st.Cut(); !ok || text != "A-\nB-" || ft.Text() != "  C-" {
		t.Fatalf("cut = %q, text = %q", text, ft.Text())
	}
	ft.SetCursors([]Cursor{At(0), At(1)})
	if _, ok := st.Copy(); ok {
		t.Error("nothing selected: nothing to copy")
	}
}

func TestMultiCursorDrawsAllCursors(t *testing.T) {
	st, ft := stageWith(t, "ab ab")
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 2}, {Anchor: 3, Caret: 5}})
	p := st.Painter.(*fakePainter)
	st.Draw()
	n := 0
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillRect") && strings.Contains(c, "16 ") { // 2 runes = 16px selection
			n++
		}
	}
	if n < 2 {
		t.Errorf("both selections should be tinted, got %d 16px rects in %v", n, p.calls)
	}
}

func TestMultiCursorArrowsHomeEnd(t *testing.T) {
	st, ft := stageWith(t, "abc def")
	ft.SetCursors([]Cursor{At(1), At(5)})
	st.HandleKey("End", false)
	if got := ft.Cursors(); len(got) != 1 || got[0] != At(7) {
		t.Fatalf("both cursors reach the end and merge: %v", got)
	}
	ft.SetCursors([]Cursor{At(1), At(5)})
	st.HandleKey("Home", false)
	if got := ft.Cursors(); len(got) != 1 || got[0] != At(0) {
		t.Fatalf("home: %v", got)
	}
	ft.SetCursors([]Cursor{At(1), At(5)})
	st.HandleKey("ArrowRight", true) // extend
	if got := ft.Cursors(); got[0] != (Cursor{Anchor: 1, Caret: 2}) || got[1] != (Cursor{Anchor: 5, Caret: 6}) {
		t.Fatalf("shift+right extends every selection: %v", got)
	}
	st.HandleKey("ArrowLeft", false) // collapse to the start
	if got := ft.Cursors(); got[0] != At(1) || got[1] != At(5) {
		t.Fatalf("left collapses to selection starts: %v", got)
	}
	st.HandleKey("Enter", false)
	if ft.Text() != "a\nbc d\nef" {
		t.Fatalf("enter at both cursors: %q", ft.Text())
	}
	st.HandleKey("ArrowDown", false)
	st.HandleKey("ArrowUp", false)
	st.HandleKey("F5", false) // unhandled keys fall through
}

// --- line operations ----------------------------------------------------

func boldLine(ft *FloatingText, from, to int) {
	for i := from; i < to; i++ {
		ft.runes[i].f.FontWeight = 700
	}
}

func TestMoveLines(t *testing.T) {
	st, ft := stageWith(t, "aa\nbb\ncc")
	boldLine(ft, 3, 5)
	ft.SetCursors([]Cursor{At(4)})
	if !st.HandleChord("Alt+ArrowUp") || ft.Text() != "bb\naa\ncc" {
		t.Fatalf("move up: %q", ft.Text())
	}
	if got := ft.Cursors()[0]; got != At(1) {
		t.Errorf("the cursor goes with the line: %v", got)
	}
	if ft.FormatAt(0).FontWeight != 700 || ft.FormatAt(3).FontWeight != 0 {
		t.Error("formatting travels with the line")
	}
	if st.HandleChord("Alt+ArrowUp") {
		t.Error("the first line cannot move up")
	}
	st.HandleChord("Alt+ArrowDown")
	st.HandleChord("Alt+ArrowDown")
	if ft.Text() != "aa\ncc\nbb" || ft.Cursors()[0] != At(7) {
		t.Fatalf("move down twice: %q %v", ft.Text(), ft.Cursors())
	}
	if st.HandleChord("Alt+ArrowDown") {
		t.Error("the last line cannot move down")
	}
}

func TestMoveSelectedBlockAndSeveralBlocks(t *testing.T) {
	st, ft := stageWith(t, "1\n2\n3\n4\n5")
	ft.SetCursors([]Cursor{{Anchor: 2, Caret: 5}}) // lines 2 and 3
	st.HandleChord("Alt+ArrowUp")
	if ft.Text() != "2\n3\n1\n4\n5" {
		t.Fatalf("block up: %q", ft.Text())
	}
	if got := ft.Cursors()[0]; got != (Cursor{Anchor: 0, Caret: 3}) {
		t.Errorf("selection moves too: %v", got)
	}
	ft.SetText("1\n2\n3\n4\n5")
	ft.SetCursors([]Cursor{At(2), At(6)}) // lines 2 and 4
	st.HandleChord("Alt+ArrowDown")
	if ft.Text() != "1\n3\n2\n5\n4" {
		t.Fatalf("two blocks down: %q", ft.Text())
	}
	ft.SetText("1\n2\n3\n4\n5")
	ft.SetCursors([]Cursor{At(2), At(6)})
	st.HandleChord("Alt+ArrowUp")
	if ft.Text() != "2\n1\n4\n3\n5" {
		t.Fatalf("two blocks up: %q", ft.Text())
	}
	// A selection ending at the start of a line excludes that line.
	ft.SetText("a\nb\nc")
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 2}})
	st.HandleChord("Alt+ArrowDown")
	if ft.Text() != "b\na\nc" {
		t.Errorf("selection ending at line start: %q", ft.Text())
	}
}

func TestDuplicateLines(t *testing.T) {
	st, ft := stageWith(t, "ab\ncd")
	boldLine(ft, 0, 2)
	ft.SetCursors([]Cursor{At(1)})
	st.HandleChord("Alt+Shift+ArrowDown")
	if ft.Text() != "ab\nab\ncd" || ft.Cursors()[0] != At(4) {
		t.Fatalf("copy down: %q %v", ft.Text(), ft.Cursors())
	}
	if ft.FormatAt(3).FontWeight != 700 {
		t.Error("the copy keeps its formatting")
	}
	st.HandleChord("Alt+Shift+ArrowUp")
	if ft.Text() != "ab\nab\nab\ncd" || ft.Cursors()[0] != At(4) {
		t.Fatalf("copy up keeps the cursor on the upper copy: %q %v", ft.Text(), ft.Cursors())
	}
	// Several cursors duplicate their own lines.
	ft.SetText("x\ny\nz")
	ft.SetCursors([]Cursor{At(0), At(4)})
	st.HandleChord("Alt+Shift+ArrowDown")
	if ft.Text() != "x\nx\ny\nz\nz" {
		t.Fatalf("two lines: %q", ft.Text())
	}
	if got := ft.Cursors(); got[0] != At(2) || got[1] != At(8) {
		t.Errorf("cursors = %v", got)
	}
}

func TestDeleteLines(t *testing.T) {
	st, ft := stageWith(t, "a\nb\nc")
	ft.SetCursors([]Cursor{At(2)})
	st.HandleChord("Mod+Shift+K")
	if ft.Text() != "a\nc" || ft.Cursors()[0] != At(2) {
		t.Fatalf("middle: %q %v", ft.Text(), ft.Cursors())
	}
	st.HandleChord("Mod+Shift+K")
	if ft.Text() != "a" || ft.Cursors()[0] != At(1) {
		t.Fatalf("last line takes the newline before it: %q %v", ft.Text(), ft.Cursors())
	}
	st.HandleChord("Mod+Shift+K")
	if ft.Text() != "" {
		t.Fatalf("only line: %q", ft.Text())
	}
	ft.SetText("1\n2\n3\n4")
	ft.SetCursors([]Cursor{At(0), At(4), At(6)})
	st.HandleChord("Mod+Shift+K")
	if ft.Text() != "2" {
		t.Fatalf("several cursors: %q", ft.Text())
	}
}

func TestFeatureCommandsNeedEditing(t *testing.T) {
	ft, _ := newFloat("a\nb") // edit mode but not editing yet
	st := NewStage(&fakePainter{}, 100, 100, ft)
	if st.HandleChord("Alt+ArrowDown") || st.EditTarget() != nil {
		t.Error("nothing to edit before the box is clicked")
	}
	view := NewFloatingText("a", 0, 0, 10, 10)
	if view.EditTarget() != nil {
		t.Error("view mode has no edit target")
	}
}

func TestEditTargetThroughScrollView(t *testing.T) {
	ft := editing(t, "a\nb")
	sv := NewScrollView(0, 0, 300, 300)
	sv.Add(ft, 300, 300)
	st := NewStage(&fakePainter{}, 300, 300, sv)
	ft.SetCursors([]Cursor{At(0)})
	if st.EditTarget() == nil || !st.HandleChord("Alt+ArrowDown") || ft.Text() != "b\na" {
		t.Fatalf("a scroll view hands its content's edit target up: %q", ft.Text())
	}
}

// --- Markdown clipboard ---------------------------------------------------

func TestMarkdownFromItems(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []Item
		want  string
	}{
		{"plain", []Item{Chars{Text: "hello"}}, "hello"},
		{"bold italic", []Item{Chars{Text: "x", Format: Format{FontWeight: 700, FontStyle: "italic"}}}, "***x***"},
		{"marks", []Item{
			Chars{Text: "a", Format: Format{Strike: true}},
			Chars{Text: " b ", Format: Format{Underline: true}},
			Chars{Text: "c", Format: Format{Background: "#ff0"}},
			Chars{Text: "d", Format: Format{Superscript: true}},
			Chars{Text: "e", Format: Format{Subscript: true}},
		}, "~~a~~ ++b++ ==c==^d^~e~"},
		{"spaces stay outside", []Item{Chars{Text: " bold ", Format: Format{FontWeight: 700}}}, " **bold** "},
		{"code", []Item{Chars{Text: "x := 1", Format: Format{FontFamily: "monospace", FontWeight: 700}}}, "`x := 1`"},
		{"heading", []Item{Chars{Text: "Title\nbody", Format: Format{Scale: 2}}}, "# Title\n# body"},
		{"heading then text", []Item{Chars{Text: "Title", Format: Format{Scale: 1.6}}, Chars{Text: "\nbody"}}, "## Title\nbody"},
		{"escapes", []Item{Chars{Text: "2*3 ~ [x] a_b _c ++ 1+1"}}, `2\*3 \~ \[x\] a_b \_c \+\+ 1+1`},
		{"line start", []Item{Chars{Text: "# not a heading\n- not a list"}}, "\\# not a heading\n\\- not a list"},
		{"blank run", []Item{Chars{Text: "  ", Format: Format{FontWeight: 700}}}, "  "},
		{"non-chars skipped", []Item{Break{Kind: BreakPage}, Chars{Text: "a"}, ColumnConfig{}}, "a"},
	} {
		if got := MarkdownFromItems(tc.items); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMarkdownRoundTrip(t *testing.T) {
	src := []Item{
		Chars{Text: "Plain "},
		Chars{Text: "bold", Format: Format{FontWeight: 700}},
		Chars{Text: " and "},
		Chars{Text: "struck", Format: Format{Strike: true}},
		Chars{Text: " x"},
		Chars{Text: "2", Format: Format{Superscript: true}},
	}
	md := MarkdownFromItems(src)
	back := flattenItems(Markdown{}.Transform(md), false)
	if plainOf(back) != "Plain bold and struck x2" {
		t.Fatalf("text = %q (markdown %q)", plainOf(back), md)
	}
	var sawBold, sawStrike, sawSup bool
	for _, it := range back {
		c := it.(Chars)
		switch c.Text {
		case "bold":
			sawBold = c.Format.FontWeight >= 600
		case "struck":
			sawStrike = c.Format.Strike
		case "2":
			sawSup = c.Format.Superscript
		}
	}
	if !sawBold || !sawStrike || !sawSup {
		t.Errorf("formats lost in %q: %+v", md, back)
	}
}

type fakeClip struct {
	written string
	content string
}

func (f *fakeClip) clipboard() Clipboard {
	return Clipboard{
		Write: func(s string) { f.written = s },
		Read:  func(fn func(string)) { fn(f.content) },
	}
}

func TestCopyAndPasteMarkdownCommands(t *testing.T) {
	st, ft := stageWith(t, "hello world")
	boldLine(ft, 0, 5)
	fc := &fakeClip{}
	st.Clipboard = fc.clipboard()
	redraws := 0
	st.Redraw = func() { redraws++ }

	ft.SetCursors([]Cursor{At(3)})
	if st.HandleChord("Mod+Shift+C") {
		t.Error("nothing selected: nothing to copy")
	}
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 11}})
	if !st.HandleChord("Mod+Shift+C") || fc.written != "**hello** world" {
		t.Fatalf("copied %q", fc.written)
	}

	fc.content = "some **bold** and ~~gone~~ text\n"
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 11}})
	if !st.HandleChord("Mod+Shift+V") {
		t.Fatal("paste not handled")
	}
	if ft.Text() != "some bold and gone text" {
		t.Fatalf("pasted text = %q (the marks must not be characters)", ft.Text())
	}
	if ft.FormatAt(5).FontWeight < 600 || !ft.FormatAt(15).Strike || ft.FormatAt(0).FontWeight != 0 {
		t.Errorf("pasted formatting wrong: %+v %+v", ft.FormatAt(5), ft.FormatAt(15))
	}
	if redraws != 1 || ft.Caret() != len([]rune("some bold and gone text")) {
		t.Errorf("redraws = %d, caret = %d", redraws, ft.Caret())
	}

	// No clipboard: the commands decline.
	st.Clipboard = Clipboard{}
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 4}})
	if st.HandleChord("Mod+Shift+C") || st.HandleChord("Mod+Shift+V") {
		t.Error("without a clipboard the commands decline")
	}
	st.Clipboard = fc.clipboard()
	fc.content = ""
	if st.HandleChord("Mod+Shift+V"); ft.Text() != "some bold and gone text" {
		t.Error("an empty clipboard pastes nothing")
	}
}

func TestPasteMarkdownPerCursorAndMultiCopy(t *testing.T) {
	st, ft := stageWith(t, "a b")
	fc := &fakeClip{content: "**x**\ny"}
	st.Clipboard = fc.clipboard()
	ft.SetCursors([]Cursor{At(0), At(2)})
	st.HandleChord("Mod+Shift+V")
	if ft.Text() != "xa yb" || ft.FormatAt(0).FontWeight < 600 || ft.FormatAt(3).FontWeight != 0 {
		t.Fatalf("one line per cursor: %q", ft.Text())
	}
	if got := ft.Cursors(); got[0] != At(1) || got[1] != At(4) {
		t.Errorf("cursors = %v", got)
	}
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 2}, {Anchor: 3, Caret: 5}})
	st.HandleChord("Mod+Shift+C")
	if fc.written != "**x**a\nyb" {
		t.Errorf("copied %q", fc.written)
	}
}

func TestCustomFeature(t *testing.T) {
	var ran bool
	r := NewRegistry(featureFunc{"upper", func(r *Registry) {
		r.AddCommand("text.upper", "Upper case", "Mod+Shift+U", func(c *CommandContext) bool {
			ran = true
			txt := c.Target.EditText()
			c.Target.Replace([]Edit{{From: 0, To: len([]rune(txt)), Items: plainItems(strings.ToUpper(txt))}})
			return true
		})
	}})
	st, ft := stageWith(t, "abc")
	st.Features = r
	if !st.HandleChord("Mod+Shift+U") || !ran || ft.Text() != "ABC" {
		t.Fatalf("custom feature: ran=%v text=%q", ran, ft.Text())
	}
	if st.HandleChord("Alt+ArrowUp") {
		t.Error("features left out of the registry are off")
	}
}

type featureFunc struct {
	name string
	fn   func(*Registry)
}

func (f featureFunc) Name() string        { return f.name }
func (f featureFunc) Install(r *Registry) { f.fn(r) }

// Every default chord is written the way Chord builds it, or the browser
// glue could never match it.
func TestDefaultChordsMatchChord(t *testing.T) {
	for chord := range NewRegistry(DefaultFeatures()...).Keymap() {
		parts := strings.Split(chord, "+")
		key := parts[len(parts)-1]
		var ctrl, shift, alt bool
		for _, p := range parts[:len(parts)-1] {
			switch p {
			case "Mod":
				ctrl = true
			case "Shift":
				shift = true
			case "Alt":
				alt = true
			}
		}
		if got := Chord(key, ctrl, shift, alt, false); got != chord {
			t.Errorf("chord %q is spelled %q by Chord", chord, got)
		}
	}
}

func TestBreaksFeature(t *testing.T) {
	p := &fakePainter{}
	ps := NewPageSet(
		Page{Width: 200, Height: 240, Margin: Insets{Left: 10, Top: 10, Right: 10, Bottom: 10}},
		Page{Width: 200, Height: 240, Margin: Insets{Left: 10, Top: 10, Right: 10, Bottom: 10}},
	)
	ps.Style.Pad = 0
	f := NewTextFlow("", 0, 0, 0, 0)
	f.Pages = ps
	f.Items = []Item{Chars{Text: "abcd"}}
	f.SetPainter(p)
	f.SetMode(ModeEdit)
	st := NewStage(p, 500, 600, f)
	x0, y0, _, _ := ps.ContentRect(0)
	f.MouseDown(x0+16, y0+5)
	f.MouseUp(0, 0)
	if !st.HandleChord("Mod+Enter") {
		t.Fatal("page break not handled")
	}
	if f.Layout().Pages != 2 {
		t.Fatalf("pages = %d, want 2", f.Layout().Pages)
	}
	if c := f.Cursors()[0]; c.Caret != 2 || !c.After {
		t.Fatalf("the caret goes after the break: %+v", c)
	}
	st.InsertText("X")
	if flowText(f) != "abXcd" {
		t.Fatalf("typed text: %q", flowText(f))
	}
	items := f.ContentItems()
	if len(items) != 3 || items[0].(Chars).Text != "ab" || items[2].(Chars).Text != "Xcd" {
		t.Fatalf("typing after the break goes behind it: %+v", items)
	}
	for _, ln := range f.Layout().Lines {
		if ln.Page == 1 && ln.Start != 2 {
			t.Errorf("page 2 starts with the text after the break: %+v", ln)
		}
	}
	// Clicking at the start of the second page's line puts the caret after the break.
	x1, y1, _, _ := ps.ContentRect(1)
	f.MouseDown(x1, y1+5)
	f.MouseUp(0, 0)
	if c := f.Cursors()[0]; c.Caret != 2 || !c.After {
		t.Fatalf("click after the break: %+v", c)
	}
	st.Draw() // the caret is drawn on page 2
	// Backspace there removes the break.
	st.HandleKey("Backspace", false)
	if len(f.ContentItems()) != 1 || f.Layout().Pages != 1 {
		t.Fatalf("backspace after a break removes it: %+v", f.ContentItems())
	}
	// A column break, then Delete in front of it.
	f.SetCursors([]Cursor{At(1)})
	st.HandleChord("Mod+Shift+Enter")
	var kinds []BreakKind
	for _, it := range f.ContentItems() {
		if b, ok := it.(Break); ok {
			kinds = append(kinds, b.Kind)
		}
	}
	if len(kinds) != 1 || kinds[0] != BreakColumn {
		t.Fatalf("column break: %+v", f.ContentItems())
	}
	f.SetCursors([]Cursor{At(1)})
	st.HandleKey("Delete", false)
	if len(f.ContentItems()) != 1 {
		t.Fatalf("delete in front of a break removes it: %+v", f.ContentItems())
	}
	// A selection is replaced by the break; a FloatingText declines.
	f.SetCursors([]Cursor{{Anchor: 1, Caret: 3}})
	st.HandleChord("Mod+Enter")
	if flowText(f) != "acd" || len(f.ContentItems()) != 3 {
		t.Fatalf("selection replaced: %q %+v", flowText(f), f.ContentItems())
	}
	fst, _ := stageWith(t, "text")
	if fst.HandleChord("Mod+Enter") {
		t.Error("a floating text cannot hold a break")
	}
}
