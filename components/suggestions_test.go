package components

import (
	"strings"
	"testing"

	"github.com/elaous/xango"
)

func TestSuggestStateKeys(t *testing.T) {
	var s SuggestState
	if s.Key("ArrowDown") != SuggestNone {
		t.Fatal("a closed list leaves keys alone")
	}
	s.Show([]string{"plan", "planet", "plant"})
	s.Numbers = true
	steps := []struct {
		key  string
		act  SuggestAction
		want int
	}{{"ArrowDown", SuggestMoved, 1}, {"ArrowUp", SuggestMoved, 0}, {"ArrowUp", SuggestMoved, 2}, {"Home", SuggestMoved, 0}, {"End", SuggestMoved, 2}, {"2", SuggestAccept, 1}, {"9", SuggestNone, 1}, {"x", SuggestNone, 1}, {"Tab", SuggestAccept, 1}, {"Enter", SuggestAccept, 1}, {"Escape", SuggestDismiss, 1}}
	for _, st := range steps {
		if got := s.Key(st.key); got != st.act || s.Active != st.want {
			t.Fatalf("%s: action %v active %d, want %v %d", st.key, got, s.Active, st.act, st.want)
		}
	}
	if s.Selected() != "planet" {
		t.Fatalf("selected %q", s.Selected())
	}
	s.Close()
	if s.Open || s.Selected() != "" {
		t.Fatal("closed")
	}
	s.Show(nil)
	if s.Open {
		t.Fatal("no words: closed")
	}
}

func TestSuggestionsMarkup(t *testing.T) {
	s := SuggestState{Words: []string{"plan", "planet"}, Active: 1, Open: true, Numbers: true}
	html := render(t, Suggestions(SuggestionsProps{ID: "sg", State: s, X: 10, Y: 20}))
	for _, want := range []string{`role="listbox"`, `id="sg"`, `id="sg-opt-1"`, `aria-selected="true"`, `aria-setsize="2"`, `aria-posinset="2"`, `left:10px;top:20px`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in %s", want, html)
		}
	}
	if strings.Contains(render(t, Suggestions(SuggestionsProps{State: SuggestState{}})), "listbox") {
		t.Error("a closed list renders nothing")
	}
	attrs := render(t, xango.El("div", SuggestionsControl("sg", s)))
	for _, want := range []string{`role="combobox"`, `aria-expanded="true"`, `aria-activedescendant="sg-opt-1"`, `aria-controls="sg"`} {
		if !strings.Contains(attrs, want) {
			t.Errorf("control missing %s in %s", want, attrs)
		}
	}
	if a := s.Announcement(false); !strings.Contains(a, "2 suggestions") || !strings.Contains(a, "planet, 2 of 2") {
		t.Errorf("announcement %q", a)
	}
}
