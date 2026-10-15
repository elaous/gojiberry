package canvas

import (
	"reflect"
	"strings"
	"testing"
)

const (
	heb  = "שלום"  // 4 letters
	arab = "مرحبا" // 5 letters
)

func TestBidiLevels(t *testing.T) {
	cases := []struct {
		text string
		rtl  bool
		want []uint8 // nil: all level 0
	}{
		{"abc def", false, nil},
		{heb, true, []uint8{1, 1, 1, 1}},
		{"ab " + heb + " cd", false, []uint8{0, 0, 0, 1, 1, 1, 1, 0, 0, 0}}, // the spaces take the paragraph's direction
		{heb + " ab", true, []uint8{1, 1, 1, 1, 1, 2, 2}},                   // Latin text in a right-to-left paragraph is one level up
		{heb + " 123", true, []uint8{1, 1, 1, 1, 1, 2, 2, 2}},               // so is a number
		{arab + " 123", true, []uint8{1, 1, 1, 1, 1, 1, 2, 2, 2}},           // an Arabic number after Arabic letters
		{heb + ", 12", true, []uint8{1, 1, 1, 1, 1, 1, 2, 2}},               // the comma and the space between two right-to-left sides are right to left
		{"1,000 " + heb, true, []uint8{2, 2, 2, 2, 2, 1, 1, 1, 1, 1}},       // a separator between digits belongs to the number
		{"x" + heb + "." + "y", false, []uint8{0, 1, 1, 1, 1, 0, 0}},        // a full stop between the two directions takes the paragraph's
		{"(" + heb + ")", true, []uint8{1, 1, 1, 1, 1, 1}},                  // brackets round right-to-left text
		{"", false, nil},
		{"אְb", true, []uint8{1, 1, 2}}, // a combining mark goes with its letter
	}
	for _, c := range cases {
		got := bidiLevels([]rune(c.text), c.rtl)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("levels of %q (rtl %v) = %v, want %v", c.text, c.rtl, got, c.want)
		}
	}
	if !IsRTLText([]rune("123 "+heb)) || IsRTLText([]rune("abc "+heb)) || IsRTLText(nil) || !IsRTLText([]rune(arab)) {
		t.Error("the direction of a paragraph is that of its first strong letter")
	}
}

func TestVisualOrder(t *testing.T) {
	for _, c := range []struct {
		levels []uint8
		want   []int
	}{
		{[]uint8{0, 0, 0}, []int{0, 1, 2}},
		{[]uint8{0, 1, 1, 0}, []int{0, 2, 1, 3}},
		{[]uint8{1, 1}, []int{1, 0}},
		{[]uint8{1, 2, 1}, []int{2, 1, 0}},
		{[]uint8{0, 1, 2, 2, 1, 0}, []int{0, 4, 2, 3, 1, 5}},
	} {
		if got := visualOrder(c.levels); !reflect.DeepEqual(got, c.want) {
			t.Errorf("order of %v = %v, want %v", c.levels, got, c.want)
		}
	}
}

// rtlFlow is a right-to-left paragraph of a Hebrew word and a Latin word, in a column 400 wide at x 10, each
// letter 8 wide: the Latin word is to the left of the Hebrew one, and the line is at the right edge.
func rtlFlow(t *testing.T) (*TextFlow, Line) {
	t.Helper()
	f, _ := newFlow(heb+" abc", 400, 100)
	lay := f.Layout()
	if len(lay.Lines) != 1 {
		t.Fatalf("lines: %v", lineTexts(lay))
	}
	return f, lay.Lines[0]
}

func TestRightToLeftParagraphIsLaidOutFromTheRight(t *testing.T) {
	_, ln := rtlFlow(t)
	if !ln.RTL || !ln.Bidi {
		t.Fatalf("line RTL %v Bidi %v", ln.RTL, ln.Bidi)
	}
	// "שלום " is 5 runes = 40 wide, "abc" 24: the line is 64 wide and ends at the column's right edge (410).
	if ln.W != 64 || ln.X+ln.W != 410 {
		t.Errorf("line X %v W %v: it should end at 410", ln.X, ln.W)
	}
	if len(ln.Runs) != 2 || ln.Runs[0].Text != heb+" " || ln.Runs[1].Text != "abc" {
		t.Fatalf("runs %+v", ln.Runs)
	}
	// Visual order: the Latin word first (left), then the Hebrew.
	if ln.Runs[1].X != 0 || ln.Runs[0].X != 24 {
		t.Errorf("run positions: Hebrew at %v, Latin at %v", ln.Runs[0].X, ln.Runs[1].X)
	}
	if ln.Runs[0].Level != 1 || ln.Runs[1].Level != 2 {
		t.Errorf("levels %d %d", ln.Runs[0].Level, ln.Runs[1].Level)
	}
}

func TestLeftToRightParagraphIsUnchanged(t *testing.T) {
	f, _ := newFlow("hello world", 400, 100)
	ln := f.Layout().Lines[0]
	if ln.RTL || ln.Bidi || ln.X != 10 {
		t.Errorf("RTL %v Bidi %v X %v", ln.RTL, ln.Bidi, ln.X)
	}
	if x, _, _, _ := f.CaretRect(5); x != 10+40 {
		t.Errorf("caret at %v", x)
	}
}

