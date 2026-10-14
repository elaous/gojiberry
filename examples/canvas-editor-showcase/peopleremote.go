//go:build js && wasm

package main

import (
	"context"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/xango"
)

// The People tab on a real server (roadmap 75). While the document open is saved on the docstore server the
// person confirmed, the list shown is the server's own (docstore.HTTPAdapter.LoadACL), every change to the
// people is sent to it (SaveACL), and share links are made and revoked there (CreateLink, RevokeLink): what
// the tab shows is what the server enforces. Anywhere else the list lives in this page, as before, and says so.

// server is the adapter and document id when the document open is on the server.
func (p *peopleState) server() (a *docstore.HTTPAdapter, id string, ok bool) {
	if p.remote == nil {
		return nil, "", false
	}
	return p.remote()
}

// sync reads the server's list the first time the panel is drawn for a document that is on the server.
func (p *peopleState) sync() {
	a, id, ok := p.server()
	switch {
	case !ok:
		p.syncedFor = ""
		return
	case p.syncedFor == id:
		return
	}
	p.syncedFor = id
	p.note = T("people.reading", "Reading the access list from the server…")
	go p.load(a, id)
}

// load reads the server's list for id and draws it.
func (p *peopleState) load(a *docstore.HTTPAdapter, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	acl, err := a.LoadACL(ctx, id)
	if err != nil {
		p.note = T("people.readFailed", "The access list could not be read from the server: %s", err.Error())
	} else {
		if acl.Owner != "" { // an unshared document has no list yet: the person stays its owner
			p.acl = acl
		}
		p.note = T("people.fromServer", "This list is the server's. Changes are saved there as you make them.")
	}
	p.rerender()
}

// send sends the people of the list to the server (when the document is on it), and reads the server's list
// back when it refuses, so the tab never shows what the server did not accept.
func (p *peopleState) send(ctx context.Context) error {
	a, id, ok := p.server()
	if !ok {
		return nil
	}
	if err := a.SaveACL(ctx, id, p.acl); err != nil {
		p.syncedFor, p.note = "", T("people.sendFailed", "The server did not accept the change: %s", err.Error())
		go p.load(a, id)
		return err
	}
	p.note = T("people.fromServer", "This list is the server's. Changes are saved there as you make them.")
	return nil
}

// serverNote says where the list is kept.
func (p *peopleState) serverNote() xango.Node {
	text := T("people.local", "This list is kept in this page only. Save the document to a server to share it there.")
	if _, _, ok := p.server(); ok {
		text = p.note
	}
	return xango.El("p", []xango.Attr{xango.A("id", "people-where"), xango.A("class", "gj-doc-meta"), xango.A("role", "status")}, xango.Text(text))
}
