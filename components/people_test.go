package components

import (
	"context"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/user"
	"github.com/elaous/gojiberry/xango"
)

func TestPeopleListsAndPlugsIn(t *testing.T) {
	var log []string
	pending := &Pending{Inline: true}
	p := PeopleProps{
		People:  []user.Person{{ID: "me@x.test", Role: "owner"}, {ID: "ana@x.test", Role: "editor"}, {ID: "bo@x.test", Name: "Bo", Role: "viewer"}},
		Pending: pending, Self: "me@x.test", Draft: "new@x.test", DraftRole: "viewer",
		Invite:  func(_ context.Context, id, role string) error { log = append(log, "invite:"+id+":"+role); return nil },
		SetRole: func(_ context.Context, id, role string) error { log = append(log, "role:"+id+":"+role); return nil },
		Remove:  func(_ context.Context, id string) error { log = append(log, "remove:"+id); return nil },
		OnDraft: func(id, role string) { log = append(log, "draft:"+id+":"+role) },
	}
	n := People(p)
	html := render(t, n)
	mustContain(t, html, `data-id="me@x.test"`, "gj-people-owner", `data-id="ana@x.test"`, ">Bo<", "gj-people-invite", `value="new@x.test"`)
	if strings.Count(html, "gj-people-remove") != 2 { // the owner's row has none
		t.Errorf("remove buttons: %s", html)
	}
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "viewer"})
	}
	findHandlers(n, "input")[0](xango.Event{Value: "typed@x.test"})
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	got := strings.Join(log, " ")
	for _, want := range []string{"role:ana@x.test:viewer", "role:bo@x.test:viewer", "draft:typed@x.test:viewer", "remove:ana@x.test", "remove:bo@x.test", "invite:new@x.test:viewer"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	// You cannot remove yourself; nothing offered means nothing drawn.
	self := render(t, People(PeopleProps{People: []user.Person{{ID: "me@x.test", Role: "owner"}, {ID: "me2@x.test", Role: "editor"}}, Self: "me2@x.test", Remove: p.Remove}))
	mustNotContain(t, self, "gj-people-remove", "gj-people-invite")
	bare := render(t, People(PeopleProps{People: p.People}))
	mustNotContain(t, bare, "gj-people-remove", "gj-people-invite")
	mustContain(t, bare, `disabled="disabled"`) // with no SetRole the roles are shown, not editable
}

func TestPeopleShowsARoleTheListDoesNotKnow(t *testing.T) {
	html := render(t, People(PeopleProps{People: []user.Person{{ID: "a@x.test", Role: "commenter"}}, SetRole: func(context.Context, string, string) error { return nil }}))
	mustContain(t, html, `<option value="commenter" selected="selected">`)
}
