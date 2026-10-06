package components

import (
	"context"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// RecipientRow is a key a document is encrypted to, as the list shows it.
type RecipientRow struct {
	// Label says who the key is meant for (an e-mail, a name): a label, not what makes it
	// trusted. Type is "ssh-ed25519", "ssh-rsa" or "age-x25519". Fingerprint is what the
	// owner confirms.
	Label, Type, Fingerprint string
}

// RecipientsProps configures Recipients.
type RecipientsProps struct {
	// Owner are the owner's own keys (shown, never removed); Shared the keys the document is
	// shared with.
	Owner, Shared []RecipientRow
	// Draft is the public key being pasted. Parsed is what it parsed to, nil while it does not
	// parse (ParseError says why, once something is typed). Confirm is the fingerprint typed to
	// confirm it. OnDraft reports changes to either.
	Draft, Confirm string
	Parsed         *RecipientRow
	ParseError     string
	OnDraft        func(key, confirm string)
	// Add shares the document with the draft key, given the fingerprint confirmed; Remove takes a
	// key out and encrypts the document again without it. A nil operation leaves its control out.
	Add     func(ctx context.Context, key, confirm string) error
	Remove  func(ctx context.Context, fingerprint string) error
	Pending *Pending
	// Ask, if set, is asked before a key is removed (see Asker.Ask).
	Ask func(message string, do func())
}

// Recipients lists the keys a document is encrypted to and shares it with one more. Adding a
// key shows its type, label and fingerprint and asks the owner to type the fingerprint, which
// they should have checked with the person (a pasted key for the wrong person is not added by a
// click alone). Removing one says what it does and does not do.
func Recipients(p RecipientsProps) xango.Node {
	busy := p.Pending.Busy()
	row := func(r RecipientRow, class string, extra ...xango.Node) xango.Node {
		label := r.Label
		if label == "" {
			label = tr("recipients.unlabeled", "(no label)")
		}
		cells := []xango.Node{
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(label)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-type")}, xango.Text(r.Type)),
			xango.El("code", []xango.Attr{xango.A("class", "gj-recip-fingerprint")}, xango.Text(r.Fingerprint)),
		}
		cells = append(cells, extra...)
		return xango.El("li", []xango.Attr{xango.A("class", class), xango.A("data-fingerprint", r.Fingerprint)}, cells...)
	}
	var rows []xango.Node
	for _, r := range p.Owner {
		rows = append(rows, row(r, "gj-recip-item gj-recip-owner",
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(tr("recipients.owner", "owner")))))
	}
	for _, r := range p.Shared {
		var extra []xango.Node
		if p.Remove != nil {
			fp, name := r.Fingerprint, r.Label
			if name == "" {
				name = fp
			}
			extra = append(extra, docButton("gj-recip-remove", tr("recipients.remove", "Remove"), busy,
				confirmed(p.Ask, trf("recipients.confirmRemove", "Stop sharing with %s? The document is encrypted again without them, so they cannot open what is saved from now on. They keep anything they already opened or copied: that cannot be taken back.", name),
					actOne(p.Pending, p.Remove, fp))))
		}
		rows = append(rows, row(r, "gj-recip-item", extra...))
	}
	children := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")},
			xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("recipients.title", "Keys that can open this document")))),
		xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...),
	}
	if p.Add != nil {
		keyAttrs := []xango.Attr{xango.A("class", "gj-recip-key"), xango.A("rows", "3"), xango.A("value", p.Draft),
			xango.A("placeholder", tr("recipients.keyPlaceholder", "Paste a public key: ssh-ed25519 AAAA… or age1…")),
			xango.A("aria-label", tr("recipients.key", "Public key"))}
		confirm := p.Confirm
		if p.OnDraft != nil {
			keyAttrs = append(keyAttrs, xango.On("input", func(e xango.Event) { p.OnDraft(xango.EventValue[string](e), "") }))
		}
		form := []xango.Node{xango.El("textarea", keyAttrs)}
		draft := p.Draft
		ready := false
		switch {
		case p.Parsed != nil:
			form = append(form,
				xango.El("p", []xango.Attr{xango.A("class", "gj-recip-parsed")},
					xango.Text(trf("recipients.parsed", "%s key for %s. Its fingerprint is:", p.Parsed.Type, orNone(p.Parsed.Label)))),
				xango.El("code", []xango.Attr{xango.A("class", "gj-recip-fingerprint gj-recip-parsed-fingerprint")}, xango.Text(p.Parsed.Fingerprint)),
				xango.El("p", []xango.Attr{xango.A("class", "gj-recip-hint")},
					xango.Text(tr("recipients.checkHint", "Check this fingerprint with the person, by another way than the one you got the key by, then type it here to add the key."))))
			confAttrs := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-recip-confirm"), xango.A("value", confirm),
				xango.A("aria-label", tr("recipients.confirm", "Fingerprint, typed to confirm")), xango.A("placeholder", "SHA256:…")}
			if p.OnDraft != nil {
				confAttrs = append(confAttrs, xango.On("input", func(e xango.Event) { p.OnDraft(draft, xango.EventValue[string](e)) }))
			}
			form = append(form, xango.El("input", confAttrs))
			ready = strings.TrimSpace(confirm) == p.Parsed.Fingerprint
		case strings.TrimSpace(p.Draft) != "" && p.ParseError != "":
			form = append(form, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error gj-recip-error"), xango.A("role", "alert")}, xango.Text(p.ParseError)))
		}
		form = append(form, docButton("gj-recip-add", tr("recipients.add", "Add key"), busy || !ready, func() {
			p.Pending.Run(func(ctx context.Context) error { return p.Add(ctx, draft, confirm) })
		}))
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-recip-form")}, form...))
	}
	children = append(children, pendingError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-recip")}, children...)
}

func orNone(s string) string {
	if s == "" {
		return tr("recipients.unlabeled", "(no label)")
	}
	return s
}
