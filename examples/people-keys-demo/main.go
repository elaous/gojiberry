//go:build js && wasm

// Command people-keys-demo is the host code the People tab's key controls need: the
// components.Recipients props wired to the encryption library and the store's key directory.
//
//   - AddMyPasskey: client.DeriveKey (the passkey's PRF output) -> encrypt.PRFRecipient ->
//     encrypt.Sharing.Add. No fingerprint is typed: it is the owner's own key.
//   - CreateRecoveryKey: encrypt.GenerateRecoveryKey; the secret is shown once, and the public part
//     becomes an owner key only when the person confirms they saved it (CommitRecoveryKey).
//   - Directory: directoryclient.Lookup on the docstore server. A row's marks come from what the
//     server answered, and the owner mark only if the client re-checked the owner's signature: a
//     pasted key is "label only" however it is worded.
//   - My directory entry: publish my passkey's key (proof of possession with the passkey's own PRF
//     identity, and the owner signature when the address is enrolled), revoke it, enroll an owner
//     key (made and kept in this browser, in localStorage) and watch the log for my address:
//     "someone published a key for your address" is the report of an entry I did not make.
//
// The document is in memory and the owner's signing key is made for the page; a real host keeps
// the document in its store (see docs/encryption-and-passkeys.md). Serve it with cmd/server.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"

	"filippo.io/age"

	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/directoryclient"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/xango"
)

// docID is the one confidential document this page shares.
const docID = "00000000-0000-4000-8000-000000000001"

type page struct {
	render func()
	token  string // the passkey session
	// dirToken is a provider session (demo only, /demo/provider-session): the directory's lookup
	// is closed to passkey sessions.
	dirToken string

	store   docstore.Adapter
	ring    *encrypt.StoreKeyring
	sharing *encrypt.Sharing
	owner   []encrypt.Recipient // the owner's own keys: recovery keys and passkeys
	shared  []components.RecipientRow

	draft, confirm string
	parseErr       string
	dirEmail       string
	dirRows        []components.DirRow
	dirSearched    bool
	dirVerified    map[string]bool // fingerprint -> the directory said so, for the last lookup
	dirAttested    map[string]bool // fingerprint -> the directory said the key signed the binding too
	dirOwnerSigned map[string]bool // fingerprint -> the owner signed it, and this client checked the signature
	dirEnrolled    map[string]bool // fingerprint -> the address of the row has an owner key
	ownerAdded     map[string]bool // keys added from an owner-signed row
	verifiedAdded  map[string]bool // keys added from a verified row
	attestedAdded  map[string]bool // keys added from an attested row

	// My own directory entry (the demo's pretend provider account, ana@example.com).
	dc             *directoryclient.Client
	ownerSigning   directoryclient.OwnerKey // the owner signing key, kept in localStorage; nil until enrolled
	watch          directoryclient.WatchState
	dirStatus      string
	dirAlerts      []string
	recoverySecret string
	recoveryKey    *encrypt.Recipient // made, its secret on screen, not an owner key yet
	recoveryAcked  bool
	myAdded        *components.RecipientRow
	notice         string

	pending *components.Pending
	asker   *components.Asker
	c       *client.Client
}

func recipientRow(r encrypt.Recipient, verified, attested bool) components.RecipientRow {
	return components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Verified: verified, Attested: verified && attested}
}

// myAddress is the address of the demo's pretend identity-provider account.
const myAddress = "ana@example.com"

func store(key, value string) {
	defer func() { _ = recover() }() // storage can be blocked: the page works without it
	js.Global().Get("localStorage").Call("setItem", key, value)
}

func load(key string) string {
	defer func() { _ = recover() }()
	v := js.Global().Get("localStorage").Call("getItem", key)
	if v.IsNull() || v.IsUndefined() {
		return ""
	}
	return v.String()
}

func parseKey(text string) (encrypt.Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return encrypt.ParseAgeRecipient(text)
	}
	return encrypt.ParseSSHRecipient(text)
}

