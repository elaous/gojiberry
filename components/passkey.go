package components

import (
	"context"
	"errors"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

type PasskeyRow struct {
	ID, Label, Created, LastUsed string
	Synced                       bool
}

// PasskeyProps configures Passkey.
type PasskeyProps struct {
	// Supported says the browser can make and use passkeys here.
	Supported bool
	// SignedIn says someone is signed in; Rows are their passkeys.
	SignedIn     bool
	Rows         []PasskeyRow
	Email, Label string
	OnDraft      func(email, label string)
	SignIn       func(ctx context.Context) error
	Create       func(ctx context.Context, email, label string) error
	Add          func(ctx context.Context, label string) error
	Remove       func(ctx context.Context, id string) error
	Pending      *Pending
	Ask          func(message string, do func())
}

func Passkey(p PasskeyProps) xango.Node {
	busy := p.Pending.Busy()
	if !p.Supported {
		return xango.El("div", []xango.Attr{xango.A("class", "gj-passkey")},
			xango.El("p", []xango.Attr{xango.A("class", "gj-passkey-unsupported")},
				xango.Text(tr("passkey.unsupported", "This browser or page cannot use passkeys (they need HTTPS and a recent browser)."))))
	}
	input := func(class, label, value, placeholder string, on func(string)) xango.Node {
		attrs := []xango.Attr{xango.A("type", "text"), xango.A("class", class), xango.A("value", value),
			xango.A("placeholder", placeholder), xango.A("aria-label", label)}
		if on != nil {
			attrs = append(attrs, xango.On("input", func(e xango.Event) { on(xango.EventValue[string](e)) }))
		}
		return xango.El("input", attrs)
	}
	var children []xango.Node
	if !p.SignedIn {
		if p.SignIn != nil {
			children = append(children, docButton("gj-passkey-signin", tr("passkey.signIn", "Sign in with a passkey"), busy, func() {
				p.Pending.Run(p.SignIn)
			}))
		}
		if p.Create != nil {
			email, label := p.Email, p.Label
			children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-passkey-create-form")},
				input("gj-passkey-email", tr("passkey.email", "E-mail address"), email, "name@example.com", func(v string) {
					if p.OnDraft != nil {
						p.OnDraft(v, label)
					}
				}),
				docButton("gj-passkey-create", tr("passkey.create", "Create an account with a passkey"), busy || strings.TrimSpace(email) == "", func() {
					p.Pending.Run(func(ctx context.Context) error { return p.Create(ctx, email, label) })
				})))
		}
	} else {
		var rows []xango.Node
		for _, r := range p.Rows {
			cells := []xango.Node{
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(r.Label)),
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(passkeyMeta(r))),
			}
			if p.Remove != nil {
				id, name := r.ID, r.Label
				cells = append(cells, docButton("gj-passkey-remove", tr("passkey.remove", "Remove"), busy,
					confirmed(p.Ask, trf("passkey.confirmRemove", "Remove the passkey %s? You will not be able to sign in with it any more. If it is your only way to sign in, it stays.", name),
						actOne(p.Pending, p.Remove, id))))
			}
			rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-passkey-item"), xango.A("data-id", r.ID)}, cells...))
		}
		children = append(children,
			xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")},
				xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("passkey.title", "Your passkeys")))),
			xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...))
		if p.Add != nil {
			label := p.Label
			children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-passkey-add-form")},
				input("gj-passkey-label", tr("passkey.label", "Name for this passkey"), label, tr("passkey.labelPlaceholder", "Laptop, phone…"), func(v string) {
					if p.OnDraft != nil {
						p.OnDraft(p.Email, v)
					}
				}),
				docButton("gj-passkey-add", tr("passkey.add", "Add a passkey"), busy, func() {
					p.Pending.Run(func(ctx context.Context) error { return p.Add(ctx, label) })
				})))
		}
	}
	children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-passkey-note")},
		xango.Text(tr("passkey.note", "A passkey is two factors in one: your device, and the fingerprint, face or PIN that unlocks it. There is no separate code to type."))))
	children = append(children, passkeyError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-passkey")}, children...)
}

func passkeyError(p *Pending) []xango.Node {
	var coded interface{ PasskeyCode() string }
	if err := p.Err(); err != nil && errors.As(err, &coded) && coded.PasskeyCode() == "account_exists" {
		return []xango.Node{xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error"), xango.A("role", "alert")},
			xango.Text(tr("passkey.alreadyCreated", "The first passkey for this account was already made, in another tab or on another device. Reload this page and add another passkey from your account if you want one. The passkey this device just made is not used: you can delete it in your browser's or authenticator's passkey settings.")))}
	}
	return pendingError(p)
}

func passkeyMeta(r PasskeyRow) string {
	parts := []string{trf("passkey.created", "added %s", r.Created)}
	if r.LastUsed != "" {
		parts = append(parts, trf("passkey.lastUsed", "last used %s", r.LastUsed))
	}
	if r.Synced {
		parts = append(parts, tr("passkey.synced", "synced across your devices"))
	}
	return strings.Join(parts, " · ")
}
