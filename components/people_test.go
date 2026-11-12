package components

import (
	"context"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/user"
	"github.com/elaous/xango"
)

func TestPeopleListsAndPlugsIn(t *testing.T) {
	var log []string
	pending := &Pending{Inline: true}
	p := PeopleProps{
		People:  []user.Person{{ID: "me@gojiberry.io", Role: "owner"}, {ID: "ana@gojiberry.io", Role: "editor"}, {ID: "bo@gojiberry.io", Name: "Bo", Role: "viewer"}},
		Pending: pending, Self: "me@gojiberry.io", Draft: "new@gojiberry.io", DraftRole: "viewer",
		Invite:  func(_ context.Context, id, role string) error { log = append(log, "invite:"+id+":"+role); return nil },
		SetRole: func(_ context.Context, id, role string) error { log = append(log, "role:"+id+":"+role); return nil },
		Remove:  func(_ context.Context, id string) error { log = append(log, "remove:"+id); return nil },
		OnDraft: func(id, role string) { log = append(log, "draft:"+id+":"+role) },
	}
	n := People(p)
	html := render(t, n)
	mustContain(t, html, `data-id="me@gojiberry.io"`, "gj-people-owner", `data-id="ana@gojiberry.io"`, ">Bo<", "gj-people-invite", `value="new@gojiberry.io"`)
	if strings.Count(html, "gj-people-remove") != 2 { // the owner's row has none
		t.Errorf("remove buttons: %s", html)
	}
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "viewer"})
	}
	findHandlers(n, "input")[0](xango.Event{Value: "typed@gojiberry.io"})
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	got := strings.Join(log, " ")
	for _, want := range []string{"role:ana@gojiberry.io:viewer", "role:bo@gojiberry.io:viewer", "draft:typed@gojiberry.io:viewer", "remove:ana@gojiberry.io", "remove:bo@gojiberry.io", "invite:new@gojiberry.io:viewer"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	self := render(t, People(PeopleProps{People: []user.Person{{ID: "me@gojiberry.io", Role: "owner"}, {ID: "me2@gojiberry.io", Role: "editor"}}, Self: "me2@gojiberry.io", Remove: p.Remove}))
	mustNotContain(t, self, "gj-people-remove", "gj-people-invite")
	bare := render(t, People(PeopleProps{People: p.People}))
	mustNotContain(t, bare, "gj-people-remove", "gj-people-invite")
	mustContain(t, bare, `disabled="disabled"`) // with no SetRole the roles are shown, not editable
}

func TestPeopleShowsARoleTheListDoesNotKnow(t *testing.T) {
	html := render(t, People(PeopleProps{People: []user.Person{{ID: "a@gojiberry.io", Role: "commenter"}}, SetRole: func(context.Context, string, string) error { return nil }}))
	mustContain(t, html, `<option value="commenter" selected="selected">`)
}
