package main

import "testing"

func typeText(d *document, s string) {
	for _, r := range s {
		d.insert(string(r))
	}
}

func TestTypingIsOneUndoStepAndRedoComesBack(t *testing.T) {
	var d document
	typeText(&d, "hello")
	d.insert(" world") // a paste is a step of its own
	if d.String() != "hello world" {
		t.Fatal(d.String())
	}
	if !d.Undo() || d.String() != "hello" {
		t.Fatalf("undo takes back the paste: %q", d.String())
	}
	if !d.Undo() || d.String() != "" {
		t.Fatalf("and then the whole word, one step: %q", d.String())
	}
	if d.Undo() {
		t.Error("nothing left to undo")
	}
	if !d.Redo() || d.String() != "hello" || !d.Redo() || d.String() != "hello world" {
		t.Fatalf("redo: %q", d.String())
	}
	if d.Redo() {
		t.Error("nothing left to redo")
	}
	// A new edit forgets the redo steps.
	d.Undo()
	d.backspace()
	if d.Redo() {
		t.Error("an edit after Undo cannot be redone over")
	}
}

func TestEditsThatChangeNothingAreNotSteps(t *testing.T) {
	var d document
	typeText(&d, "ab")
	d.resetCursorTo(0)
	d.backspace() // at the start: nothing to delete
	d.moveLineUp()
	if len(d.hist.undo) != 1 {
		t.Fatalf("%d steps; only the typing counts", len(d.hist.undo))
	}
}

func TestUndoRestoresFormatsAndCursors(t *testing.T) {
	var d document
	typeText(&d, "bold me")
	d.selectAll()
	d.toggleSelectionFormatAllCursors(formatBold)
	if !d.fmtAt(0).Bold {
		t.Fatal("bold was not applied")
	}
	d.transformSelectionCaseAllCursors(true)
	if d.String() != "BOLD ME" {
		t.Fatal(d.String())
	}
	d.Undo()
	if d.String() != "bold me" || !d.fmtAt(0).Bold {
		t.Fatalf("undo of the case change: %q", d.String())
	}
	d.Undo()
	if d.fmtAt(0).Bold || d.Cursor != 7 || d.Anchor != 0 {
		t.Fatalf("undo of the format change brings back the selection too: cursor %d anchor %d", d.Cursor, d.Anchor)
	}
}

func TestSelectedTextOfSeveralCursors(t *testing.T) {
	var d document
	typeText(&d, "one two three")
	if d.selectedText() != "" {
		t.Error("nothing selected")
	}
	d.setCursors([]cursor{{Pos: 3, Anchor: 0}, {Pos: 13, Anchor: 8}})
	if got := d.selectedText(); got != "one\nthree" {
		t.Errorf("%q", got)
	}
}

func TestCutIsOneUndoStep(t *testing.T) {
	var d document
	typeText(&d, "keep cut")
	d.setCursors([]cursor{{Pos: 8, Anchor: 4}})
	text := d.selectedText()
	d.backspace() // what Cut does after taking the text
	if text != " cut" || d.String() != "keep" {
		t.Fatalf("%q %q", text, d.String())
	}
	d.Undo()
	if d.String() != "keep cut" {
		t.Errorf("%q", d.String())
	}
}
