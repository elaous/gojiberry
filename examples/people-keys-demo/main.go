//go:build js && wasm

// Command people-keys-demo is the host code the People tab's key controls need: the
// components.Recipients props wired to the encryption library and the store's key directory.
//
//   - AddMyPasskey: client.DeriveKey (the passkey's PRF output) -> encrypt.PRFRecipient ->
//     encrypt.Sharing.Add. No fingerprint is typed: it is the owner's own key.
//   - CreateRecoveryKey: encrypt.GenerateRecoveryKey; the public part becomes an owner key and
//     the secret is shown once.
//   - Directory: GET /directory/lookup on the docstore server. A row's Verified mark comes from
//     what the server answered, and from nothing else: a pasted key is "label only" however it
//     is worded.
//
// The document is in memory and the owner's signing key is made for the page; a real host keeps
// the document in its store (see docs/encryption-and-passkeys.md). Serve it with cmd/server.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/auth/passkey/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
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
	verifiedAdded  map[string]bool // keys added from a verified row
	recoverySecret string
	myAdded        *components.RecipientRow
	notice         string

	pending *components.Pending
	asker   *components.Asker
	c       *client.Client
}

func recipientRow(r encrypt.Recipient, verified bool) components.RecipientRow {
	return components.RecipientRow{Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Verified: verified}
}

func parseKey(text string) (encrypt.Recipient, error) {
	if strings.HasPrefix(strings.TrimSpace(text), "age1") {
		return encrypt.ParseAgeRecipient(text)
	}
	return encrypt.ParseSSHRecipient(text)
}

func newPage() (*page, error) {
	p := &page{dirVerified: map[string]bool{}, verifiedAdded: map[string]bool{}}
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
	p.sharing = &encrypt.Sharing{Store: p.store, Adapter: encrypt.New(p.store, p.ring), Signer: signer}
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
		p.shared = append(p.shared, components.RecipientRow{Label: s.Label, Type: s.Type, Fingerprint: s.Fingerprint, Verified: p.verifiedAdded[s.Fingerprint]})
	}
	return nil
}

func (p *page) ownerRows() []components.RecipientRow {
	var rows []components.RecipientRow
	for _, o := range p.owner {
		rows = append(rows, recipientRow(o, false))
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
	row := recipientRow(r, false)
	p.myAdded = &row
	return p.refreshShared(ctx)
}

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
	p.owner = append(p.owner, r)
	p.ring.Owners = append(p.ring.Owners, r.Key) // an owner key opens everything, whatever the list says
	return secret, nil
}

// lookup asks the server's directory; Verified is whatever it answered.
func (p *page) lookup(ctx context.Context, email string) ([]components.DirRow, error) {
	base := js.Global().Get("location").Get("origin").String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/directory/lookup?email="+url.QueryEscape(email), nil)
	if err != nil {
		return nil, err
	}
	if p.dirToken == "" {
		pr, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/demo/provider-session", nil)
		if err != nil {
			return nil, err
		}
		pres, err := http.DefaultClient.Do(pr)
		if err != nil {
			return nil, err
		}
		tb, err := io.ReadAll(io.LimitReader(pres.Body, 1<<10))
		pres.Body.Close()
		if err != nil || pres.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("no provider session for the directory (%d)", pres.StatusCode)
		}
		p.dirToken = strings.TrimSpace(string(tb))
	}
	req.Header.Set("Authorization", "Bearer "+p.dirToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the key directory answered %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Keys []struct {
			Key, Label, Type, Fingerprint string
			Verified                      bool
		}
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	rows := make([]components.DirRow, 0, len(out.Keys))
	for _, k := range out.Keys {
		rows = append(rows, components.DirRow{Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Key: k.Key, Verified: k.Verified})
	}
	return rows, nil
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
	row := recipientRow(r, p.dirVerified[r.Fingerprint])
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
		CreateRecoveryKey: p.createRecoveryKey,
		OnRecoveryKey:     func(s string) { p.recoverySecret = s; p.render() },
		RecoverySecret:    p.recoverySecret,
		OnRecoveryDone:    func() { p.recoverySecret = ""; p.render() },
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
		p.asker.Dialog())
}

// onDirResults keeps what the directory said: which fingerprints it vouches for.
func (p *page) onDirResults(email string, rows []components.DirRow) {
	p.dirRows, p.dirSearched = rows, true
	p.dirVerified = map[string]bool{}
	for _, r := range rows {
		if r.Verified {
			p.dirVerified[r.Fingerprint] = true
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
