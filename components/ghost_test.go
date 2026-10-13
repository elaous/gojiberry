package components

import "testing"

func TestGhostShowsOnlyWhatContinuesThePrefix(t *testing.T) {
	var g GhostState
	if !g.Show("ze", "zebra", 5) || g.Suffix != "bra" || !g.Visible() {
		t.Fatalf("ze -> %+v", g)
	}
	// A typed capital is not changed: only the rest is offered.
	if !g.Show("ZE", "zebra", 5) || g.Suffix != "bra" {
		t.Fatalf("ZE -> %+v", g)
	}
	for _, c := range [][2]string{{"", "zebra"}, {"zebra", "zebra"}, {"zx", "zebra"}, {"zebras", "zebra"}} {
		if g.Show(c[0], c[1], 3) || g.Visible() || g.Suffix != "" {
			t.Errorf("%v must show nothing", c)
		}
	}
	if !g.Show("Äp", "Äpfel", 2) || g.Suffix != "fel" || g.Width() != 3 {
		t.Errorf("non-ASCII: %+v", g)
	}
}

// Only Tab accepts; no other key inserts the suggestion.
func TestGhostIsAcceptedByTabAlone(t *testing.T) {
	keys := []string{"Enter", " ", "ArrowRight", "ArrowLeft", "End", "a", "Backspace", "ArrowDown", "Shift", "Delete", "."}
	for _, k := range keys {
		var g GhostState
		g.Show("ze", "zebra", 2)
		if g.Key(k) != GhostNone || !g.Visible() {
			t.Errorf("%q acted on the ghost text", k)
		}
	}
	var g GhostState
	g.Show("ze", "zebra", 2)
	if g.Key("Tab") != GhostAccept {
		t.Fatal("Tab accepts")
	}
	if g.Accept(3) != "" {
		t.Error("a moved caret must not receive the text")
	}
	if got := g.Accept(2); got != "bra" || g.Visible() {
		t.Errorf("accept gave %q", got)
	}
	if g.Accept(2) != "" || g.Key("Tab") != GhostNone {
		t.Error("nothing left to accept")
	}
}

func TestGhostEscapeDismissesUntilTheWordChanges(t *testing.T) {
	var g GhostState
	g.Show("ze", "zebra", 2)
	if g.Key("Escape") != GhostDismiss || g.Visible() {
		t.Fatal("Escape dismisses")
	}
	if g.Show("ze", "zebra", 2) {
		t.Error("the same prefix is not offered again")
	}
	g.Typed("zeb")
	if !g.Show("zeb", "zebra", 3) {
		t.Error("a new letter brings it back")
	}
	if g.Key("Escape") == GhostNone {
		t.Error("dismissed again")
	}
	if g.Key("Escape") != GhostNone {
		t.Error("Escape with nothing shown is not for the ghost text")
	}
}

func TestGhostIsAnnouncedOncePerWord(t *testing.T) {
	var g GhostState
	if g.Announcement() != "" {
		t.Error("nothing shown, nothing said")
	}
	g.Show("ze", "zebra", 2)
	first := g.Announcement()
	if first == "" || first != "Suggestion: zebra. Press Tab to accept." {
		t.Errorf("announcement: %q", first)
	}
	g.Show("zeb", "zebra", 3)
	if g.Announcement() != "" {
		t.Error("the same word is not announced again for the next letter")
	}
	g.Show("zep", "zeppelin", 3)
	if g.Announcement() == "" {
		t.Error("another word is announced")
	}
	g.Typed("")
	g.Show("ze", "zebra", 2)
	if g.Announcement() == "" {
		t.Error("a new word starts afresh")
	}
}
