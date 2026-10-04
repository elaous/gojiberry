//go:build js && wasm

package main

import (
	"context"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
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
}

func newPeople(owner string, rerender func()) *peopleState {
	return &peopleState{acl: docstore.ACL{Owner: owner}, pending: components.NewPending(rerender), rerender: rerender}
}

func (p *peopleState) panel() xango.Node {
	return components.People(components.PeopleProps{
		People: p.list(), Self: p.acl.Owner, Pending: p.pending,
		Draft: p.draft, DraftRole: p.role,
		OnDraft: func(id, role string) { p.draft, p.role = id, role; p.rerender() },
		Invite: func(_ context.Context, id, role string) error {
			if err := p.acl.Grant(id, docstore.Role(role)); err != nil {
				return err
			}
			p.draft = ""
			return nil
		},
		SetRole: func(_ context.Context, id, role string) error { return p.acl.Grant(id, docstore.Role(role)) },
		Remove:  func(_ context.Context, id string) error { return p.acl.Revoke(id) },
	})
}

func (p *peopleState) list() []components.Person {
	var out []components.Person
	for _, who := range p.acl.People() {
		out = append(out, components.Person{ID: who.ID, Role: who.Role})
	}
	return out
}
