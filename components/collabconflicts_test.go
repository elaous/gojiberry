package components

import (
	"testing"
	"time"
)

func TestCollabAnnouncerSaysMergedAndRejectedChanges(t *testing.T) {
	a := CollabAnnouncer{Every: time.Second}
	t0 := time.Unix(1000, 0)
	a.Update(t0, true, []string{"Ana"}, "")
	a.NoteMerged(1)
	if got := a.Update(t0.Add(2*time.Second), true, []string{"Ana"}, ""); got != "1 change was merged." {
		t.Fatalf("one: %q", got)
	}
	a.NoteMerged(2)
	a.NoteMerged(3)
	if !a.Pending() {
		t.Fatal("held merged changes are pending")
	}
	if got := a.Update(t0.Add(2*time.Second+time.Millisecond), true, []string{"Ana"}, ""); got != "" {
		t.Fatalf("spaced: %q", got)
	}
	if got := a.Update(t0.Add(5*time.Second), true, []string{"Ana"}, ""); got != "5 changes were merged." {
		t.Fatalf("grouped: %q", got)
	}
	a.NoteRejected(1)
	if got := a.Update(t0.Add(8*time.Second), true, []string{"Ana"}, ""); got != "A change could not be applied." {
		t.Fatalf("rejected: %q", got)
	}
	a.NoteRejected(2)
	a.NoteRejected(1)
	a.NoteMerged(2)
	if got := a.Update(t0.Add(11*time.Second), true, []string{"Ana"}, ""); got != "2 changes were merged. 3 changes could not be applied." {
		t.Fatalf("both: %q", got)
	}
	if a.Pending() {
		t.Fatal("all said")
	}
	// Said with the connection coming back, and at the connection-only level too (work was kept or lost,
	// whoever is there); not at all when announcements are off.
	b := CollabAnnouncer{Every: time.Second, Level: CollabImportant}
	b.Update(t0, true, nil, "")
	b.Update(t0.Add(2*time.Second), false, nil, "")
	b.NoteMerged(4)
	if got := b.Update(t0.Add(6*time.Second), true, nil, ""); got != "Connected again. 4 changes were merged." {
		t.Fatalf("important: %q", got)
	}
	c := CollabAnnouncer{Level: CollabOff}
	c.Update(t0, true, nil, "")
	c.NoteMerged(4)
	c.NoteRejected(1)
	if c.Pending() || c.Update(t0.Add(10*time.Second), true, nil, "") != "" {
		t.Fatal("nothing is said when off")
	}
}

func TestWhereSentenceSaysThePlaceOnlyWhenAsked(t *testing.T) {
	text := "First paragraph here.\nSecond one has a longer line of words in it.\n\nFourth"
	cases := []struct {
		anchor, caret int
		has           bool
		want          string
	}{
		{0, 0, true, "Bo is at the start of paragraph 1 of 4."},
		{5, 5, true, `Bo is in paragraph 1 of 4, after "First".`},
		{21, 21, true, `Bo is in paragraph 1 of 4, after "First paragraph here.".`},
		{22, 22, true, "Bo is at the start of paragraph 2 of 4."},
		{22, 50, true, "Bo has text selected in paragraph 2 of 4."},
		{66, 66, true, `Bo is in paragraph 2 of 4, after "…a longer line of words in it.".`},
		{67, 67, true, "Bo is at the start of paragraph 3 of 4."},
		{0, 0, false, "Bo has no cursor in the document."},
		{999, 999, true, `Bo is in paragraph 4 of 4, after "Fourth".`}, // an offset past the end is the end
	}
	for _, c := range cases {
		if got := WhereSentence("Bo", text, c.anchor, c.caret, c.has); got != c.want {
			t.Errorf("anchor %d caret %d: got %q want %q", c.anchor, c.caret, got, c.want)
		}
	}
	// An object in the text (U+FFFC) is not read out as a character.
	if got := WhereSentence("Bo", "see \uFFFC", 5, 5, true); got != `Bo is in paragraph 1 of 1, after "see".` {
		t.Errorf("object: %q", got)
	}
}

func TestPresenceWhereButtonsAreOptIn(t *testing.T) {
	peers := []PresencePeer{{Name: "Bo"}, {User: "cy@example.com"}}
	mustNotContain(t, render(t, Presence(PresenceProps{Self: PresencePeer{Name: "Ana"}, Peers: peers})), "gj-presence-where")
	var asked []PresencePeer
	n := Presence(PresenceProps{Self: PresencePeer{Name: "Ana"}, Peers: peers, OnWhere: func(p PresencePeer) { asked = append(asked, p) }})
	mustContain(t, render(t, n), `class="gj-presence-where"`, "Where is Bo?", "Where is cy@example.com?", `role="group"`)
	mustNotContain(t, render(t, Presence(PresenceProps{Self: PresencePeer{Name: "Ana"}, OnWhere: func(PresencePeer) {}})), "gj-presence-where") // nobody else, nothing to ask
	clickAll(n)
	if len(asked) != 2 || asked[0].Name != "Bo" || asked[1].User != "cy@example.com" {
		t.Fatalf("each button asks about its own person: %+v", asked)
	}
}

func TestCollabAnnouncerSaysWhoChangedTheParagraph(t *testing.T) {
	a := &CollabAnnouncer{}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.Update(now, true, []string{"Bo"}, "")
	a.NoteConflict([]string{"Bo"})
	got := a.Update(now.Add(3*time.Second), true, []string{"Bo"}, "")
	if got != "Bo changed the paragraph you were editing. Both changes were kept." {
		t.Errorf("one person: %q", got)
	}
	a.NoteConflict([]string{""})
	if got := a.Update(now.Add(6*time.Second), true, []string{"Bo"}, ""); got != "Someone changed the paragraph you were editing. Both changes were kept." {
		t.Errorf("not known: %q", got)
	}
	a.NoteConflict([]string{"Bo"})
	a.NoteConflict([]string{"Cy"})
	if got := a.Update(now.Add(9*time.Second), true, []string{"Bo"}, ""); got != "Others changed paragraphs you were editing, 2 times. All changes were kept." {
		t.Errorf("several: %q", got)
	}
	q := &CollabAnnouncer{Level: CollabOff}
	q.NoteConflict([]string{"Bo"})
	if q.Pending() {
		t.Error("nothing is said at level off")
	}
}
