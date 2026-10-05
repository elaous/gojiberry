package components

import (
	"context"
	"slices"

	"github.com/elaous/gojiberry/xango"
)

// Person is someone with access to a document. ID is how they are known (an
// address); Role is "owner", "editor" or "viewer".
type Person struct{ ID, Name, Role string }

// PeopleProps configures People.
type PeopleProps struct {
	People []Person
	// Roles are the roles that can be given, in the order offered ("editor",
	// "viewer" if empty).
	Roles []string
	// Invite gives someone a role; SetRole changes one; Remove takes access away.
	// A nil operation leaves its control out. The owner's row has none.
	Invite  func(ctx context.Context, id, role string) error
	SetRole func(ctx context.Context, id, role string) error
	Remove  DocAction
	// Draft is the address being typed to invite, and DraftRole the role chosen
	// for them (the first of Roles if empty); OnDraft reports changes to either.
	Draft, DraftRole string
	OnDraft          func(id, role string)
	// Self is the signed-in user, who cannot remove themselves here.
	Self    string
	Pending *Pending
}

// People lists who can open a document, each with a role to change and a Remove,
// and a form to invite someone. Like the other document components it knows
// nothing about where access is kept: a docstore ACL, an HTTP API, a test double.
func People(p PeopleProps) xango.Node {
	roles := p.Roles
	if len(roles) == 0 {
		roles = []string{"editor", "viewer"}
	}
	busy := p.Pending.Busy()
	roleSelect := func(class, label, current string, on func(string)) xango.Node {
		var opts []xango.Node
		offered := roles
		if current != "" && !slices.Contains(roles, current) {
			offered = append([]string{current}, roles...) // a role the list does not know is still shown
		}
		for _, r := range offered {
			attrs := []xango.Attr{xango.A("value", r)}
			if r == current {
				attrs = append(attrs, xango.A("selected", "selected"))
			}
			opts = append(opts, xango.El("option", attrs, xango.Text(tr("people.role."+r, r))))
		}
		attrs := []xango.Attr{xango.A("class", class), xango.A("aria-label", label)}
		if busy || on == nil {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		} else {
			attrs = append(attrs, xango.On("change", func(e xango.Event) { on(xango.EventValue[string](e)) }))
		}
		return xango.El("select", attrs, opts...)
	}
	head := xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")},
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("people.title", "People with access"))))
	var rows []xango.Node
	for _, who := range p.People {
		name := who.Name
		if name == "" {
			name = who.ID
		}
		row := []xango.Node{
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(name)),
		}
		if who.Role == "owner" {
			row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-people-owner")}, xango.Text(tr("people.role.owner", "owner"))))
		} else {
			id := who.ID
			var on func(string)
			if p.SetRole != nil {
				on = func(role string) { p.Pending.Run(func(ctx context.Context) error { return p.SetRole(ctx, id, role) }) }
			}
			row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, roleSelect("gj-people-role", trf("people.roleOf", "Role of %s", name), who.Role, on)))
			if p.Remove != nil && who.ID != p.Self {
				row = append(row, docButton("gj-people-remove", tr("people.remove", "Remove"), busy, actOne(p.Pending, p.Remove, id)))
			}
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-people-item"), xango.A("data-id", who.ID)}, row...))
	}
	children := []xango.Node{head, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...)}
	if p.Invite != nil {
		role := p.DraftRole
		if role == "" {
			role = roles[0]
		}
		input := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-people-invite-id"), xango.A("value", p.Draft),
			xango.A("placeholder", tr("people.invitePlaceholder", "name@example.com")), xango.A("aria-label", tr("people.invite", "Invite"))}
		if p.OnDraft != nil {
			input = append(input, xango.On("input", func(e xango.Event) { p.OnDraft(xango.EventValue[string](e), role) }))
		}
		pick := roleSelect("gj-people-invite-role", tr("people.inviteRole", "Role for the invited"), role, func(r string) {
			if p.OnDraft != nil {
				p.OnDraft(p.Draft, r)
			}
		})
		draft := p.Draft
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head gj-people-invite-form")},
			xango.El("input", input), pick,
			docButton("gj-people-invite", tr("people.invite", "Invite"), busy || draft == "", func() {
				p.Pending.Run(func(ctx context.Context) error { return p.Invite(ctx, draft, role) })
			})))
	}
	children = append(children, pendingError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-people")}, children...)
}
