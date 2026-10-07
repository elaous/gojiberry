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
	// Uses is how many documents the key can open (this one included); 0 means unknown.
	Uses int
	// Verified marks a key that the store's directory says was published by an account with a
	// verified e-mail, while signed in (encrypt.PublishedKey.Verified). It proves the account
	// and that the account published the key, not that the key is safe; without it the
	// e-mail is only a label and the owner confirms the fingerprint.
	Verified bool
	// VerifiedBy is the host of the directory that said so ("verified by dir.example"): the mark
	// is that directory's word, and a page whose directory address came from a link must not let
	// it pass for more. Empty shows the plain mark.
	VerifiedBy string
	// CanRemove and RemoveBlocked are for an owner key when RecipientsProps.RemoveOwner is set:
	// whether the host would let the key be taken out, and, when not, why in words (the last key,
	// the only way back, the store unlocked with this key alone).
	CanRemove     bool
	RemoveBlocked string
}

// DirRow is a key found in the store's directory for an e-mail address (encrypt.PublishedKey),
// as the picker lists it. Key is the public key text that fills the draft.
type DirRow struct {
	Label, Type, Fingerprint, Key string
	Verified                      bool
	// VerifiedBy is the host of the directory that answered (RecipientRow.VerifiedBy).
	VerifiedBy string
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
	// key out and encrypts the document again without it, and returns how many files it
	// encrypted again (Sharing.Remove). A nil operation leaves its control out.
	Add    func(ctx context.Context, key, confirm string) error
	Remove func(ctx context.Context, fingerprint string) (int, error)
	// OnRemoved is called after a Remove that worked, with the count Remove returned; the
	// host can keep it and pass RemovedNotice(n) as Notice on the next render.
	OnRemoved func(fingerprint string, files int)
	// RemoveEverywhere takes a key out of every document that uses it (each is decrypted and
	// encrypted again without it). Offered next to Remove for a key with Uses above one.
	RemoveEverywhere func(ctx context.Context, fingerprint string) error
	// Notice is a status line shown under the list (for instance RemovedNotice).
	Notice  string
	Pending *Pending
	// Ask, if set, is asked before a key is removed (see Asker.Ask).
	Ask func(message string, do func())

	// Directory looks up the keys published for an e-mail address (nil leaves the picker out).
	// DirEmail is the address typed (OnDirEmail reports edits); after a lookup that worked,
	// OnDirResults gets the address and rows, which the host keeps and passes back as
	// DirResults with DirSearched true. Choosing a row calls OnPick; with no OnPick the row's
	// key fills the draft (OnDraft). A host that handles OnPick sets Draft and Parsed, and
	// Parsed.Verified from the row, never from what was pasted.
	Directory    func(ctx context.Context, email string) ([]DirRow, error)
	DirEmail     string
	OnDirEmail   func(email string)
	OnDirResults func(email string, rows []DirRow)
	DirResults   []DirRow
	DirSearched  bool
	OnPick       func(row DirRow)
	// TrustVerified lets a draft key that is Parsed.Verified be added without typing its
	// fingerprint (it is still shown). Off by default: a verified mark proves the account, not
	// the key's safety.
	TrustVerified bool

	// My passkey as a key. AddMyPasskey derives the owner's own passkey key (client.DeriveKey,
	// encrypt.PRFRecipient) and adds it (Sharing.Add); nil leaves the control out. The owner is
	// deriving their own key, so no fingerprint is typed; MyPasskeyAdded (set by the host after
	// it worked) shows the fingerprint that was added. PasskeySupported false shows a note
	// instead of the button.
	AddMyPasskey     func(ctx context.Context) error
	PasskeySupported bool
	MyPasskeyAdded   *RecipientRow
	// Confidential says the document is end to end encrypted. Then, when none of Owner's
	// keys is something other than a passkey (every Owner row has Type "passkey-prf", or there
	// are none), another passkey cannot be added (encrypt.CheckRecoverableOwner): the control shows SecondKeyMessage (encrypt.ErrNeedSecondRecipient's text; a default
	// is used when empty) and offers a recovery key.
	Confidential     bool
	SecondKeyMessage string
	// CreateRecoveryKey makes a recovery key (encrypt.GenerateRecoveryKey), adds its public part as
	// an owner key and returns the secret. The button is shown only when OnRecoveryKey is also
	// set (a key whose secret cannot be shown must not become an owner key). The component hands
	// the secret to OnRecoveryKey and keeps no copy; the host passes it back as RecoverySecret
	// until the person has saved it and OnRecoveryDone is called, and must then drop it: it is
	// shown once. What holds: the component does not keep the secret in the DOM after that
	// ("I have saved it"); the host holds it in memory while it is shown. Go strings cannot be
	// wiped, so a copy may stay in memory until it is collected. Copy and Download
	// (nil leaves the button out) are the host's clipboard and file save.
	CreateRecoveryKey func(ctx context.Context) (secret string, err error)
	OnRecoveryKey     func(secret string)
	RecoverySecret    string
	OnRecoveryDone    func()
	Copy              func(text string)
	Download          func(name, text string)
	// CommitRecoveryKey, when set, makes the recovery key transactional: CreateRecoveryKey only
	// prepares the key (nothing is added to the store yet) and returns its secret, the secret is
	// shown, and the key becomes an owner key only when the person acknowledges they saved it
	// (RecoveryAcknowledged, set by the host from OnRecoveryAcknowledge) and presses the confirm
	// button, which runs CommitRecoveryKey. A key whose secret was never seen is thereby never an
	// owner key. CancelRecoveryKey (nil leaves the button out) drops the prepared key and secret
	// without adding it. Without CommitRecoveryKey the key is already added when its secret is
	// shown, and "I have saved it" only hides the secret (the older behavior).
	CommitRecoveryKey     func(ctx context.Context) error
	CancelRecoveryKey     func()
	RecoveryAcknowledged  bool
	OnRecoveryAcknowledge func(saved bool)

	// RemoveOwner takes an owner key out of the store (each owner row gets "Remove this key",
	// enabled by its row's CanRemove). ReplaceOwner swaps a passkey for a new one; it is offered
	// on the rows of type "passkey-prf". Both ask first (Ask) and say what it does and does not do.
	RemoveOwner  func(ctx context.Context, fingerprint string) error
	ReplaceOwner func(ctx context.Context, fingerprint string) error

	// Publish publishes the owner's keys in the store's key directory (the "Publish my keys"
	// button, shown with the directory picker; nil leaves it out). PublishNotice is the line the
	// host shows after it, or why it could not.
	Publish       func(ctx context.Context) error
	PublishNotice string
}

