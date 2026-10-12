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
	return saver.Target{
		Name:    serverTarget,
		Label:   T("server.label", "Server %s (the server can read these documents)", d.host()),
		Adapter: docstore.NewHTTPAdapter(d.url, client),
	}, true
}

// applyServerTarget makes the server a save target while a server is confirmed, and removes it when the
// person forgets the server. It draws nothing (the page is not drawn yet when it first runs).
func (u *ui) applyServerTarget() {
	if t, ok := u.serverStore(); ok {
		u.saver.Saver.AddTarget(t)
		return
	}
	u.saver.Saver.RemoveTarget(serverTarget)
}

// syncServerTarget follows a change of the confirmed server: the target comes or goes, and when the
// saver was on the server it goes back to the browser (a document saved there stays there).
func (u *ui) syncServerTarget() {
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
