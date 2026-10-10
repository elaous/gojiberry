// Package dirclient is the showcase's view of a docstore server's key directory
// (docstore.Server.WithDirectory). The routes, the proof of possession and the checks of what the
// server answers are directoryclient's; this package is the small interface the page uses and the
// failures it tells the person about, so the page does not depend on the client's shape. It has no
// browser code (net/http runs over fetch in the page), so it is tested natively against the demo
// server.
package dirclient

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/directoryclient"
)

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
}

// PublishRequest is a key to publish.
type PublishRequest struct {
	// Key is the public key text, Label what to call it.
	Key, Label string
	// Identity opens the server's proof-of-possession challenge for Key: the unlocked private
	// side. Nil sends no proof, and a server that requires one answers ErrProofRequired.
	Identity age.Identity
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

	mu  sync.Mutex
	tok string
	dc  *directoryclient.Client
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
		c.dc = &directoryclient.Client{BaseURL: c.Base, HTTP: c.HTTP, Token: func() string {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.tok
		}}
	}
	return c.dc, nil
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
	k, err := dc.Publish(ctx, directoryclient.PublishRequest{Key: r.Key, Label: r.Label, Identity: r.Identity})
	if err != nil {
		return Key{}, failure(err)
	}
	return Key{Key: k.Key, Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Verified: k.Verified, Held: k.Held}, nil
}
