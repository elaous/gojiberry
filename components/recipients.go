package components

import (
	"context"
	"strings"

	"github.com/elaous/xango"
)

type RecipientRow struct {
	Label, Type, Fingerprint   string
	Detail                     string
	Uses                       int
	Verified                   bool
	VerifiedBy                 string
	Attested                   bool
	OwnerSigned, OwnerEnrolled bool
	CanRemove                  bool
	RemoveBlocked              string
}

type DirRow struct {
	Label, Type, Fingerprint, Key string
	Verified                      bool
	// VerifiedBy is the host of the directory that answered (RecipientRow.VerifiedBy).
	VerifiedBy string
	Attested   bool
	// OwnerSigned and OwnerEnrolled: see RecipientRow; the strongest mark.
	OwnerSigned, OwnerEnrolled bool
	Note                       string
}

// RecipientsProps configures Recipients.
type RecipientsProps struct {
	// owner keys are shown, never removed
	Owner, Shared []RecipientRow
	// Draft is the public key being pasted.
	Draft, Confirm   string
	Parsed           *RecipientRow
	ParseError       string
	OnDraft          func(key, confirm string)
	Add              func(ctx context.Context, key, confirm string) error
	Remove           func(ctx context.Context, fingerprint string) (int, error)
	OnRemoved        func(fingerprint string, files int)
	RemoveEverywhere func(ctx context.Context, fingerprint string) error
	Notice           string
	Pending          *Pending
	Ask              func(message string, do func())

	Directory     func(ctx context.Context, email string) ([]DirRow, error)
	DirEmail      string
	OnDirEmail    func(email string)
	OnDirResults  func(email string, rows []DirRow)
	DirResults    []DirRow
	DirSearched   bool
	OnPick        func(row DirRow)
	TrustVerified bool

	// My passkey as a key.
	AddMyPasskey     func(ctx context.Context) error
	PasskeySupported bool
	MyPasskeyAdded   *RecipientRow
	// Confidential says the document is end to end encrypted.
	Confidential          bool
	SecondKeyMessage      string
	CreateRecoveryKey     func(ctx context.Context) (secret string, err error)
	OnRecoveryKey         func(secret string)
	RecoverySecret        string
	OnRecoveryDone        func()
	Copy                  func(text string)
	Download              func(name, text string)
	CommitRecoveryKey     func(ctx context.Context) error
	CancelRecoveryKey     func()
	RecoveryAcknowledged  bool
	OnRecoveryAcknowledge func(saved bool)

	RemoveOwner  func(ctx context.Context, fingerprint string) error
	ReplaceOwner func(ctx context.Context, fingerprint string) error

	Publish       func(ctx context.Context) error
	PublishNotice string
}

func RemovedNotice(files int) string {
	return trf("recipients.removed", "Stopped sharing. %d stored files were encrypted again without that key.", files)
}

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
		if r.Detail != "" {
			cells = append(cells, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-detail")}, xango.Text(r.Detail)))
		}
		if r.Verified {
			cells = append(cells, verifiedMark(true, r.Attested, r.OwnerSigned, r.OwnerEnrolled, r.VerifiedBy))
		}
		cells = append(cells, extra...)
		return xango.El("li", []xango.Attr{xango.A("class", class), xango.A("data-fingerprint", r.Fingerprint)}, cells...)
	}
	var rows []xango.Node
	for _, r := range p.Owner {
		extra := []xango.Node{xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(tr("recipients.owner", "owner")))}
		extra = append(extra, ownerControls(p, r, busy)...)
		rows = append(rows, row(r, "gj-recip-item gj-recip-owner", extra...))
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
					func() {
						p.Pending.Run(func(ctx context.Context) error {
							n, err := p.Remove(ctx, fp)
							if err == nil && p.OnRemoved != nil {
								p.OnRemoved(fp, n)
							}
							return err
						})
					})))
		}
		if p.RemoveEverywhere != nil && r.Uses > 1 {
			fp, name := r.Fingerprint, r.Label
			if name == "" {
				name = fp
			}
			extra = append(extra,
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-recip-uses")},
					xango.Text(trf("recipients.uses", "opens %d documents", r.Uses))),
				docButton("gj-recip-remove-all", trf("recipients.removeAll", "Remove from all %d documents", r.Uses), busy,
					confirmed(p.Ask, trf("recipients.confirmRemoveAll", "%s can open %d documents. Removing this key decrypts every one of them and encrypts it again without it, so they cannot open what is saved from now on. They keep anything they already opened or copied: that cannot be taken back. Continue?", name, r.Uses),
						actOne(p.Pending, p.RemoveEverywhere, fp))))
		}
		rows = append(rows, row(r, "gj-recip-item", extra...))
	}
	children := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")},
			xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("recipients.title", "Keys that can open this document")))),
		xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...),
	}
	if p.Notice != "" {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-recip-notice"), xango.A("role", "status")}, xango.Text(p.Notice)))
	}
	children = append(children, myKeyControl(p, busy)...)
	children = append(children, publishControl(p, busy)...)
	if p.Add != nil {
		children = append(children, directoryPicker(p, busy)...)
		keyAttrs := []xango.Attr{xango.A("class", "gj-recip-key"), xango.A("rows", "3"), xango.A("value", p.Draft),
			xango.A("placeholder", tr("recipients.keyPlaceholder", "Paste a public key: ssh-ed25519 AAAA… or age1…")),
			xango.A("aria-label", tr("recipients.key", "Public key"))}
		confirm := p.Confirm
		if p.OnDraft != nil {
			keyAttrs = append(keyAttrs, xango.On("input", func(e xango.Event) { p.OnDraft(xango.EventValue[string](e), "") }))
		}
		// the draft is both the value attribute and the textarea child text
		form := []xango.Node{xango.El("textarea", keyAttrs, xango.Text(p.Draft))}
		draft := p.Draft
		ready := false
		switch {
		case p.Parsed != nil:
			form = append(form,
				xango.El("p", []xango.Attr{xango.A("class", "gj-recip-parsed")},
					xango.Text(trf("recipients.parsed", "%s key for %s. Its fingerprint is:", p.Parsed.Type, orNone(p.Parsed.Label)))),
				xango.El("code", []xango.Attr{xango.A("class", "gj-recip-fingerprint gj-recip-parsed-fingerprint")}, xango.Text(p.Parsed.Fingerprint)),
				verifiedMark(p.Parsed.Verified, p.Parsed.Attested, p.Parsed.OwnerSigned, p.Parsed.OwnerEnrolled, p.Parsed.VerifiedBy))
			if p.TrustVerified && p.Parsed.Verified {
				confirm = p.Parsed.Fingerprint
				ready = true
				break
			}
			form = append(form, xango.El("p", []xango.Attr{xango.A("class", "gj-recip-hint")},
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
