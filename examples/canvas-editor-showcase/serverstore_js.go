//go:build js && wasm

package main

import (
	"context"
	"net/http"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

const serverTarget = "server"

// serverStore is the saver target for the docstore server the person confirmed (the key directory's
// address, with the same sign-in token): documents saved there are kept by the server, in whichever
// database it runs on, and every device that signs in to it sees them. They are not end-to-end
// encrypted: the server can read them (the label says so), and "confidential" stays a browser-only
// level until the encrypted store itself can live on a server.
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

// useShareLink applies a share link the person chose to use: the server target sends it with every request
// (docstore.HTTPAdapter.UseShareLink), the saver switches to the server and, when the address names a
// document (/d/{doc}), opens it. It answers in words. The server decides what the link allows.
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

// stopShareLink forgets the link: the server target goes back to sending only the person's own sign-in.
func (u *ui) stopShareLink() {
	u.shareToken = ""
	u.applyServerTarget()
	u.people.links.inUse = false
	u.people.links.incomingNote = ""
	u.people.rerender()
}

// applyServerTarget makes the server a save target while a server is confirmed, and removes it when the
// person forgets the server. It draws nothing (the page is not drawn yet when it first runs).
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

// syncServerTarget follows a change of the confirmed server: the target comes or goes, and when the
// saver was on the server it goes back to the browser (a document saved there stays there).
func (u *ui) syncServerTarget() {
	if encTarget == "server" {
		u.enc.render()
		return // the encrypted store's own target stays; changing the server takes effect at the next load
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
