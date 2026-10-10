// Package dirclient is the showcase's view of a docstore server's key directory
// (docstore.Server.WithDirectory). The routes, the proof of possession and the checks of what the
// server answers are directoryclient's; this package is the small interface the page uses and the
// failures it tells the person about, so the page does not depend on the client's shape. It has no
// browser code (net/http runs over fetch in the page), so it is tested natively against the demo
// server.
package dirclient

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/directoryclient"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// OwnerKey is the Ed25519 key that signs the entries of one address in the directory. Whoever holds
// it can publish and revoke for the address, so it is kept and backed up like a password.
type OwnerKey = directoryclient.OwnerKey

// GenerateOwnerKey makes a new owner key.
func GenerateOwnerKey() (OwnerKey, error) { return directoryclient.GenerateOwnerKey() }

// ExportOwnerKey is the key as text to keep (a backup); ImportOwnerKey reads it back.
func ExportOwnerKey(k OwnerKey) string { return directoryclient.ExportOwnerKey(k) }

// ImportOwnerKey reads what ExportOwnerKey wrote.
func ImportOwnerKey(s string) (OwnerKey, error) {
	return directoryclient.ImportOwnerKey(strings.TrimSpace(s))
}

// OwnerFingerprint is the short name of an owner key, what the person compares.
func OwnerFingerprint(k OwnerKey) string {
	return encrypt.OwnerFingerprint(k.Public().(ed25519.PublicKey))
}

// Enrollment is what the server says of an address's owner keys.
type Enrollment struct {
	// Enrolled: the address has an owner key.
	Enrolled bool
	// Fingerprints are the fingerprints of its owner keys.
	Fingerprints []string
}

// Key is a key the directory lists for an address, as the People tab's picker shows it.
type Key struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	// Verified is what the server answered: the key was published by an account whose e-mail the
	// identity provider verified. It is never inferred from anything the page typed.
	Verified bool `json:"verified"`
	// Held: whoever published the key opened a challenge sent to it (proof of possession).
	Held bool `json:"held"`
}

// Directory is what the page needs of a key directory.
type Directory interface {
	// Lookup lists the keys published for an e-mail address.
	Lookup(ctx context.Context, email string) ([]Key, error)
	// Publish publishes a public key for the signed-in account's own address.
	Publish(ctx context.Context, r PublishRequest) (Key, error)
	// Me is the e-mail address the server knows the signed-in account by (GET /auth/me): the
	// address an owner key is enrolled for.
	Me(ctx context.Context) (string, error)
	// Enrollment says whether the address has an owner key.
	Enrollment(ctx context.Context, email string) (Enrollment, error)
	// Enroll registers owner as the owner key of the signed-in account's address.
	Enroll(ctx context.Context, email string, owner OwnerKey) error
	// Pin tells the client that owner is the key somebody confirmed for the address (it enrolled
	// it, or the person restored it from a backup), so rows signed by it can be told from rows
	// signed by another.
	Pin(email string, owner OwnerKey)
}

// PublishRequest is a key to publish.
type PublishRequest struct {
	// Key is the public key text, Label what to call it.
	Key, Label string
	// Identity opens the server's proof-of-possession challenge for Key: the unlocked private
	// side. Nil sends no proof, and a server that requires one answers ErrProofRequired.
	Identity age.Identity
	// Email and Owner sign the entry with the address's owner key. An address that has one
	// refuses an entry without it (ErrOwnerRequired). Owner nil: no signature.
	Email string
	Owner OwnerKey
}

// The failures a page tells the person about.
var (
	// ErrNoDirectory: the server has no key directory (the routes are absent, or it is not a
	// docstore server).
	ErrNoDirectory = errors.New("this server has no key directory")
	// ErrSignInRequired: the server wants a signed-in provider account (a session token).
	ErrSignInRequired = errors.New("the key directory needs a signed-in account: set a session token")
	// ErrNotAllowed: the account may not do this (a passkey account cannot look up or publish; an
	// account whose e-mail is not verified cannot publish).
	ErrNotAllowed = errors.New("this account may not do that: the directory needs an account with a verified e-mail from an identity provider")
	// ErrRateLimited: too many requests.
	ErrRateLimited = errors.New("the directory is rate limited: try again in a minute")
	// ErrProofRequired: the server wants proof that the publisher holds the key, and none could be
	// given (the key is locked, or it is not one that can open a challenge).
	ErrProofRequired = errors.New("the directory wants proof that you hold this key: unlock it first")
	// ErrOwnerRequired: the address has an owner key and the server wants an entry signed by it.
	ErrOwnerRequired = errors.New("this address has an owner key: publishing needs an entry signed by it")
	// ErrAlreadyEnrolled: the address already has an owner key. If it is yours, restore it from its
	// backup; if not, somebody enrolled before you did (the log shows who and when).
	ErrAlreadyEnrolled = errors.New("this address already has an owner key")
	// ErrBadProof: the proof of possession did not verify.
	ErrBadProof = errors.New("the directory did not accept the proof that you hold this key")
)