func newPage() (*page, error) {
	p := &page{dirVerified: map[string]bool{}, dirAttested: map[string]bool{}, dirOwnerSigned: map[string]bool{}, dirEnrolled: map[string]bool{}, ownerAdded: map[string]bool{}, verifiedAdded: map[string]bool{}, attestedAdded: map[string]bool{}}
	if k, err := directoryclient.ImportOwnerKey(load("gj-owner-key")); err == nil {
		p.ownerSigning = k
	}
	if st, err := directoryclient.ParseWatchState(load("gj-watch-state")); err != nil {
		// Damaged, or saved by an older version: never use half of it. Forget it and say the next look is
		// a first look.
		store("gj-watch-state", "")
		p.dirAlerts = append(p.dirAlerts, "The saved record of what this device has seen of the key directory could not be read, so the next check starts from scratch.")
	} else {
		p.watch = st
	}
	p.pending = components.NewPending(func() { p.render() })
	p.asker = components.NewAsker(func() { p.render() })
	p.c = &client.Client{Token: func() string { return p.token }}
	seed := make([]byte, 32)
	buf := js.Global().Get("Uint8Array").New(32)
	js.Global().Get("crypto").Call("getRandomValues", buf)
	js.CopyBytesToGo(seed, buf)
	signer, err := encrypt.NewSigner(seed)
	clear(seed)
	if err != nil {
		return nil, err
	}
	p.store = docstore.NewMemoryAdapter()
	p.ring = &encrypt.StoreKeyring{Store: p.store, OwnerSigners: []ed25519.PublicKey{signer.Public()}}
	adapter := encrypt.New(p.store, p.ring)
	adapter.Signer = signer // frames are signed with the owner's key: a public key alone cannot forge one
	p.sharing = &encrypt.Sharing{Store: p.store, Adapter: adapter, Signer: signer}
	return p, nil
}

// refreshShared reads the document's list: the keys it is shared with, besides the owner's own.
func (p *page) refreshShared(ctx context.Context) error {
	list, err := p.sharing.List(ctx, docID)
	if err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	own := map[string]bool{}
	for _, o := range p.owner {
		own[o.Fingerprint] = true
	}
	p.shared = p.shared[:0]
	for _, s := range list {
		if own[s.Fingerprint] {
			continue
		}
		p.shared = append(p.shared, components.RecipientRow{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint, Verified: p.verifiedAdded[s.Fingerprint], Attested: p.attestedAdded[s.Fingerprint], OwnerSigned: p.ownerAdded[s.Fingerprint]})
	}
	return nil
}

func (p *page) ownerRows() []components.RecipientRow {
	var rows []components.RecipientRow
	for _, o := range p.owner {
		rows = append(rows, recipientRow(o, false, false))
	}
	return rows
}

func (p *page) addMyPasskey(ctx context.Context) error {
	if err := encrypt.CheckRecoverableOwner(p.owner, nil); err != nil {
		return err
	}
	id, secret, err := client.DeriveKey(ctx, "", encrypt.PRFSalt)
	if errors.Is(err, client.ErrCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = id // the credential ID would be a hint stored in clear in the list: left out, the browser asks
	r, err := encrypt.PRFRecipient(secret, "My passkey", "")
	clear(secret) // the secret is the root of the key: do not keep it around
	if err != nil {
		return err
	}
	// The owner is adding their own key: it is confirmed with its own fingerprint.
	if err := p.sharing.Add(ctx, docID, r, r.Fingerprint); err != nil {
		return err
	}
	p.owner = append(p.owner, r)
	row := recipientRow(r, false, false)
	p.myAdded = &row
	return p.refreshShared(ctx)
}

// createRecoveryKey makes a recovery key and returns its secret to be shown. The key is not an owner
// key yet: commitRecoveryKey adds it once the person confirmed they saved the secret, so a key whose
// secret was never seen can never count as a way back.
func (p *page) createRecoveryKey(ctx context.Context) (string, error) {
	pub, secret, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		return "", err
	}
	r, err := encrypt.ParseAgeRecipient(pub)
	if err != nil {
		return "", err
	}
	r.Label = "Recovery key"
	p.recoveryKey, p.recoveryAcked = &r, false
	return secret, nil
}

func (p *page) commitRecoveryKey(ctx context.Context) error {
	if p.recoveryKey == nil || !p.recoveryAcked {
		return errors.New("confirm that you saved the recovery key first")
	}
	r := *p.recoveryKey
	p.owner = append(p.owner, r)
	p.ring.Owners = append(p.ring.Owners, r.Key) // an owner key opens everything, whatever the list says
	p.recoveryKey, p.recoverySecret, p.recoveryAcked = nil, "", false
	return nil
}

