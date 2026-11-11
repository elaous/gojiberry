//go:build js && wasm

package main

import (
	"context"
	"net/http"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

const serverTarget = "server"

func (u *ui) serverStore() (saver.Target, bool) {
	d := &u.enc.dir
	if d.url == "" {
		return saver.Target{}, false
	}
	client := &http.Client{Transport: bearerTransport{token: d.sessionToken}}
	adapter := docstore.NewHTTPAdapter(d.url, client)
	adapter.UseShareLink(u.shareToken) // "" removes it
	return saver.Target{
		Name:    serverTarget,
		Label:   T("server.label", "Server %s (the server can read these documents)", d.host()),
		Adapter: adapter,
	}, true
}

func (u *ui) useShareLink(token string) string {
	if encTarget == "server" {
		return T("links.incoming.encrypted", "A link cannot be used while the encrypted store lives on the server. Switch the store to this browser first.")
	}
	if _, ok := u.serverStore(); !ok {
		return T("links.incoming.noServer", "There is no server to use it with. Confirm a server below, then open the address again.")
	}
	u.shareToken = token
	u.applyServerTarget()
	u.people.links.inUse = true
	u.saver.Pending.Run(func(ctx context.Context) error {
		if err := u.saver.SetTarget(ctx, serverTarget); err != nil {
			return err
		}
		if doc := pageDoc(); doc != "" {
			return u.saver.Open(ctx, doc)
		}
		return nil
	})
	return T("links.incoming.started", "The link is in use. Documents it allows are in the Save tab.")
}

func (u *ui) stopShareLink() {
	u.shareToken = ""
	u.applyServerTarget()
	u.people.links.inUse = false
	u.people.links.incomingNote = ""
	u.people.rerender()
}

func (u *ui) applyServerTarget() {
	if encTarget == "server" {
		return // the encrypted store on the server is the server target
	}
	if t, ok := u.serverStore(); ok {
		u.saver.Saver.AddTarget(t)
		return
	}
	u.saver.Saver.RemoveTarget(serverTarget)
}

func (u *ui) syncServerTarget() {
	if encTarget == "server" {
		u.enc.render()
		return
	}
	if _, ok := u.serverStore(); !ok {
		if where, _ := u.saver.Where(); where == serverTarget {
			u.saver.Pending.Run(func(ctx context.Context) error {
				err := u.saver.SetTarget(ctx, "browser")
				u.saver.Saver.RemoveTarget(serverTarget)
				return err
			})
			return
		}
	}
	u.applyServerTarget()
	u.enc.render()
}