// Client is a Directory over a docstore server's routes, through directoryclient.
type Client struct {
	// Base is the server's address ("https://docs.example.com", no trailing path).
	Base string
	// Token returns the bearer token of the signed-in account, "" for none. It is asked for each
	// request.
	Token func(ctx context.Context) (string, error)
	// HTTP is the HTTP client; nil is http.DefaultClient.
	HTTP *http.Client

	mu   sync.Mutex
	tok  string
	dc   *directoryclient.Client
	pins directoryclient.MemoryPins
}

// client is the directoryclient.Client for this call, its token set from Token. One is kept, so
// what it remembers between lookups lasts as long as this Client.
func (c *Client) client(ctx context.Context) (*directoryclient.Client, error) {
	var tok string
	if c.Token != nil {
		t, err := c.Token(ctx)
		if err != nil {
			return nil, err
		}
		tok = t
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tok = tok
	if c.dc == nil {
		c.dc = &directoryclient.Client{BaseURL: c.Base, HTTP: c.HTTP, Pins: c.pinsLocked(), Token: func() string {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.tok
		}}
	}
	return c.dc, nil
}

func (c *Client) pinsLocked() directoryclient.MemoryPins {
	if c.pins == nil {
		c.pins = directoryclient.MemoryPins{}
	}
	return c.pins
}

// Pin implements Directory.
func (c *Client) Pin(email string, owner OwnerKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pinsLocked().Set(email, encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey)))
}

// Me implements Directory: GET /auth/me.
func (c *Client) Me(ctx context.Context) (string, error) {
	var tok string
	if c.Token != nil {
		t, err := c.Token(ctx)
		if err != nil {
			return "", err
		}
		tok = t
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.Base, "/")+"/auth/me", nil)
	if err != nil {
		return "", err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return "", ErrNoDirectory
	case http.StatusUnauthorized:
		return "", ErrSignInRequired
	default:
		return "", errors.New("the server could not say who is signed in")
	}
	var u struct{ Email string }
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&u); err != nil {
		return "", err
	}
	if strings.TrimSpace(u.Email) == "" {
		return "", ErrNotAllowed
	}
	return strings.ToLower(strings.TrimSpace(u.Email)), nil
}

// Enrollment implements Directory.
func (c *Client) Enrollment(ctx context.Context, email string) (Enrollment, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	e, err := dc.Enrollment(ctx, email)
	if err != nil {
		return Enrollment{}, failure(err)
	}
	out := Enrollment{Enrolled: e.Enrolled}
	for _, k := range e.Keys {
		if pub, err := encrypt.ParseOwnerPub(k.Pub); err == nil {
			out.Fingerprints = append(out.Fingerprints, encrypt.OwnerFingerprint(pub))
		}
	}
	return out, nil
}

// Enroll implements Directory.
func (c *Client) Enroll(ctx context.Context, email string, owner OwnerKey) error {
	dc, err := c.client(ctx)
	if err != nil {
		return err
	}
	if _, err := dc.Enroll(ctx, email, owner); err != nil {
		return failure(err)
	}
	return nil
}

// failure puts the server's refusals in this package's sentinels (the page's words for them).
func failure(err error) error {
	var de *directoryclient.Error
	if !errors.As(err, &de) {
		return err
	}
	switch {
	case errors.Is(err, docstore.ErrDirectoryOwnerRequired):
		return ErrOwnerRequired
	case errors.Is(err, docstore.ErrDirectoryProofRequired):
		return ErrProofRequired
	case errors.Is(err, docstore.ErrDirectoryBadProof):
		return ErrBadProof
	case errors.Is(err, docstore.ErrDirectoryEnrolled):
		return ErrAlreadyEnrolled
	}
	switch de.Status {
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return ErrNoDirectory
	case http.StatusUnauthorized:
		return ErrSignInRequired
	case http.StatusForbidden:
		return errors.Join(ErrNotAllowed, err)
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return err
}

// Lookup implements Directory.
func (c *Client) Lookup(ctx context.Context, email string) ([]Key, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := dc.Lookup(ctx, email)
	if err != nil {
		return nil, failure(err)
	}
	out := make([]Key, 0, len(rows))
	for _, r := range rows {
		out = append(out, Key{Key: r.Key, Label: r.Label, Type: r.Type, Fingerprint: r.Fingerprint, Verified: r.Verified, Held: r.Held})
	}
	return out, nil
}

// Publish implements Directory.
func (c *Client) Publish(ctx context.Context, r PublishRequest) (Key, error) {
	dc, err := c.client(ctx)
	if err != nil {
		return Key{}, err
	}
	k, err := dc.Publish(ctx, directoryclient.PublishRequest{Email: r.Email, Key: r.Key, Label: r.Label, Identity: r.Identity, Owner: r.Owner})
	if err != nil {
		return Key{}, failure(err)
	}
	return Key{Key: k.Key, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified, Held: k.Held}, nil
}
