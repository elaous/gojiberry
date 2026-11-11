//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/user"
	"github.com/elaous/gojiberry/xango"
)

type peopleState struct {
	acl         docstore.ACL
	draft, role string
	pending     *components.Pending
	rerender    func()
	links       *linksUI // share links (links.go)

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

func (p *peopleState) handles() map[string]string {
	out := map[string]string{}
	taken := map[string]bool{}
	for _, person := range p.list() {
		addr := strings.ToLower(strings.TrimSpace(person.Email))
		if addr == "" {
			addr = strings.ToLower(strings.TrimSpace(person.ID))
		}
		base := person.Username
		if base == "" {
			base, _, _ = strings.Cut(addr, "@")
		}
		base = strings.Trim(handleChars.ReplaceAllString(base, "-"), "-.")
		if base == "" {
			base = "person"
		}
		h := base
		for i := 2; taken[strings.ToLower(h)]; i++ {
			h = fmt.Sprintf("%s-%d", base, i)
		}
		taken[strings.ToLower(h)] = true
		out[addr] = h
		if id := strings.ToLower(strings.TrimSpace(person.ID)); id != addr {
			out[id] = h
		}
	}
	return out
}

var handleChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func (p *peopleState) HandleOf(address string) (string, bool) {
	h, ok := p.handles()[strings.ToLower(strings.TrimSpace(address))]
	return h, ok
}

func (p *peopleState) Has(handle string) bool {
	for _, h := range p.handles() {
		if strings.EqualFold(h, handle) {
			return true
		}
	}
	return false
}

func (p *peopleState) list() []user.Person { return p.acl.Active(p.links.now()).People() }