// directory is the client of the server's key directory, signed in as the demo's provider account.
// It pins the log keys the first time (demo only: see /demo/log-keys).
func (p *page) directory(ctx context.Context) (*directoryclient.Client, error) {
	if p.dc != nil {
		return p.dc, nil
	}
	base := js.Global().Get("location").Get("origin").String()
	get := func(path string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return nil, err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(io.LimitReader(res.Body, 1<<16))
		if err != nil || res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %d", path, res.StatusCode)
		}
		return b, nil
	}
	tok, err := get("/demo/provider-session")
	if err != nil {
		return nil, fmt.Errorf("no provider session for the directory: %w", err)
	}
	raw, err := get("/demo/log-keys")
	if err != nil {
		return nil, err
	}
	var enc map[string]string
	if err := json.Unmarshal(raw, &enc); err != nil {
		return nil, err
	}
	keys := map[string]ed25519.PublicKey{}
	for kid, k := range enc {
		if b, err := base64.StdEncoding.DecodeString(k); err == nil && len(b) == ed25519.PublicKeySize {
			keys[kid] = b
		}
	}
	session := strings.TrimSpace(string(tok))
	p.dirToken = session
	p.dc = &directoryclient.Client{BaseURL: base, Token: func() string { return session }, LogKeys: keys, Pins: directoryclient.MemoryPins{}, State: &p.watch}
	return p.dc, nil
}

// lookup asks the server's directory through the client, which re-checks the owner signatures and
// attestations itself: a row's marks are what was proved, not what the server said.
func (p *page) lookup(ctx context.Context, email string) ([]components.DirRow, error) {
	c, err := p.directory(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := c.Lookup(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("the key directory: %w", err)
	}
	rows := make([]components.DirRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, components.DirRow{Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Key: k.Key, Verified: k.Verified,
			Attested: k.AttestationVerified, OwnerSigned: k.OwnerSigned, OwnerEnrolled: k.OwnerEnrolled})
	}
	return rows, nil
}

// passkeyIdentity asks for the passkey (one touch) and derives the age identity of its key; the
// recipient it makes must be the one that was added, or this is another passkey.
func (p *page) passkeyIdentity(ctx context.Context) (*encrypt.Recipient, age.Identity, error) {
	for _, o := range p.owner {
		if !o.IsPasskey() {
			continue
		}
		_, secret, err := client.DeriveKey(ctx, "", encrypt.PRFSalt)
		if err != nil {
			return nil, nil, err
		}
		id, err := encrypt.PRFIdentity(secret)
		clear(secret)
		if err != nil {
			return nil, nil, err
		}
		if id.Recipient().String() != o.Text {
			return nil, nil, errors.New("that is not the passkey that was added")
		}
		o := o
		return &o, id, nil
	}
	return nil, nil, errors.New("add your passkey first")
}

// publishMine publishes the passkey's key for my address: with proof of possession by the
// passkey's own identity and, when I have enrolled an owner key, the owner's signature.
func (p *page) publishMine(ctx context.Context) error {
	c, err := p.directory(ctx)
	if err != nil {
		return err
	}
	r, id, err := p.passkeyIdentity(ctx)
	if err != nil {
		return err
	}
	k, err := c.Publish(ctx, directoryclient.PublishRequest{Email: myAddress, Key: r.Text, Label: r.Label, Identity: id, Owner: p.ownerKey()})
	if err != nil {
		return err
	}
	p.dirStatus = "published " + k.Fingerprint
	return nil
}

// ownerKey is the owner signing key, nil before enrollment.
func (p *page) ownerKey() directoryclient.OwnerKey { return p.ownerSigning }

func (p *page) revokeMine(ctx context.Context) error {
	c, err := p.directory(ctx)
	if err != nil {
		return err
	}
	var fp string
	for _, o := range p.owner {
		if o.IsPasskey() {
			fp = o.Fingerprint
		}
	}
	if fp == "" {
		return errors.New("add your passkey first")
	}
	if _, err := c.Revoke(ctx, myAddress, fp, p.ownerKey()); err != nil {
		return err
	}
	p.dirStatus = "revoked " + fp
	return nil
}

