// Package dirclient is the showcase's client for a docstore server's key directory
// (GET /directory/lookup, POST /directory/publish: docstore.Server.WithDirectory). It is behind a
// small interface so the page does not depend on the routes' shape: when the API changes (a proof
// of possession for publishing is planned), only this package follows. It has no browser code
// (net/http runs over fetch in the page), so it is tested natively against the demo server.
package dirclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
}

// Directory is what the page needs of a key directory.
type Directory interface {
	// Lookup lists the keys published for an e-mail address.
	Lookup(ctx context.Context, email string) ([]Key, error)
	// Publish publishes a public key (its text) with a label for the signed-in account's own
	// address.
	Publish(ctx context.Context, key, label string) (Key, error)
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
)

// HTTP is a Directory over a docstore server's routes.
type HTTP struct {
	// Base is the server's address ("https://docs.example.com", no trailing path).
	Base string
	// Token returns the bearer token of the signed-in account, "" for none. It is asked for each
	// request.
	Token func(ctx context.Context) (string, error)
	// Client is the HTTP client; nil is http.DefaultClient.
	Client *http.Client
}

func (h *HTTP) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.Base, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.Token != nil {
		tok, err := h.Token(ctx)
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	c := h.Client
	if c == nil {
		c = http.DefaultClient
	}
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusOK:
		return data, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, ErrNoDirectory
	case http.StatusUnauthorized:
		return nil, ErrSignInRequired
	case http.StatusForbidden:
		return nil, fmt.Errorf("%w (%s)", ErrNotAllowed, strings.TrimSpace(string(data)))
	case http.StatusTooManyRequests:
		return nil, ErrRateLimited
	}
	return nil, fmt.Errorf("the key directory answered %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
}

// Lookup implements Directory: GET /directory/lookup?email=.
func (h *HTTP) Lookup(ctx context.Context, email string) ([]Key, error) {
	data, err := h.do(ctx, http.MethodGet, "/directory/lookup?email="+url.QueryEscape(email), nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []Key `json:"keys"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("the key directory answered something that is not a key list: %w", err)
	}
	return out.Keys, nil
}

// Publish implements Directory: POST /directory/publish {"key", "label"}. The server takes the
// address from the signed-in account, so none is sent.
func (h *HTTP) Publish(ctx context.Context, key, label string) (Key, error) {
	body, _ := json.Marshal(map[string]string{"key": key, "label": label})
	data, err := h.do(ctx, http.MethodPost, "/directory/publish", body)
	if err != nil {
		return Key{}, err
	}
	var k Key
	if err := json.Unmarshal(data, &k); err != nil {
		return Key{}, err
	}
	return k, nil
}
