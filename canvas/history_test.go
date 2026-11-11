package canvas

import "testing"

func TestUndoRedoTypingRunsAndDeletes(t *testing.T) {
	f, st := editFlow(t, 5, Chars{Text: "hello world"})
	typeText2(f, " big") // one run
	if flowText(f) != "hello big world" {
		t.Fatalf("typed: %q", flowText(f))
	}
	st.HandleKey("Backspace", false)
	st.HandleKey("Backspace", false) // a run of deletes
	if flowText(f) != "hello b world" {
		t.Fatalf("deleted: %q", flowText(f))
	}
	if !st.HandleChord("Mod+Z") || flowText(f) != "hello big world" {
		t.Fatalf("undo the deletes in one step: %q", flowText(f))
	}
	if !st.HandleChord("Mod+Z") || flowText(f) != "hello world" || f.Cursors()[0].Caret != 5 {
		t.Fatalf("undo the typing in one step, caret back: %q %v", flowText(f), f.Cursors())
	}
	if st.HandleChord("Mod+Z") {
		t.Error("nothing more to undo")
	}
	if !st.HandleChord("Mod+Shift+Z") || flowText(f) != "hello big world" {
		t.Fatalf("redo: %q", flowText(f))
	}
	if !st.HandleChord("Mod+Y") || flowText(f) != "hello b world" {
		t.Fatalf("Ctrl+Y redoes too: %q", flowText(f))
	}
	if st.HandleChord("Mod+Y") {
		t.Error("nothing more to redo")
	}
	st.HandleChord("Mod+Z")
	st.InsertText("!")
	if st.HandleChord("Mod+Y") {
		t.Error("typing after an undo clears redo")
	}
}

func typeText2(f *TextFlow, s string) {
	for _, r := range s {
		f.InsertText(string(r))
	}
}

func TestMovingBreaksATypingRun(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "abc"})
	f.SetCursors([]Cursor{At(3)})
	typeText2(f, "12")
	st.HandleKey("ArrowLeft", false) // ends the run
	typeText2(f, "X")
	if flowText(f) != "abc1X2" {
		t.Fatalf("text: %q", flowText(f))
	}
	st.HandleChord("Mod+Z")
	if flowText(f) != "abc12" {
		t.Errorf("only the X is undone: %q", flowText(f))
	}
	st.HandleChord("Mod+Z")
	if flowText(f) != "abc" {
		t.Errorf("then the run: %q", flowText(f))
	}
}

func TestUndoRestoresFormattingParagraphsAndStructure(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "one\ntwo"})
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 3}})
	st.HandleChord("Mod+B")
	st.RunCommand(CmdBulletList)
	st.HandleChord("Mod+Alt+Enter") // a rule replaces the selection
	st.HandleChord("Mod+Enter")
	steps := 0
	for st.HandleChord("Mod+Z") {
		steps++
	}
	if steps != 4 {
		t.Errorf("one undo step per command: %d", steps)
	}
	if items := f.ContentItems(); len(items) != 1 || items[0].(Chars).Text != "one\ntwo" || items[0].(Chars).Format != (Format{}) {
		t.Fatalf("all the way back: %+v", items)
	}
	for st.HandleChord("Mod+Shift+Z") {
	}
	hasBreak, hasRule := false, false
	for _, it := range f.ContentItems() {
		switch it.(type) {
		case Break:
			hasBreak = true
		case Inline:
			hasRule = true
		}
	}
	if !hasBreak || !hasRule {
		t.Errorf("redone: break=%v rule=%v %+v", hasBreak, hasRule, f.ContentItems())
	}
}

func TestUndoAcrossMultiCursorEditsAndFeatures(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "foo foo\nbar"})
	f.SetCursors([]Cursor{At(1)})
	st.HandleChord("Mod+D")
	st.HandleChord("Mod+D")
	st.InsertText("X")
	if flowText(f) != "X X\nbar" {
		t.Fatalf("multi typing: %q", flowText(f))
	}
	f.SetCursors([]Cursor{At(5)})
	st.HandleChord("Alt+ArrowUp")
	st.HandleChord("Mod+Z")
	st.HandleChord("Mod+Z")
	if flowText(f) != "foo foo\nbar" {
		t.Errorf("undone: %q", flowText(f))
	}
}

func TestUndoInAFloatingText(t *testing.T) {
	st, ft := stageWith(t, "hello")
	ft.SetCursors([]Cursor{At(5)})
	for _, r := range " world" {
		ft.InsertText(string(r))
	}
	st.HandleKey("Backspace", false)
	st.HandleKey("Backspace", false)
	ft.SetCursors([]Cursor{{Anchor: 0, Caret: 5}})
	st.HandleChord("Mod+B")
	if ft.Text() != "hello wor" || ft.FormatAt(0).FontWeight != 700 {
		t.Fatalf("setup: %q", ft.Text())
	}
	st.HandleChord("Mod+Z")
	if ft.FormatAt(0).FontWeight == 700 {
		t.Error("undo the bold")
	}
	st.HandleChord("Mod+Z")
	if ft.Text() != "hello world" {
		t.Errorf("undo the deletes: %q", ft.Text())
	}
	st.HandleChord("Mod+Z")
	if ft.Text() != "hello" {
		t.Errorf("undo the typing: %q", ft.Text())
	}
	if st.HandleChord("Mod+Z") {
		t.Error("nothing before editing began")
	}
	st.HandleChord("Mod+Shift+Z")
	st.HandleChord("Mod+Shift+Z")
	st.HandleChord("Mod+Shift+Z")
	if ft.Text() != "hello wor" || ft.FormatAt(0).FontWeight != 700 {
		t.Errorf("redo all: %q", ft.Text())
	}
	ft.SetText("fresh")
	if st.HandleChord("Mod+Z") {
		t.Error("history is gone after SetText")
	}
}

func TestHistoryLimitCanBeLowered(t *testing.T) {
	defer SetHistoryLimit(DefaultHistoryLimit)
	if HistoryLimit() != DefaultHistoryLimit {
		t.Fatalf("limit %d", HistoryLimit())
	}
	SetHistoryLimit(1) // below the least: kept at the least
	if HistoryLimit() != MinHistoryLimit {
		t.Errorf("limit %d, want the least %d", HistoryLimit(), MinHistoryLimit)
	}
	SetHistoryLimit(20)
	_, st := editFlow(t, 0, Chars{Text: ""})
	for range 50 {
		st.InsertText("\n")
	}
	n := 0
	for st.HandleChord("Mod+Z") {
		n++
	}
	if n != 20 {
		t.Errorf("undid %d steps, want 20", n)
	}
}

func TestHistoryLimitsAndReset(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: ""})
	for range DefaultHistoryLimit + 20 {
		st.InsertText("\n") // each Enter is its own step (not a run)
	}
	n := 0
	for st.HandleChord("Mod+Z") {
		n++
	}
	if n != DefaultHistoryLimit {
		t.Errorf("history keeps %d steps, undid %d", DefaultHistoryLimit, n)
	}
	f.ResetContent()
	if f.Undo() || f.Redo() {
		t.Error("ResetContent forgets history")
	}
	bst, _ := stageWith(t, "x")
	if bst.HandleChord("Mod+Z") || bst.HandleChord("Mod+Y") {
		t.Error("nothing to undo or redo")
	}
	var h snapshotHistory[int]
	h.commit(1, "") // before start: ignored
	if len(h.undo) != 0 {
		t.Error("commit before start")
	}
	h.start(0)
	h.commit(0, "") // no change: ignored
	if len(h.undo) != 0 {
		t.Error("commit without a change")
	}
}