// enrollOwner makes the owner key in this browser and registers it for my address.
func (p *page) enrollOwner(ctx context.Context) error {
	c, err := p.directory(ctx)
	if err != nil {
		return err
	}
	k, err := directoryclient.GenerateOwnerKey()
	if err != nil {
		return err
	}
	if _, err := c.Enroll(ctx, myAddress, k); err != nil {
		return err
	}
	p.ownerSigning = k
	store("gj-owner-key", directoryclient.ExportOwnerKey(k))
	p.dirStatus = "owner key enrolled"
	return nil
}

// watchMine fetches the log since the last position seen, checks it, and reports every entry for
// my address that I did not make.
func (p *page) watchMine(ctx context.Context) error {
	c, err := p.directory(ctx)
	if err != nil {
		return err
	}
	rep, err := c.WatchLog(ctx, myAddress, &p.watch, directoryclient.WatchOptions{Owner: p.ownerKey()})
	if err != nil {
		p.dirAlerts = append(p.dirAlerts, "the log cannot be trusted: "+err.Error())
		return nil
	}
	if b, err := json.Marshal(p.watch); err == nil {
		store("gj-watch-state", string(b))
	}
	for _, f := range rep.Foreign {
		p.dirAlerts = append(p.dirAlerts, fmt.Sprintf("%s (log entry %d)", f.Message, f.Entry.Seq))
	}
	for _, v := range rep.Violations {
		p.dirAlerts = append(p.dirAlerts, fmt.Sprintf("log entry %d breaks the owner rules: %s", v.Seq, v.Why))
	}
	p.dirStatus = fmt.Sprintf("log checked up to entry %d, %d new for my address", rep.Head.Seq, len(rep.Entries))
	return nil
}

// parsed is the draft as a row. Its Verified is the directory's answer for that fingerprint in
// the last lookup, never something the pasted text says.
func (p *page) parsed() *components.RecipientRow {
	if strings.TrimSpace(p.draft) == "" {
		return nil
	}
	r, err := parseKey(p.draft)
	if err != nil {
		p.parseErr = err.Error()
		return nil
	}
	p.parseErr = ""
	row := recipientRow(r, p.dirVerified[r.Fingerprint], p.dirAttested[r.Fingerprint])
	row.OwnerSigned, row.OwnerEnrolled = p.dirOwnerSigned[r.Fingerprint], p.dirEnrolled[r.Fingerprint]
	if row.Label == "" {
		row.Label = p.dirEmail
	}
	return &row
}

func (p *page) add(ctx context.Context, key, confirm string) error {
	r, err := parseKey(key)
	if err != nil {
		return err
	}
	if r.Label == "" {
		r.Label = strings.TrimSpace(p.dirEmail)
	}
	if err := p.sharing.Add(ctx, docID, r, confirm); err != nil {
		if errors.Is(err, encrypt.ErrFingerprint) {
			return errors.New("that is not the fingerprint of this key")
		}
		return err
	}
	p.verifiedAdded[r.Fingerprint] = p.dirVerified[r.Fingerprint]
	p.attestedAdded[r.Fingerprint] = p.dirAttested[r.Fingerprint]
	p.ownerAdded[r.Fingerprint] = p.dirOwnerSigned[r.Fingerprint]
	p.draft, p.confirm, p.parseErr = "", "", ""
	return p.refreshShared(ctx)
}

