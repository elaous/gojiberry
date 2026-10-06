//go:build js && wasm

// Package client is the browser side of auth/passkey: it asks the server for a challenge,
// has the browser's authenticator answer it (navigator.credentials.create and .get), and posts
// the answer back. It uses the browser's own JSON helpers (PublicKeyCredential.parse...FromJSON
// and toJSON), so there is no hand-written conversion of binary fields; a browser without them
// is reported as not supported.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"syscall/js"
)

// ErrAlreadyRegistered is returned when the authenticator already holds a passkey for the account.
var ErrAlreadyRegistered = errors.New("this device already has a passkey for this account; use another device or another security key")

// ErrCancelled is returned when the person closed the browser's passkey prompt.
var ErrCancelled = errors.New("passkey: cancelled")

// Supported says whether this browser can make and use passkeys here (a secure context with
// WebAuthn and its JSON helpers).
func Supported() bool {
	pk := js.Global().Get("PublicKeyCredential")
	return pk.Truthy() && pk.Get("parseCreationOptionsFromJSON").Truthy() && pk.Get("parseRequestOptionsFromJSON").Truthy() &&
		js.Global().Get("navigator").Get("credentials").Truthy()
}

// Row is one passkey of the signed-in person.
type Row struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Created  string `json:"created"`
	LastUsed string `json:"lastUsed"`
	Synced   bool   `json:"synced"`
}

// Client talks to a server mounting auth/passkey's routes.
type Client struct {
	// Base is the server's address ("" for the page's own origin).
	Base string
	// Token returns the bearer token of the signed-in person, or "".
	Token func() string
}

type jsErr struct{ name, msg string }

func (e *jsErr) Error() string { return e.name + ": " + e.msg }

func await(ctx context.Context, p js.Value) (js.Value, error) {
	type res struct {
		v   js.Value
		err error
	}
	ch := make(chan res, 1)
	var then, fail js.Func
	rel := func() { then.Release(); fail.Release() }
	then = js.FuncOf(func(_ js.Value, a []js.Value) any {
		rel()
		v := js.Undefined()
		if len(a) > 0 {
			v = a[0]
		}
		ch <- res{v: v}
		return nil
	})
	fail = js.FuncOf(func(_ js.Value, a []js.Value) any {
		rel()
		e := &jsErr{name: "Error", msg: "rejected"}
		if len(a) > 0 && a[0].Truthy() {
			if n := a[0].Get("name"); n.Truthy() {
				e.name = n.String()
			}
			if m := a[0].Get("message"); m.Truthy() {
				e.msg = m.String()
			}
		}
		ch <- res{err: e}
		return nil
	})
	p.Call("then", then, fail)
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		return js.Undefined(), ctx.Err()
	}
}

func (c *Client) call(ctx context.Context, method, path, query string, body []byte) (int, []byte, error) {
	opts := js.Global().Get("Object").New()
	opts.Set("method", method)
	h := js.Global().Get("Object").New()
	if tok := c.token(); tok != "" {
		h.Set("Authorization", "Bearer "+tok)
	}
	h.Set("Content-Type", "application/json")
	opts.Set("headers", h)
	if body != nil {
		opts.Set("body", string(body))
	}
	u := strings.TrimRight(c.Base, "/") + path
	if query != "" {
		u += "?" + query
	}
	resp, err := await(ctx, js.Global().Call("fetch", u, opts))
	if err != nil {
		return 0, nil, err
	}
	text, err := await(ctx, resp.Call("text"))
	if err != nil {
		return 0, nil, err
	}
	return resp.Get("status").Int(), []byte(text.String()), nil
}

func (c *Client) token() string {
	if c.Token == nil {
		return ""
	}
	return c.Token()
}

func serverError(status int, body []byte) error {
	var e struct{ Error string }
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return errors.New(e.Error)
	}
	return fmt.Errorf("passkey: the server answered %d", status)
}

