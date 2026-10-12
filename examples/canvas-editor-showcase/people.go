//go:build js && wasm

package main

import (
	"context"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/user"
	"github.com/elaous/gojiberry/xango"
)

// peopleState is who can open the document, held in memory here: a real app keeps
// the ACL where its documents are (docstore's /docs/{id}/acl) and plugs those
// calls into the same props.
type peopleState struct {
	acl         docstore.ACL
	draft, role string
	pending     *components.Pending
	rerender    func()
	links       *linksUI // share links (links.go)
}

func newPeople(owner string, rerender func()) *peopleState {
	p := &peopleState{acl: docstore.ACL{Owner: owner}, pending: components.NewPending(rerender), rerender: rerender}
	p.links = newLinks(p)
	return p
}

func (p *peopleState) panel(confirm func(string, func())) xango.Node {
	return xango.Frag(p.peoplePanel(confirm), p.links.panel())
}

func (p *peopleState) peoplePanel(confirm func(string, func())) xango.Node {
	return components.People(components.PeopleProps{
		Confirm: confirm,
		People:  p.list(), Self: p.acl.Owner, Pending: p.pending,
		Draft: p.draft, DraftRole: p.role,
		OnDraft: func(id, role string) { p.draft, p.role = id, role; p.rerender() },
		Invite: func(_ context.Context, id, role string) error {
			if err := p.acl.GrantFor(id, docstore.Role(role), p.links.now(), p.links.personDuration()); err != nil {
				return err
			}
			p.draft = ""
			return nil
		},
		SetRole: func(_ context.Context, id, role string) error { return p.acl.Grant(id, docstore.Role(role)) },
		Remove:  func(_ context.Context, id string) error { return p.acl.Revoke(id) },
	})
}

func (p *peopleState) list() []user.Person { return p.acl.Active(p.links.now()).People() }