func (p *page) view() xango.Node {
	status := "no passkey yet"
	if p.token != "" {
		status = "signed in"
	}
	parsed := p.parsed()
	props := components.RecipientsProps{
		Owner: p.ownerRows(), Shared: p.shared,
		Draft: p.draft, Confirm: p.confirm, Parsed: parsed, ParseError: p.parseErr,
		OnDraft: func(k, c string) { p.draft, p.confirm = k, c; p.render() },
		Add:     p.add,
		Remove: func(ctx context.Context, fp string) (int, error) {
			return p.sharing.Remove(ctx, docID, fp)
		},
		OnRemoved: func(string, int) { p.notice = "" },
		Notice:    p.notice,
		Pending:   p.pending, Ask: p.asker.Ask,

		Directory:    p.lookup,
		DirEmail:     p.dirEmail,
		OnDirEmail:   func(e string) { p.dirEmail = e; p.render() },
		OnDirResults: p.onDirResults,
		DirResults:   p.dirRows, DirSearched: p.dirSearched,
		OnPick: func(r components.DirRow) { p.draft, p.confirm = r.Key, ""; p.render() },

		AddMyPasskey: p.addMyPasskey, PasskeySupported: client.Supported(), MyPasskeyAdded: p.myAdded,
		Confidential: true, SecondKeyMessage: encrypt.ErrNeedSecondRecipient.Error(),
		CreateRecoveryKey:     p.createRecoveryKey,
		OnRecoveryKey:         func(s string) { p.recoverySecret = s; p.render() },
		RecoverySecret:        p.recoverySecret,
		CommitRecoveryKey:     p.commitRecoveryKey,
		CancelRecoveryKey:     func() { p.recoveryKey, p.recoverySecret, p.recoveryAcked = nil, "", false; p.render() },
		RecoveryAcknowledged:  p.recoveryAcked,
		OnRecoveryAcknowledge: func(on bool) { p.recoveryAcked = on; p.render() },
		Copy: func(text string) {
			js.Global().Get("navigator").Get("clipboard").Call("writeText", text)
		},
	}
	makePasskey := xango.El("button", []xango.Attr{xango.A("id", "make-passkey"), xango.A("type", "button"), xango.On("click", func(xango.Event) {
		p.pending.Run(func(ctx context.Context) error {
			tok, err := p.c.Register(ctx, "ana@example.com", "", "Demo passkey")
			if errors.Is(err, client.ErrCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			p.token = tok
			return nil
		})
	})}, xango.Text("Create a passkey"))
	return xango.El("div", nil,
		xango.El("p", []xango.Attr{xango.A("id", "status")}, xango.Text(status)),
		makePasskey,
		components.Recipients(props),
		p.myEntry(),
		p.asker.Dialog())
}

// myEntry is the "my directory entry" section: publish, revoke, enroll an owner key, watch my
// address. It acts for the demo's pretend provider account.
func (p *page) myEntry() xango.Node {
	btn := func(id, text string, f func(context.Context) error) xango.Node {
		return xango.El("button", []xango.Attr{xango.A("id", id), xango.A("type", "button"), xango.On("click", func(xango.Event) {
			p.pending.Run(func(ctx context.Context) error {
				err := f(ctx)
				if errors.Is(err, client.ErrCancelled) {
					return nil
				}
				if err != nil {
					p.dirStatus = "failed: " + err.Error()
				}
				return err
			})
		})}, xango.Text(text))
	}
	var alerts []xango.Node
	for _, a := range p.dirAlerts {
		alerts = append(alerts, xango.El("li", []xango.Attr{xango.A("class", "dir-alert")}, xango.Text(a)))
	}
	enrolled := "no owner key"
	if p.ownerSigning != nil {
		enrolled = "owner key held in this browser"
	}
	return xango.El("section", []xango.Attr{xango.A("id", "my-directory")},
		xango.El("h2", nil, xango.Text("My directory entry ("+myAddress+")")),
		xango.El("p", []xango.Attr{xango.A("id", "dir-owner")}, xango.Text(enrolled)),
		btn("dir-publish", "Publish my passkey key", p.publishMine),
		btn("dir-revoke", "Revoke it", p.revokeMine),
		btn("dir-enroll", "Enroll an owner key", p.enrollOwner),
		btn("dir-watch", "Watch my address", p.watchMine),
		xango.El("p", []xango.Attr{xango.A("id", "dir-status"), xango.A("role", "status")}, xango.Text(p.dirStatus)),
		xango.El("ul", []xango.Attr{xango.A("id", "dir-alerts")}, alerts...))
}

// onDirResults keeps what the directory said: which fingerprints it vouches for.
func (p *page) onDirResults(email string, rows []components.DirRow) {
	p.dirRows, p.dirSearched = rows, true
	p.dirVerified, p.dirAttested, p.dirOwnerSigned, p.dirEnrolled = map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		if r.Verified {
			p.dirVerified[r.Fingerprint] = true
			p.dirAttested[r.Fingerprint] = r.Attested
			p.dirOwnerSigned[r.Fingerprint] = r.OwnerSigned
			p.dirEnrolled[r.Fingerprint] = r.OwnerEnrolled
		}
	}
}

func main() {
	p, err := newPage()
	if err != nil {
		js.Global().Get("document").Call("write", err.Error())
		return
	}
	var root *domrt.Root
	p.render = func() {
		node := p.view()
		container := js.Global().Get("document").Call("getElementById", "app")
		if root == nil {
			root = domrt.Mount(container, node)
		} else {
			root.Update(node)
		}
	}
	p.render()
	select {}
}
