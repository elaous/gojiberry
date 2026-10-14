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

	// remote, when set, gives the server's adapter and the id of the document open there (ok false: the
	// document is not on a server). Then the list below is the server's: read from it, and every change is sent.
	remote    func() (a *docstore.HTTPAdapter, id string, ok bool)
	syncedFor string // the document whose list was asked for
	note      string // how the server answered, in words
}

func newPeople(owner string, rerender func()) *peopleState {
	p := &peopleState{acl: docstore.ACL{Owner: owner}, pending: components.NewPending(rerender), rerender: rerender}
	p.links = newLinks(p)
	return p
}

func (p *peopleState) panel(confirm func(string, func())) xango.Node {
	p.sync()
	return xango.Frag(p.serverNote(), p.peoplePanel(confirm), p.links.panel())
}

func (p *peopleState) peoplePanel(confirm func(string, func())) xango.Node {
	return components.People(components.PeopleProps{
		Confirm: confirm,
		People:  p.list(), Self: p.acl.Owner, Pending: p.pending,
		Draft: p.draft, DraftRole: p.role,
		OnDraft: func(id, role string) { p.draft, p.role = id, role; p.rerender() },
		Invite: func(ctx context.Context, id, role string) error {
			if err := p.acl.GrantFor(id, docstore.Role(role), p.links.now(), p.links.personDuration()); err != nil {
				return err
			}
			p.draft = ""
			return p.send(ctx)
		},
		SetRole: func(ctx context.Context, id, role string) error {
			if err := p.acl.Grant(id, docstore.Role(role)); err != nil {
				return err
			}
			return p.send(ctx)
		},
		Remove: func(ctx context.Context, id string) error {
			if err := p.acl.Revoke(id); err != nil {
				return err
			}
			return p.send(ctx)
		},
	})
}

func (p *peopleState) list() []user.Person { return p.acl.Active(p.links.now()).People() }
