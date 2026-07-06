package main

import "testing"

// newTestDoc returns an empty document with the given text inserted
// via a single primary cursor at the start -- a plain helper, not
// exercising multi-cursor itself.
func newTestDoc(text string) *document {
	d := &document{}
	d.insert(text)
	d.Cursor, d.Anchor = 0, 0
	return d
}

func TestSingleCursorInsertStillWorks(t *testing.T) {
	d := newTestDoc("hello")
	d.Cursor, d.Anchor = 5, 5
	d.insert(" world")
	if got := d.String(); got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
	if d.Cursor != 11 || d.Anchor != 11 {
		t.Fatalf("cursor = (%d, %d), want (11, 11)", d.Cursor, d.Anchor)
	}
}

func TestMultiCursorInsertSameText(t *testing.T) {
	// "a-b-c", cursors after each letter (1, 3, 5): typing "X" at all
	// three should give "aX-bX-cX", each cursor landing right after its
	// own inserted X.
	d := newTestDoc("a-b-c")
	d.Cursor, d.Anchor = 1, 1
	d.ExtraCursors = []cursor{{Pos: 3, Anchor: 3}, {Pos: 5, Anchor: 5}}

	d.insert("X")

	if got, want := d.String(), "aX-bX-cX"; got != want {
		t.Fatalf("text: got %q, want %q", got, want)
	}
	wantCursors := []cursor{{2, 2}, {5, 5}, {8, 8}}
	assertCursors(t, d, wantCursors)
}

func TestMultiCursorBackspace(t *testing.T) {
	// "aXbXcX", cursors right after each X: backspacing all three at
	// once should remove each X, leaving "abc".
	d := newTestDoc("aXbXcX")
	d.Cursor, d.Anchor = 2, 2
	d.ExtraCursors = []cursor{{Pos: 4, Anchor: 4}, {Pos: 6, Anchor: 6}}

	d.backspace()

	if got, want := d.String(), "abc"; got != want {
		t.Fatalf("text: got %q, want %q", got, want)
	}
	assertCursors(t, d, []cursor{{1, 1}, {2, 2}, {3, 3}})
}

func TestMultiCursorDeleteForward(t *testing.T) {
	// "aXbXcX", cursors right *before* each X: forward-deleting all
	// three should remove each X, same result as the backspace case
	// but approached from the other side.
	d := newTestDoc("aXbXcX")
	d.Cursor, d.Anchor = 1, 1
	d.ExtraCursors = []cursor{{Pos: 3, Anchor: 3}, {Pos: 5, Anchor: 5}}

	d.delete()

	if got, want := d.String(), "abc"; got != want {
		t.Fatalf("text: got %q, want %q", got, want)
	}
	assertCursors(t, d, []cursor{{1, 1}, {2, 2}, {3, 3}})
}

// TestMultiCursorReplaceDifferentLengthSelections is the key
// rebasing-correctness test: two cursors each have their own
// *differently sized* selection (3 chars vs. 2 chars), and both are
// replaced with the same 1-char text in one batch. Processing in
// descending position order means cursor B's edit (entirely after A)
// never has to rebase A's already-known position -- if that
// assumption were wrong, A's final position would be off.
func TestMultiCursorReplaceDifferentLengthSelections(t *testing.T) {
	// "00abc00de00": cursor A selects "abc" ([2,5)), cursor B selects
	// "de" ([7,9)) -- deliberately different-sized selections (3 vs. 2
	// chars). Replacing both with "X" in one batch: descending-order
	// processing does B first ([7,9) -> "X"), which doesn't disturb A's
	// still-untouched [2,5); only then does A's own replacement happen.
	// If that ordering assumption were wrong, A's resulting position
	// would be off.
	d := newTestDoc("00abc00de00")
	d.Cursor, d.Anchor = 2, 5
	d.ExtraCursors = []cursor{{Pos: 7, Anchor: 9}}

	d.insert("X")

	if got, want := d.String(), "00X00X00"; got != want {
		t.Fatalf("text: got %q, want %q", got, want)
	}
	// "00abc00de00" -> replace [7,9) "de" with "X" -> "00abc00X00"
	//               -> replace [2,5) "abc" with "X" -> "00X00X00"
	assertCursors(t, d, []cursor{{3, 3}, {6, 6}})
}

