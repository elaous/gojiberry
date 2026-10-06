package components

import (
	"context"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// PasskeyRow is a passkey of the signed-in person, as the list shows it.
type PasskeyRow struct {
	ID, Label, Created, LastUsed string
	// Synced says the passkey is backed up by a password manager or the platform (it can be used on
	// the person's other devices, and is not lost with this one).
	Synced bool
}

// PasskeyProps configures Passkey.
type PasskeyProps struct {
	// Supported says the browser can make and use passkeys here.
	Supported bool
	// SignedIn says someone is signed in; Rows are their passkeys.
	SignedIn bool
	Rows     []PasskeyRow
	// Email and Label are what is typed: the address for a new account, the name of a passkey
	// being added. OnDraft reports changes to either.
	Email, Label string
	OnDraft      func(email, label string)
	// SignIn signs in with a passkey; Create makes an account with one (needs an e-mail address);
	// Add adds a passkey to the signed-in account; Remove deletes one. A nil operation leaves its
	// control out.
	SignIn  func(ctx context.Context) error
	Create  func(ctx context.Context, email, label string) error
	Add     func(ctx context.Context, label string) error
	Remove  func(ctx context.Context, id string) error
	Pending *Pending
	// Ask, if set, is asked before a passkey is removed (see Asker.Ask).
	Ask func(message string, do func())
}

// Passkey is the sign-in and passkey-management control: "Sign in with a passkey" and "Create an
// account with a passkey" when nobody is signed in, and the person's passkeys with Add and Remove
// when someone is. A passkey is two factors in itself (the device, and the fingerprint, face or
// PIN that unlocks it), so there is no separate code to type; the control says so.
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
	children = append(children, pendingError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-passkey")}, children...)
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
