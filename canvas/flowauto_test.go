package canvas

import (
	"reflect"
	"testing"
)

func typeFlow(f *TextFlow, s string) {
	for _, r := range s {
		f.InsertText(string(r))
	}
}

func TestTextFlowAutoClose(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: "x"})
	f.AutoClose = DefaultAutoClose()
	f.SetCursors([]Cursor{At(1)})
	typeFlow(f, `(a[b "c`)
	if flowText(f) != `x(a[b "c"])` {
		t.Fatalf("pairs: %q", flowText(f))
	}
	typeFlow(f, `"])`)
	if flowText(f) != `x(a[b "c"])` || f.Cursors()[0] != At(11) {
		t.Fatalf("closers are stepped over: %q %v", flowText(f), f.Cursors())
	}
	f.SetCursors([]Cursor{At(2)})
	typeFlow(f, "'")
	if flowText(f) != `x('a[b "c"])` {
		t.Fatalf("a quote before a word: %q", flowText(f))
	}
	f.SetCursors([]Cursor{At(4)})
	typeFlow(f, "'")
	if flowText(f) != `x('a'[b "c"])` {
		t.Fatalf("a quote after a word: %q", flowText(f))
	}
}

func TestTextFlowAutoCloseWrapsSelectionAndManyCursors(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: "foo bar"})
	f.AutoClose = DefaultAutoClose()
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 3}})
	f.InsertText("(")
	if flowText(f) != "(foo) bar" || f.Cursors()[0] != (Cursor{Anchor: 1, Caret: 4}) {
		t.Fatalf("wrap: %q %v", flowText(f), f.Cursors())
	}
	f.SetCursors([]Cursor{{Anchor: 1, Caret: 4}, {Anchor: 6, Caret: 9}})
	f.InsertText("[")
	if flowText(f) != "([foo]) [bar]" {
		t.Fatalf("two wraps: %q", flowText(f))
	}
	f.SetCursors([]Cursor{At(0), At(13)})
	f.InsertText("{")
	if flowText(f) != "{}([foo]) [bar]{}" {
		t.Fatalf("a pair at each cursor: %q", flowText(f))
	}
	if got := f.Cursors(); !reflect.DeepEqual(got, []Cursor{At(1), At(16)}) {
		t.Fatalf("cursors inside the pairs: %v", got)
	}
	f.SetCursors([]Cursor{At(1), At(16)})
	f.InsertText("}") // steps over at both
	if got := f.Cursors(); flowText(f) != "{}([foo]) [bar]{}" || !reflect.DeepEqual(got, []Cursor{At(2), At(17)}) {
		t.Fatalf("overtype at both: %q %v", flowText(f), got)
	}
}

func TestTextFlowNoAutoCloseByDefault(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: ""})
	typeFlow(f, "(")
	if flowText(f) != "(" {
		t.Fatalf("%q", flowText(f))
	}
}

func TestTextFlowCompletion(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: "alpha alpine alps\n"})
	f.Vocabulary = []string{"almond"}
	f.SetCursors([]Cursor{At(18)})
	typeFlow(f, "al")
	if got := f.Suggestions(); !reflect.DeepEqual(got, []string{"alps", "alpine", "alpha", "almond"}) {
		t.Fatalf("suggestions: %v", got)
	}
	f.HandleKey("ArrowDown", false)
	f.HandleKey("ArrowDown", false)
	if f.sugg.Hover() != 2 {
		t.Fatalf("hover %d", f.sugg.Hover())
	}
	f.HandleKey("ArrowUp", false)
	f.HandleKey("ArrowUp", false)
	f.HandleKey("ArrowUp", false)
	if f.sugg.Hover() != 3 {
		t.Fatalf("hover wraps: %d", f.sugg.Hover())
	}
	f.HandleKey("Tab", false)
	if flowText(f) != "alpha alpine alps\nalmond" || f.Cursors()[0] != At(24) || len(f.Suggestions()) != 0 {
		t.Fatalf("accepted: %q %v", flowText(f), f.Cursors())
	}
	f.HandleKey("Backspace", false)
	if len(f.Suggestions()) == 0 {
		t.Fatal("Backspace keeps completing")
	}
	f.HandleKey("Escape", false)
	if len(f.Suggestions()) != 0 || !f.Editing() {
		t.Fatal("Escape closes the popup and stays in the text")
	}
	typeFlow(f, "p")
	f.HandleKey("ArrowLeft", false)
	if len(f.Suggestions()) != 0 {
		t.Fatal("moving closes it")
	}
}

func TestTextFlowCompleterAndOff(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: ""})
	typeFlow(f, "hello")
	if len(f.Suggestions()) != 0 {
		t.Fatal("off by default")
	}
	f.Completer = CompleterFunc(func(p string) []string { return []string{p + "!"} })
	f.InsertText("x")
	if got := f.Suggestions(); !reflect.DeepEqual(got, []string{"hellox!"}) {
		t.Fatal(got)
	}
	f.sugg.Activate(0)
	if flowText(f) != "hellox!" {
		t.Fatalf("custom completer: %q", flowText(f))
	}
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 3}})
	f.InsertText("y")
	if len(f.Suggestions()) != 0 {
		t.Fatal("none while selecting / short prefix")
	}
	f.SetCursors([]Cursor{At(1), At(3)})
	f.InsertText("zz")
	if len(f.Suggestions()) != 0 {
		t.Fatal("none with several cursors, or after a paste")
	}
}

func TestTextFlowCompletionPopupDrawsAndClicks(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "gopher gopro\n"})
	p := st.Painter.(*fakePainter)
	f.Complete = true
	f.SetCursors([]Cursor{At(13)})
	typeFlow(f, "go")
	if len(f.Suggestions()) != 2 {
		t.Fatal(f.Suggestions())
	}
	before := len(p.calls)
	f.DrawOverlay()
	if len(p.calls) == before {
		t.Fatal("the popup is drawn")
	}
	x, y, _, _ := f.SuggestionMenu().Rect()
	f.MouseDown(x+5, y+5)
	if len(f.Suggestions()) != 0 || flowText(f) == "gopher gopro\ngo" {
		t.Fatalf("clicking an entry accepts it: %q", flowText(f))
	}
	typeFlow(f, " go")
	f.MouseDown(3, 3) // a click elsewhere closes the popup
	if len(f.Suggestions()) != 0 {
		t.Fatal("click elsewhere closes")
	}
	f.SetViewport(0, 0, 100, 100)
	f.SetMode(ModeView)
	f.DrawOverlay()
}