func TestCaretAndClicksInBidirectionalText(t *testing.T) {
	f, ln := rtlFlow(t)
	left := ln.X // 346
	caretX := func(o int) float64 { x, _, _, _ := f.CaretRect(o); return x }
	// Start of the paragraph: the right edge. Inside the Hebrew word: moving left as the offset grows.
	if got := caretX(0); got != left+64 {
		t.Errorf("caret at 0 = %v, want %v", got, left+64)
	}
	if a, b := caretX(1), caretX(2); !(a > b) {
		t.Errorf("in right-to-left text the caret goes left as the offset grows: %v %v", a, b)
	}
	if got := caretX(5); got != left+24 {
		t.Errorf("caret between the words = %v, want %v", got, left+24)
	}
	// Inside the Latin word it goes right.
	if a, b := caretX(6), caretX(7); !(a < b) {
		t.Errorf("in the Latin word the caret goes right: %v %v", a, b)
	}
	if got := caretX(8); got != left+24 {
		t.Errorf("caret at the end = %v, want %v (the right edge of the Latin word)", got, left+24)
	}
	// Clicks find the same places.
	// (Offsets 5 and 8 are at one place, the two words meeting: a click there finds the first.)
	for _, o := range []int{0, 1, 2, 3, 4, 6, 7} {
		if got := f.OffsetAt(caretX(o), ln.Y+1); got != o {
			t.Errorf("a click at the caret of %d finds %d", o, got)
		}
	}
	if got := f.OffsetAt(left-50, ln.Y+1); got != 5 && got != 6 {
		t.Errorf("a click left of the line finds %d", got)
	}
}

func TestSelectionInBidirectionalTextCanBeTwoRanges(t *testing.T) {
	f, ln := rtlFlow(t)
	left := ln.X
	// The last Hebrew letter and the space (3..5), and the first Latin letter (5..6): the Hebrew word is at the
	// right of the line, its end at the left of it; the Latin word is at the left of the line.
	rects := f.lineRects(ln, 3, 6)
	if len(rects) != 2 {
		t.Fatalf("rects %v", rects)
	}
	if rects[0] != [2]float64{left + 24, left + 24 + 16} || rects[1] != [2]float64{left, left + 8} {
		t.Errorf("rects %v, want the Hebrew part %v and the Latin %v", rects, [2]float64{left + 24, left + 40}, [2]float64{left, left + 8})
	}
	// The whole of it from one side to the other is one range.
	if r := f.lineRects(ln, 0, 8); len(r) != 1 || r[0][0] != left || r[0][1] != left+64 {
		t.Errorf("whole line %v", r)
	}
	// Only Hebrew letters: one range, measured from the right.
	if r := f.lineRects(ln, 0, 2); len(r) != 1 || r[0][1] != left+64 || r[0][0] != left+64-16 {
		t.Errorf("first two letters %v", r)
	}
}

func TestArrowKeysMoveByWhatIsSeen(t *testing.T) {
	f, ln := rtlFlow(t)
	// In the Hebrew word, the left arrow goes to the next letter in the text.
	if got := f.visualStep(1, false); got != 2 {
		t.Errorf("left from 1 = %d, want 2", got)
	}
	if got := f.visualStep(2, true); got != 1 {
		t.Errorf("right from 2 = %d, want 1", got)
	}
	// In the Latin word it is the other way round.
	if got := f.visualStep(6, true); got != 7 {
		t.Errorf("right from 6 = %d, want 7", got)
	}
	// Left of the Latin word's last letter is before it; the left edge of the line has nothing further.
	if got := f.visualStep(8, false); got != 7 {
		t.Errorf("left from 8 = %d, want 7", got)
	}
	if got := f.visualStep(6, false); got != 6 {
		t.Errorf("left at the left edge of the only line stays: %d", got)
	}
	// Right from the right edge of a right-to-left line has nothing before it, either.
	if got := f.visualStep(0, true); got != 0 {
		t.Errorf("right at the right edge of the only line stays: %d", got)
	}
	_ = ln
	// A line of left-to-right text is as it was.
	g, _ := newFlow("abc", 400, 100)
	if g.visualStep(1, true) != 2 || g.visualStep(1, false) != 0 {
		t.Error("left-to-right arrows changed")
	}
}

func TestRightToLeftParagraphAlignmentAndMarker(t *testing.T) {
	f, _ := newFlow(heb, 400, 100)
	ln := f.Layout().Lines[0]
	if ln.X+ln.W != 410 {
		t.Errorf("the default alignment of a right-to-left paragraph is the right edge: ends at %v", ln.X+ln.W)
	}
	f.SetCursors([]Cursor{{Anchor: 1, Caret: 1}})
	f.ApplyParagraph(func(pf *ParaFormat) { pf.Align = AlignRight })
	if ln := f.Layout().Lines[0]; ln.X != 10 {
		t.Errorf("right means the end of the line, which is the left edge here: X %v", ln.X)
	}
}

// Right-to-left runs are drawn with the painter in that direction (so the spaces and punctuation at their ends
// go where the text has them), and set back after.
func TestRightToLeftRunsAreDrawnInTheirDirection(t *testing.T) {
	f, p := newFlow(heb+" abc", 400, 100)
	f.Draw()
	var seq []string
	for _, c := range p.calls {
		if len(c) > 9 && (c[:9] == "direction" || c[:8] == "fillText") {
			seq = append(seq, c)
		}
	}
	if len(seq) != 4 || seq[0] != "direction rtl" || !strings.HasPrefix(seq[1], "fillText \""+heb+" \" 370 ") || seq[2] != "direction ltr" || !strings.HasPrefix(seq[3], "fillText \"abc\" 346 ") {
		t.Errorf("drawing calls %q", seq)
	}
}