func TestAddCursorAtExistingPositionMerges(t *testing.T) {
	// setCursors (called by every cursor-mutating operation, including
	// addCursorAt) dedupes by exact Pos, so two cursors can never
	// actually reach forEachCursorDescending already stacked at the
	// same spot -- confirm the merge happens at the earliest possible
	// point (cursor creation), not deferred to the next edit.
	d := newTestDoc("ab")
	d.resetCursorTo(1)
	d.addCursorAt(1) // same position as the existing (only) cursor
	assertCursors(t, d, []cursor{{1, 1}})

	d.insert("X")
	if got, want := d.String(), "aXb"; got != want {
		t.Fatalf("text: got %q, want %q", got, want)
	}
	assertCursors(t, d, []cursor{{2, 2}})
}

func TestAddCursorAndCollapse(t *testing.T) {
	d := newTestDoc("hello world")
	d.resetCursorTo(5)
	assertCursors(t, d, []cursor{{5, 5}})

	d.addCursorAt(0)
	d.addCursorAt(11)
	assertCursors(t, d, []cursor{{0, 0}, {5, 5}, {11, 11}})

	// The primary is always the leftmost live cursor (see setCursors),
	// so adding one at 0 -- left of the original 5 -- made *that* the
	// new primary; collapsing keeps it, not "wherever the cursor
	// started out", since no per-cursor identity survives a sort.
	d.collapseCursors()
	assertCursors(t, d, []cursor{{0, 0}})
}

func TestSelectAllDropsExtraCursors(t *testing.T) {
	d := newTestDoc("hello")
	d.addCursorAt(2)
	d.selectAll()
	// Anchor at the start, Pos (the blinking caret) at the end --
	// matching where a real "select all" leaves the caret.
	assertCursors(t, d, []cursor{{5, 0}})
}

func TestMoveCursorsIndependently(t *testing.T) {
	d := newTestDoc("0123456789")
	d.Cursor, d.Anchor = 3, 3
	d.ExtraCursors = []cursor{{Pos: 7, Anchor: 7}}

	d.moveCursors(func(pos int) int { return pos + 1 }, false)
	assertCursors(t, d, []cursor{{4, 4}, {8, 8}})

	// Shift-extend: Anchor should stay put, only Pos moves.
	d.moveCursors(func(pos int) int { return pos + 1 }, true)
	assertCursors(t, d, []cursor{{5, 4}, {9, 8}})
}

func TestMoveCursorsClampsToDocumentBounds(t *testing.T) {
	d := newTestDoc("abc")
	d.Cursor, d.Anchor = 0, 0
	d.ExtraCursors = []cursor{{Pos: 3, Anchor: 3}}

	d.moveCursors(func(pos int) int { return pos - 10 }, false)
	assertCursors(t, d, []cursor{{0, 0}}) // both clamp to 0 and merge

	d.ExtraCursors = []cursor{{Pos: 0, Anchor: 0}}
	d.Cursor, d.Anchor = 3, 3
	d.moveCursors(func(pos int) int { return pos + 10 }, false)
	assertCursors(t, d, []cursor{{3, 3}}) // both clamp to Len()=3 and merge
}

func TestToggleFormatAllCursors(t *testing.T) {
	d := newTestDoc("aaXbbXcc")
	d.Cursor, d.Anchor = 0, 2                      // selects "aa"
	d.ExtraCursors = []cursor{{Pos: 6, Anchor: 8}} // selects "cc"

	d.toggleSelectionFormatAllCursors(formatBold)

	for i := 0; i < 2; i++ {
		if !d.fmtAt(i).Bold {
			t.Errorf("index %d: expected Bold after toggling first selection", i)
		}
	}
	for i := 3; i < 5; i++ {
		if d.fmtAt(i).Bold {
			t.Errorf("index %d: unselected middle text should stay unbolded", i)
		}
	}
	for i := 6; i < 8; i++ {
		if !d.fmtAt(i).Bold {
			t.Errorf("index %d: expected Bold after toggling second selection", i)
		}
	}
}

func TestTransformCaseAllCursors(t *testing.T) {
	d := newTestDoc("aaXbbXcc")
	d.Cursor, d.Anchor = 0, 2
	d.ExtraCursors = []cursor{{Pos: 6, Anchor: 8}}

	d.transformSelectionCaseAllCursors(true)

	if got, want := d.String(), "AAXbbXCC"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// assertCursors checks d's live cursors (primary + ExtraCursors,
// sorted ascending -- setCursors' own invariant) exactly match want.
func assertCursors(t *testing.T, d *document, want []cursor) {
	t.Helper()
	got := d.cursors()
	if len(got) != len(want) {
		t.Fatalf("cursor count = %d, want %d (got %+v, want %+v)", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cursor %d = %+v, want %+v (full: got %+v, want %+v)", i, got[i], want[i], got, want)
		}
	}
}