// RemovedNotice is the line that says what a Remove did: how many stored files were encrypted
// again without the key.
func RemovedNotice(files int) string {
	return trf("recipients.removed", "Stopped sharing. %d stored files were encrypted again without that key.", files)
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
		if r.Verified {
			cells = append(cells, verifiedMark(true, r.VerifiedBy))
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
		// The draft is both the value attribute (what the client patches as the control's value)
		// and the child text (the only way a <textarea> has initial content in plain HTML).
		form := []xango.Node{xango.El("textarea", keyAttrs, xango.Text(p.Draft))}
		draft := p.Draft
		ready := false
		switch {
		case p.Parsed != nil:
			form = append(form,
				xango.El("p", []xango.Attr{xango.A("class", "gj-recip-parsed")},
					xango.Text(trf("recipients.parsed", "%s key for %s. Its fingerprint is:", p.Parsed.Type, orNone(p.Parsed.Label)))),
				xango.El("code", []xango.Attr{xango.A("class", "gj-recip-fingerprint gj-recip-parsed-fingerprint")}, xango.Text(p.Parsed.Fingerprint)),
				verifiedMark(p.Parsed.Verified, p.Parsed.VerifiedBy))
			if p.TrustVerified && p.Parsed.Verified {
				// The account is verified and published this key: no typing, the fingerprint
				// above is still shown. Add confirms with the fingerprint shown.
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
