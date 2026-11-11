package collab

import (
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
)

// Another person changing the paragraph being edited is a conflict.
func TestConflictIsReportedForAParagraphEditedHere(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "the brown fox\nsecond line\nthird line"})
	var got []Conflict
	a.OnConflict = func(c Conflict) { got = append(got, c) }

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

	got = nil
	click(b.Flow, 28)
	b.Flow.InsertText("!")
	exchange(a, b)
	if len(got) != 0 {
		t.Errorf("a change elsewhere was reported: %+v", got)
	}

	click(a.Flow, 24)
	a.Flow.InsertText("?")
	then := now
	defer func() { now = then }()
	now = func() time.Time { return then().Add(2 * DefaultConflictWindow) }
	click(b.Flow, 15)
	b.Flow.InsertText("X")
	exchange(a, b)
	if len(got) != 0 {
		t.Errorf("an old edit was reported: %+v", got)
	}
}

// Paragraphs with the same words are told apart.
func TestConflictTellsEqualParagraphsApart(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "same words\nsame words\nend"})
	var got []Conflict
	a.OnConflict = func(c Conflict) { got = append(got, c) }
	click(a.Flow, 10)
	a.Flow.InsertText("!")
	click(b.Flow, 21) // the end of the second "same words"
	b.Flow.InsertText("?")
	exchange(a, b)
	if len(got) != 0 {
		t.Fatalf("a change to the other paragraph was reported: %+v", got)
	}
	click(a.Flow, 3)
	a.Flow.InsertText("a")
	click(b.Flow, 4)
	b.Flow.InsertText("b")
	exchange(a, b)
	if len(got) != 1 || !strings.HasPrefix(got[0].Paragraph, "sam") {
		t.Fatalf("conflicts = %+v", got)
	}
}

// The window is the bridge's to set.
func TestConflictWindowIsConfigurable(t *testing.T) {
	a, b := pair(t, canvas.Chars{Text: "one line"})
	var got []Conflict
	a.OnConflict = func(c Conflict) { got = append(got, c) }
	a.ConflictWindow = time.Millisecond
	click(a.Flow, 3)
	a.Flow.InsertText("A")
	then := now
	defer func() { now = then }()
	now = func() time.Time { return then().Add(time.Second) }
	click(b.Flow, 5)
	b.Flow.InsertText("B")
	exchange(a, b)
	if len(got) != 0 {
		t.Errorf("an edit older than the window was reported: %+v", got)
	}
}

// A departed person's name stays known.
func TestConflictNamesSomeoneWhoLeft(t *testing.T) {
	s := &Session{names: map[string]string{"b": "Bo"}, Peers: nil}
	s.noteConflict(Conflict{Replicas: []string{"b", "z"}})
	if s.Conflicts != 1 || len(s.ConflictWith) != 2 || s.ConflictWith[0] != "Bo" || s.ConflictWith[1] != "" {
		t.Errorf("%d %v", s.Conflicts, s.ConflictWith)
	}
}
