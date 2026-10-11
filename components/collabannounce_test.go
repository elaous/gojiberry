package components

import (
	"testing"
	"time"
)

func TestCollabAnnouncerGroupsSpacesAndFilters(t *testing.T) {
	t0 := time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	var a CollabAnnouncer
	if got := a.Update(at(0), true, []string{"Ana"}, ""); got != "" {
		t.Fatalf("the first call only remembers: %q", got)
	}
	if got := a.Update(at(10), true, []string{"Ana", "Bo"}, ""); got != "Bo joined." {
		t.Errorf("one joins: %q", got)
	}
	// changes inside the spacing are held, then said together
	if got := a.Update(at(10), true, []string{"Ana", "Bo", "Cy"}, ""); got != "" {
		t.Errorf("held: %q", got)
	}
	if got := a.Update(at(11), true, []string{"Ana", "Bo", "Cy", "Di"}, ""); got != "" {
		t.Errorf("still held: %q", got)
	}
	if got := a.Update(at(13), true, []string{"Ana", "Bo", "Cy", "Di"}, ""); got != "2 people joined: Cy, Di." {
		t.Errorf("grouped: %q", got)
	}
	// someone who comes and goes before anything was said is not announced
	a.Update(at(14), true, []string{"Ana", "Bo", "Cy", "Di", "Ed"}, "")
	if got := a.Update(at(20), true, []string{"Ana", "Bo", "Cy", "Di"}, ""); got != "" {
		t.Errorf("joined and left: %q", got)
	}
	// leaving, and many names are counted
	if got := a.Update(at(30), true, []string{"Ana"}, ""); got != "3 people left: Bo, Cy, Di." {
		t.Errorf("leaving: %q", got)
	}
	// the connection
	if got := a.Update(at(40), false, []string{"Ana"}, ""); got != "The connection to the shared document was lost." {
		t.Errorf("offline: %q", got)
	}
	if got := a.Update(at(50), true, []string{"Ana"}, "the server refused an edit"); got != "Connected again. Sharing problem: the server refused an edit" {
		t.Errorf("online with a problem: %q", got)
	}
	if got := a.Update(at(60), true, []string{"Ana"}, ""); got != "Sharing works again." {
		t.Errorf("problem gone: %q", got)
	}
	if got := a.Update(at(70), true, []string{"Ana"}, ""); got != "" {
		t.Errorf("nothing changed: %q", got)
	}

	// important: the connection and problems, not the people
	b := CollabAnnouncer{Level: CollabImportant}
	b.Update(at(0), true, []string{"Ana"}, "")
	if got := b.Update(at(10), true, []string{"Ana", "Bo"}, ""); got != "" {
		t.Errorf("important says nothing of people: %q", got)
	}
	if got := b.Update(at(20), false, []string{"Ana", "Bo"}, ""); got == "" {
		t.Error("important says the connection is lost")
	}
	// off: nothing at all
	c := CollabAnnouncer{Level: CollabOff}
	c.Update(at(0), true, nil, "")
	if got := c.Update(at(10), false, []string{"Ana"}, "broken"); got != "" {
		t.Errorf("off: %q", got)
	}
}

func TestCollabAnnouncerSaysOthersComments(t *testing.T) {
	a := CollabAnnouncer{Every: time.Second}
	t0 := time.Unix(1000, 0)
	a.Update(t0, true, []string{"Ana"}, "")
	a.NoteComment("Bo")
	if got := a.Update(t0.Add(2*time.Second), true, []string{"Ana"}, ""); got != "Bo commented." {
		t.Fatalf("one: %q", got)
	}
	a.NoteComment("Bo")
	a.NoteComment("Cy")
	a.NoteComment("Bo")
	if !a.Pending() {
		t.Fatal("held comments are pending")
	}
	if got := a.Update(t0.Add(5*time.Second), true, []string{"Ana"}, ""); got != "3 new comments." {
		t.Fatalf("many: %q", got)
	}
	a.Level = CollabImportant
	a.NoteComment("Bo")
	if a.Pending() {
		t.Fatal("not said below the all level")
	}
}
