package collab

import (
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
)

// Another person changing the paragraph that is being edited here is reported; a change in another
// paragraph, or to text nobody here touched lately, is not.
func TestConflictIsReportedForAParagraphEditedHere(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "the brown fox\nsecond line\nthird line"})
	var got []Conflict
	a.OnConflict = func(c Conflict) { got = append(got, c) }

	// Ana types in the first paragraph; Bo, at the same time, in the same one.
	click(a.Flow, 13)
	a.Flow.InsertText(" ANA")
	click(b.Flow, 3)
	b.Flow.InsertText(" BO")
	exchange(a, b)
	if len(got) != 1 {
		t.Fatalf("conflicts = %d, want 1: %+v", len(got), got)
	}
	if got[0].Replicas[0] != "b" || got[0].Paragraph != "the brown fox ANA" {
		t.Errorf("conflict = %+v", got[0])
	}
	if text(a.Flow) != text(b.Flow) {
		t.Errorf("copies differ: %q vs %q", text(a.Flow), text(b.Flow))
	}

	// Bo now changes the third paragraph; Ana's recent paragraph is the first: nothing to report.
	got = nil
	click(b.Flow, 28)
	b.Flow.InsertText("!")
	exchange(a, b)
	if len(got) != 0 {
		t.Errorf("a change elsewhere was reported: %+v", got)
	}

	// Ana edits the second paragraph, and Bo's change arrives after the window: not a conflict.
	click(a.Flow, 24)
	a.Flow.InsertText("?")
	then := now
	defer func() { now = then }()
	now = func() time.Time { return then().Add(2 * conflictWindow) }
	click(b.Flow, 15)
	b.Flow.InsertText("X")
	exchange(a, b)
	if len(got) != 0 {
		t.Errorf("an old edit was reported: %+v", got)
	}
}
