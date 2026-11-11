package collab

import (
	"math/rand"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

func ctrlZ(f *canvas.TextFlow) bool { return f.Undo() }

func TestUndoOnlyUndoesMyChanges(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "hello world"})
	click(a.Flow, 0)
	a.Flow.InsertText("X")
	exchange(a, b)
	click(b.Flow, len([]rune(b.Flow.EditText())))
	b.Flow.InsertText("Y")
	exchange(a, b)
	if text(a.Flow) != "Xhello worldY" {
		t.Fatalf("%q", text(a.Flow))
	}
	if !a.CanUndo() || a.CanRedo() {
		t.Fatal("can undo, not redo")
	}
	if !ctrlZ(a.Flow) {
		t.Fatal("undo")
	}
	exchange(a, b)
	if text(a.Flow) != "hello worldY" || text(b.Flow) != "hello worldY" {
		t.Fatalf("B's Y stays, A's X goes: %q %q", text(a.Flow), text(b.Flow))
	}
	if !a.Flow.Redo() {
		t.Fatal("redo")
	}
	exchange(a, b)
	if text(a.Flow) != "Xhello worldY" || normalized(a.Flow) != normalized(b.Flow) {
		t.Fatalf("%q %q", text(a.Flow), text(b.Flow))
	}
	if a.Flow.Redo() {
		t.Fatal("nothing more to redo")
	}
	if !b.CanUndo() {
		t.Fatal("B still has its own step")
	}
}

func TestTypingAndBackspaceRunsAreOneStep(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "hello world"})
	click(a.Flow, 11)
	for _, r := range " abc" {
		a.Flow.InsertText(string(r))
	}
	if len(a.undo) != 1 {
		t.Fatalf("a run of typing is one step: %d", len(a.undo))
	}
	ctrlZ(a.Flow)
	if text(a.Flow) != "hello world" {
		t.Fatalf("%q", text(a.Flow))
	}
	click(a.Flow, 11)
	for i := 0; i < 5; i++ {
		a.Flow.HandleKey("Backspace", false)
	}
	if text(a.Flow) != "hello " || len(a.undo) != 1 {
		t.Fatalf("%q %d", text(a.Flow), len(a.undo))
	}
	exchange(a, b)
	click(b.Flow, 0)
	b.Flow.InsertText("!!")
	exchange(a, b)
	ctrlZ(a.Flow)
	exchange(a, b)
	if text(a.Flow) != "!!hello world" || text(b.Flow) != "!!hello world" {
		t.Fatalf("the deleted word is back in place: %q %q", text(a.Flow), text(b.Flow))
	}
	// The Delete key runs forward.
	click(a.Flow, 2)
	for i := 0; i < 5; i++ {
		a.Flow.HandleKey("Delete", false)
	}
	if text(a.Flow) != "!! world" {
		t.Fatalf("%q", text(a.Flow))
	}
	n := len(a.undo)
	ctrlZ(a.Flow)
	if text(a.Flow) != "!!hello world" || len(a.undo) != n-1 {
		t.Fatalf("one step: %q", text(a.Flow))
	}
}

func TestUndoFormattingKeepsOthersChanges(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "hello world"})
	a.Flow.SetCursors([]canvas.Cursor{{Anchor: 0, Caret: 5}})
	a.Flow.ApplyFormat(func(f *canvas.Format) { f.FontWeight = 700 })
	exchange(a, b)
	click(b.Flow, 3)
	b.Flow.InsertText("Z") // typed inside the bold word
	exchange(a, b)
	ctrlZ(a.Flow)
	exchange(a, b)
	if normalized(a.Flow) != normalized(b.Flow) {
		t.Fatal("differ")
	}
	bold := ""
	for _, it := range a.Flow.ContentItems() {
		if ch, ok := it.(canvas.Chars); ok && ch.Format.FontWeight == 700 {
			bold += ch.Text
		}
	}
	if bold != "Z" {
		t.Fatalf("A's bold is gone, B's letter keeps the bold it was typed into: %q", bold)
	}
	a.Flow.Redo()
	exchange(a, b)
	if normalized(a.Flow) != normalized(b.Flow) {
		t.Fatal("differ after redo")
	}
}

