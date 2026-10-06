package components

import (
	"context"
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// passkeyType is encrypt.TypePasskey (components does not import the encryption package).
const passkeyType = "passkey-prf"

// needsSecondKey says whether adding a passkey must wait for a key that is not a passkey: the
// document is confidential and none of the owner's keys is anything but a passkey
// (encrypt.CheckRecoverableOwner counts the owner's keys only).
func needsSecondKey(p RecipientsProps) bool {
	if !p.Confidential {
		return false
	}
	for _, r := range p.Owner {
		if r.Type != passkeyType {
			return false
		}
	}
	return true
}

// verifiedMark is the mark next to a key, strongest first: "verified e-mail, signed by its owner"
// when the address's owner key signed the publication (ownerSigned: holds against the server that
// runs the directory too), "verified e-mail, signed by the key" when the key attested the binding
// itself, plain "verified e-mail", or a label only that needs its fingerprint confirmed. None
// says the key is safe. With ownerEnrolled and no owner signature a verified key also carries a
// note: the address has an owner key and this key was not signed by it.
func verifiedMark(verified, attested, ownerSigned, ownerEnrolled bool) xango.Node {
	if verified && ownerSigned {
		return xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-mark gj-recip-verified gj-recip-owner-signed"),
			xango.A("title", tr("recipients.ownerSignedHelp", "The owner of this address holds a signing key of their own and signed this key's publication, so not even the server that runs the directory could have added it. That does not prove the key is safe."))},
			xango.Text(tr("recipients.ownerSigned", "verified e-mail, signed by its owner")))
	}
	if verified && attested {
		return xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-mark gj-recip-verified gj-recip-attested"),
			xango.A("title", tr("recipients.attestedHelp", "The key was published by an account whose e-mail is verified, and the key itself signed that it belongs to this address, so the server alone could not have made that up. That does not prove the key is safe."))},
			xango.Text(tr("recipients.attested", "verified e-mail, signed by the key")))
	}
	if verified {
		mark := xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-mark gj-recip-verified"),
			xango.A("title", tr("recipients.verifiedHelp", "The key was published by an account whose e-mail is verified. That proves the account, not that the key is safe."))},
			xango.Text(tr("recipients.verified", "verified e-mail")))
		if ownerEnrolled {
			return xango.El("span", nil, mark, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-mark gj-recip-owner-unsigned"),
				xango.A("title", tr("recipients.ownerUnsignedHelp", "This address has an owner key, and the owner did not sign this key: it was published before the owner key was set up. Confirm the fingerprint with the person."))},
				xango.Text(tr("recipients.ownerUnsigned", "not signed by the owner key"))))
		}
		return mark
	}
	return xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-mark gj-recip-label-only")},
		xango.Text(tr("recipients.labelOnly", "label only: confirm the fingerprint")))
}

// directoryPicker is the "find a person" step: an e-mail, a lookup in the store's directory and
// the keys it lists, each with its verified mark and a button that fills the draft.
func directoryPicker(p RecipientsProps, busy bool) []xango.Node {
	if p.Directory == nil {
		return nil
	}
	email := strings.TrimSpace(p.DirEmail)
	attrs := []xango.Attr{xango.A("type", "email"), xango.A("class", "gj-recip-dir-email"), xango.A("value", p.DirEmail),
		xango.A("aria-label", tr("recipients.dirEmail", "E-mail address to look up")),
		xango.A("placeholder", tr("recipients.dirPlaceholder", "Find a person by e-mail"))}
	if p.OnDirEmail != nil {
		attrs = append(attrs, xango.On("input", func(e xango.Event) { p.OnDirEmail(xango.EventValue[string](e)) }))
	}
	find := docButton("gj-recip-dir-find", tr("recipients.dirFind", "Find keys"), busy || email == "", func() {
		p.Pending.Run(func(ctx context.Context) error {
			rows, err := p.Directory(ctx, email)
			if err == nil && p.OnDirResults != nil {
				p.OnDirResults(email, rows)
			}
			return err
		})
	})
	nodes := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-recip-dir")},
		xango.El("input", attrs), find)}
	if len(p.DirResults) > 0 {
		var items []xango.Node
		for _, r := range p.DirResults {
			r := r
			pick := func() {
				if p.OnPick != nil {
					p.OnPick(r)
				} else if p.OnDraft != nil {
					p.OnDraft(r.Key, "")
				}
			}
			label := r.Label
			if label == "" {
				label = email
			}
			items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-recip-item gj-recip-dir-row"), xango.A("data-fingerprint", r.Fingerprint)},
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(orNone(label))),
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-type")}, xango.Text(r.Type)),
				xango.El("code", []xango.Attr{xango.A("class", "gj-recip-fingerprint")}, xango.Text(r.Fingerprint)),
				verifiedMark(r.Verified, r.Attested, r.OwnerSigned, r.OwnerEnrolled),
				docButton("gj-recip-pick", tr("recipients.pick", "Use this key"), busy, pick)))
		}
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list gj-recip-dir-results")}, items...))
	} else if p.DirSearched {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("class", "gj-recip-hint gj-recip-dir-none"), xango.A("role", "status")},
			xango.Text(tr("recipients.dirNone", "No keys are published for that address. Paste the key instead; it stays label only until you confirm its fingerprint."))))
	}
	return nodes
}

