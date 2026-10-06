//go:build js && wasm

// Command passkey-demo is a page that signs in with passkeys: components.Passkey over
// auth/passkey/client, talking to a server that mounts auth/passkey (cmd/server here, or
// cmd/docstore with GOJIBERRY_DOCSTORE_PASSKEY_RPID set). The page must be served over HTTPS, or
// from localhost, which the browser treats as secure.
package main

import (
	"context"
	"errors"
	"syscall/js"

	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/xango"
)

func main() {
	var (
		root         *domrt.Root
		render       func()
		token        string
		who          string
		rows         []components.PasskeyRow
		email, label string
	)
	pending := components.NewPending(func() { render() })
	asker := components.NewAsker(func() { render() })
	c := &client.Client{Token: func() string { return token }}

	refresh := func(ctx context.Context) error {
		list, err := c.List(ctx)
		if err != nil {
			return err
		}
		rows = rows[:0]
		for _, r := range list {
			rows = append(rows, components.PasskeyRow{ID: r.ID, Label: r.Label, Created: shortDate(r.Created), LastUsed: shortDate(r.LastUsed), Synced: r.Synced})
		}
		return nil
	}
	signedIn := func(ctx context.Context, tok string) error {
		token = tok
		who = "signed in"
		return refresh(ctx)
	}
	render = func() {
		status := "not signed in"
		if token != "" {
			status = who
		}
		node := xango.El("div", nil,
			xango.El("p", []xango.Attr{xango.A("id", "status")}, xango.Text(status)),
			components.Passkey(components.PasskeyProps{
				Supported: client.Supported(), SignedIn: token != "", Rows: rows, Email: email, Label: label,
				OnDraft: func(e, l string) { email, label = e, l; render() },
				SignIn: func(ctx context.Context) error {
					tok, err := c.SignIn(ctx)
					if errors.Is(err, client.ErrCancelled) {
						return nil
					}
					if err != nil {
						return err
					}
					return signedIn(ctx, tok)
				},
				Create: func(ctx context.Context, e, l string) error {
					tok, err := c.Register(ctx, e, "", l)
					if errors.Is(err, client.ErrCancelled) {
						return nil
					}
					if err != nil {
						return err
					}
					return signedIn(ctx, tok)
				},
				Add: func(ctx context.Context, l string) error {
					_, err := c.Register(ctx, "", "", l)
					if errors.Is(err, client.ErrCancelled) {
						return nil
					}
					if err != nil {
						return err
					}
					label = ""
					return refresh(ctx)
				},
				Remove: func(ctx context.Context, id string) error {
					if err := c.Remove(ctx, id); err != nil {
						return err
					}
					return refresh(ctx)
				},
				Pending: pending, Ask: asker.Ask,
			}),
			asker.Dialog())
		container := js.Global().Get("document").Call("getElementById", "app")
		if root == nil {
			root = domrt.Mount(container, node)
		} else {
			root.Update(node)
		}
	}
	render()
	select {}
}

func shortDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return ""
}