// ceremony runs one begin/answer/finish exchange: begin is the route that starts it, answer
// turns the options (a JS object) into the authenticator's answer, finish is the route that
// takes it, with query.
func (c *Client) ceremony(ctx context.Context, begin string, beginBody []byte, finish string, extra url.Values, create bool) ([]byte, error) {
	status, body, err := c.call(ctx, "POST", begin, "", beginBody)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, serverError(status, body)
	}
	var started struct {
		Options json.RawMessage `json:"options"`
		Token   string          `json:"token"`
	}
	if err := json.Unmarshal(body, &started); err != nil {
		return nil, err
	}
	var pub struct {
		PublicKey json.RawMessage `json:"publicKey"`
	}
	if err := json.Unmarshal(started.Options, &pub); err != nil {
		return nil, err
	}
	pk := js.Global().Get("PublicKeyCredential")
	jsonOpts := js.Global().Get("JSON").Call("parse", string(pub.PublicKey))
	opts := js.Global().Get("Object").New()
	var cred js.Value
	if create {
		opts.Set("publicKey", pk.Call("parseCreationOptionsFromJSON", jsonOpts))
		cred, err = await(ctx, js.Global().Get("navigator").Get("credentials").Call("create", opts))
	} else {
		opts.Set("publicKey", pk.Call("parseRequestOptionsFromJSON", jsonOpts))
		cred, err = await(ctx, js.Global().Get("navigator").Get("credentials").Call("get", opts))
	}
	if err != nil {
		var je *jsErr
		if errors.As(err, &je) && je.name == "InvalidStateError" {
			return nil, ErrAlreadyRegistered
		}
		if errors.As(err, &je) && (je.name == "NotAllowedError" || je.name == "AbortError") {
			return nil, fmt.Errorf("%w (%s)", ErrCancelled, je)
		}
		return nil, err
	}
	answer := js.Global().Get("JSON").Call("stringify", cred.Call("toJSON")).String()
	extra.Set("token", started.Token)
	status, body, err = c.call(ctx, "POST", finish, extra.Encode(), []byte(answer))
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, serverError(status, body)
	}
	return body, nil
}

// Register makes a passkey. Signed in (Token returns a token) it adds one to the person's
// account, linking it to the identity they are signed in as; otherwise it makes a new account for
// email, and the bearer token of the new session is returned.
func (c *Client) Register(ctx context.Context, email, name, label string) (authToken string, err error) {
	body, _ := json.Marshal(map[string]string{"email": email, "name": name})
	out, err := c.ceremony(ctx, "/auth/passkey/register/begin", body, "/auth/passkey/register/finish", url.Values{"label": {label}}, true)
	if err != nil {
		return "", err
	}
	var fin struct {
		AuthToken string `json:"auth_token"`
	}
	_ = json.Unmarshal(out, &fin)
	return fin.AuthToken, nil
}

// SignIn signs in with a passkey the browser offers for this site (no user name to type) and
// returns the bearer token of the session.
func (c *Client) SignIn(ctx context.Context) (authToken string, err error) {
	out, err := c.ceremony(ctx, "/auth/passkey/login/begin", nil, "/auth/passkey/login/finish", url.Values{}, false)
	if err != nil {
		return "", err
	}
	var fin struct {
		AuthToken string `json:"auth_token"`
	}
	if err := json.Unmarshal(out, &fin); err != nil || fin.AuthToken == "" {
		return "", errors.New("passkey: the server gave no session")
	}
	return fin.AuthToken, nil
}

// List returns the signed-in person's passkeys.
func (c *Client) List(ctx context.Context) ([]Row, error) {
	status, body, err := c.call(ctx, "GET", "/auth/passkey/credentials", "", nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, serverError(status, body)
	}
	var rows []Row
	return rows, json.Unmarshal(body, &rows)
}

// Remove deletes one of the person's passkeys; the last one stays unless there is another way in.
func (c *Client) Remove(ctx context.Context, id string) error {
	status, body, err := c.call(ctx, "DELETE", "/auth/passkey/credentials/"+url.PathEscape(id), "", nil)
	if err != nil {
		return err
	}
	if status != http204 {
		return serverError(status, body)
	}
	return nil
}

const http204 = 204