func TestUndoParagraphFormatsAndEdgeCases(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "para one\npara two"})
	click(a.Flow, 2)
	a.Flow.ApplyParagraph(func(p *canvas.ParaFormat) { p.Align = canvas.AlignCenter })
	exchange(a, b)
	if !hasAlign(b.Flow, canvas.AlignCenter) {
		t.Fatal("alignment shared")
	}
	ctrlZ(a.Flow)
	exchange(a, b)
	if hasAlign(a.Flow, canvas.AlignCenter) || hasAlign(b.Flow, canvas.AlignCenter) {
		t.Fatal("alignment undone")
	}
	click(a.Flow, 0)
	a.Flow.InsertText("Q")
	exchange(a, b)
	b.Flow.SetCursors([]canvas.Cursor{{Anchor: 0, Caret: 1}})
	b.Flow.HandleKey("Backspace", false) // B removes A's Q
	exchange(a, b)
	if text(a.Flow) != "para one\npara two" {
		t.Fatalf("%q", text(a.Flow))
	}
	if ctrlZ(a.Flow) { // no step left that still has something to undo
		t.Fatalf("undid something that was already gone: %q", text(a.Flow))
	}
	// Not ready: no undo. New edits clear redo.
	idle, _ := Join("z", newFlow(canvas.Chars{Text: "x"}))
	if idle.Flow.Undo() {
		t.Fatal("a bridge that is not sharing yet cannot undo")
	}
	click(a.Flow, 0)
	a.Flow.InsertText("1")
	ctrlZ(a.Flow)
	if !a.CanRedo() {
		t.Fatal("redo available")
	}
	a.Flow.InsertText("2")
	if a.CanRedo() {
		t.Fatal("a new edit clears redo")
	}
	for i := 0; i < maxUndo+10; i++ { // the stack is bounded
		click(a.Flow, 0)
		a.Flow.SetCursors([]canvas.Cursor{canvas.At(i % 3)})
		a.Flow.InsertText("z")
		a.Flow.HandleKey("ArrowLeft", false)
		a.Flow.SetCursors([]canvas.Cursor{canvas.At((i * 7) % 5)})
	}
	if len(a.undo) > maxUndo {
		t.Fatalf("%d steps", len(a.undo))
	}
}

func hasAlign(f *canvas.TextFlow, al canvas.Align) bool {
	for _, it := range f.ContentItems() {
		if p, ok := it.(canvas.ParaFormat); ok && p.Align == al {
			return true
		}
	}
	return false
}

// Random edits and undos by one person while another edits.
func TestRandomUndoConverges(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		a, b := pair(t, canvas.Chars{Text: "the quick brown fox jumps over the lazy dog"})
		bridges := []*Bridge{a, b}
		for step := 0; step < 50; step++ {
			i := rng.Intn(2)
			br := bridges[i]
			f := br.Flow
			n := len([]rune(f.EditText()))
			switch rng.Intn(8) {
			case 0, 1:
				f.SetCursors([]canvas.Cursor{canvas.At(rng.Intn(n + 1))})
				f.InsertText(string(rune('a' + rng.Intn(26))))
			case 2:
				if n > 0 {
					f.SetCursors([]canvas.Cursor{canvas.At(1 + rng.Intn(n))})
					f.HandleKey("Backspace", false)
				}
			case 3:
				if n > 1 {
					from := rng.Intn(n - 1)
					f.SetCursors([]canvas.Cursor{{Anchor: from, Caret: from + 1 + rng.Intn(n-from-1)}})
					f.ApplyFormat(func(fm *canvas.Format) { fm.FontWeight = 700 - fm.FontWeight })
				}
			case 4:
				br.UndoStep(false)
			case 5:
				br.UndoStep(true)
			case 6:
				f.SetCursors([]canvas.Cursor{canvas.At(rng.Intn(n + 1))})
				f.InsertText("\n")
			default:
				exchange(a, b)
			}
			if br.Err() != nil {
				t.Fatalf("trial %d: %v", trial, br.Err())
			}
		}
		exchange(a, b)
		exchange(a, b)
		if normalized(a.Flow) != normalized(b.Flow) {
			t.Fatalf("trial %d diverged:\n a %s\n b %s", trial, normalized(a.Flow), normalized(b.Flow))
		}
		for k, br := range bridges {
			want, _ := atomsFromItems(br.Flow.ContentItems())
			have := br.read()
			if len(want) != len(have) {
				t.Fatalf("trial %d bridge %d: %d vs %d atoms", trial, k, len(want), len(have))
			}
			for m := range want {
				if want[m].r != have[m].r || !sameAttrs(want[m].attrs, have[m].attrs) {
					t.Fatalf("trial %d bridge %d: atom %d differs", trial, k, m)
				}
			}
		}
	}
}