// myKeyControl is "Add my passkey as a key" with its states: not supported, ready, a second
// key needed first (and the recovery key, shown once), and the fingerprint once added.
func myKeyControl(p RecipientsProps, busy bool) []xango.Node {
	if p.AddMyPasskey == nil {
		return nil
	}
	cls := func(c string) []xango.Attr { return []xango.Attr{xango.A("class", c)} }
	var box []xango.Node
	box = append(box, xango.El("h5", cls("gj-recip-my-title"), xango.Text(tr("recipients.myTitle", "Your passkey"))))
	switch {
	case !p.PasskeySupported:
		box = append(box, xango.El("p", append(cls("gj-recip-hint gj-recip-unsupported"), xango.A("role", "status")),
			xango.Text(tr("recipients.unsupported", "This browser or passkey cannot be used as an encryption key (it does not support the PRF extension). Use an SSH key or a recovery key instead."))))
	case needsSecondKey(p):
		msg := p.SecondKeyMessage
		if msg == "" {
			msg = tr("recipients.needSecond", "A passkey cannot be the only key: add a recovery key or an SSH key too")
		}
		box = append(box,
			xango.El("p", append(cls("gj-doc-error gj-recip-need-second"), xango.A("role", "alert")), xango.Text(msg)),
			docButton("gj-recip-add-mine", tr("recipients.addMine", "Add my passkey as a key"), true, nil))
		// Without OnRecoveryKey the secret could not be shown, and the key it belongs to would
		// already be an owner key: so the button is left out then.
		if p.CreateRecoveryKey != nil && p.OnRecoveryKey != nil && p.RecoverySecret == "" {
			box = append(box, docButton("gj-recip-recovery", tr("recipients.createRecovery", "Create a recovery key"), busy, func() {
				p.Pending.Run(func(ctx context.Context) error {
					secret, err := p.CreateRecoveryKey(ctx)
					if err == nil && p.OnRecoveryKey != nil {
						p.OnRecoveryKey(secret)
					}
					return err
				})
			}))
		}
	default:
		box = append(box,
			xango.El("p", cls("gj-recip-hint"), xango.Text(tr("recipients.myHint", "Your passkey asks you to confirm (fingerprint or PIN) and becomes a key that can open this document. Its fingerprint is shown once it is added; nothing is typed, because it is your own key."))),
			docButton("gj-recip-add-mine", tr("recipients.addMine", "Add my passkey as a key"), busy, func() {
				p.Pending.Run(func(ctx context.Context) error { return p.AddMyPasskey(ctx) })
			}))
	}
	if a := p.MyPasskeyAdded; a != nil {
		box = append(box,
			xango.El("p", append(cls("gj-recip-parsed gj-recip-my-added"), xango.A("role", "status")),
				xango.Text(tr("recipients.myAdded", "Your passkey was added. Its fingerprint is:"))),
			xango.El("code", cls("gj-recip-fingerprint gj-recip-my-fingerprint"), xango.Text(a.Fingerprint)))
	}
	box = append(box, recoveryKeyShown(p)...)
	return []xango.Node{xango.El("div", cls("gj-recip-my"), box...)}
}

// recoveryKeyShown shows the recovery key's secret, once: copyable and downloadable, with the
// warning that it must be kept offline and cannot be recovered. It exists only while the host
// passes RecoverySecret; "I have saved it" calls OnRecoveryDone, after which the host drops it.
func recoveryKeyShown(p RecipientsProps) []xango.Node {
	if p.RecoverySecret == "" {
		return nil
	}
	secret := p.RecoverySecret
	cls := func(c string) []xango.Attr { return []xango.Attr{xango.A("class", c)} }
	nodes := []xango.Node{
		xango.El("p", append(cls("gj-recip-recovery-warning"), xango.A("role", "alert")),
			xango.Text(tr("recipients.recoveryWarning", "This is your recovery key. It is shown only now. Keep it offline (print it or store it in a password manager). We cannot recover it: if you lose it and your passkey, this document is lost."))),
		xango.El("textarea", []xango.Attr{xango.A("class", "gj-recip-key gj-recip-secret"), xango.A("rows", "2"), xango.A("readonly", "readonly"),
			xango.A("aria-label", tr("recipients.recoverySecret", "Recovery key (secret)")), xango.A("value", secret)}, xango.Text(secret)),
	}
	if p.Copy != nil {
		nodes = append(nodes, docButton("gj-recip-copy", tr("recipients.copy", "Copy"), false, func() { p.Copy(secret) }))
	}
	if p.Download != nil {
		nodes = append(nodes, docButton("gj-recip-download", tr("recipients.download", "Download"), false, func() { p.Download("recovery-key.txt", secret+"\n") }))
	}
	if p.OnRecoveryDone != nil {
		nodes = append(nodes, docButton("gj-recip-recovery-done", tr("recipients.recoveryDone", "I have saved it"), false, p.OnRecoveryDone))
	}
	return []xango.Node{xango.El("div", cls("gj-recip-recovery-box"), nodes...)}
}
