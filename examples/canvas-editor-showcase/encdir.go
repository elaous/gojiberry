//go:build js && wasm

package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/dirclient"
	"github.com/elaous/gojiberry/examples/canvas-editor-showcase/encstore"
	"github.com/elaous/gojiberry/xango"
)

// The key directory of a docstore server, for the People tab: find a person by e-mail (the
// verified or label-only mark comes from what the server answered) and "Publish my keys". The page
// has no sign-in of its own, so the server and, if it needs one, a session token are settings kept
// in this browser (localStorage). A page opened with ?docstore=https://host sets the server. The
// calls go through dirclient.Directory, so they follow the API when it changes.

const (
	dirURLKey   = "showcase-enc:docstore"
	dirTokenKey = "showcase-enc:docstoken"
)

type dirUI struct {
	url, token string
	urlDraft   string
	tokenDraft string
	dir        dirclient.Directory

	email    string
	rows     []components.DirRow
	searched bool
	// verified is what the directory said of each fingerprint in the last lookup; verifiedAdded of
	// the keys that were added from a verified row. Never set from what was pasted.
	verified      map[string]bool
	verifiedAdded map[string]bool
	notice        string

	demoToken string // from the server's /demo/provider-session, for the demo server only
}

func lsGet(key string) string {
	defer func() { _ = recover() }()
	v := js.Global().Get("localStorage").Call("getItem", key)
	if v.IsNull() || v.IsUndefined() {
		return ""
	}
	return v.String()
}

func lsSet(key, val string) {
	defer func() { _ = recover() }()
	ls := js.Global().Get("localStorage")
	if val == "" {
		ls.Call("removeItem", key)
		return
	}
	ls.Call("setItem", key, val)
}

func newDirUI() dirUI {
	d := dirUI{verified: map[string]bool{}, verifiedAdded: map[string]bool{}}
	d.url, d.token = lsGet(dirURLKey), lsGet(dirTokenKey)
	if q := js.Global().Get("location").Get("search").String(); q != "" {
		if v, err := url.ParseQuery(strings.TrimPrefix(q, "?")); err == nil {
			if u := strings.TrimSpace(v.Get("docstore")); u != "" {
				d.url = u
				lsSet(dirURLKey, u)
			}
		}
	}
	d.urlDraft, d.tokenDraft = d.url, d.token
	d.connect()
	return d
}

func (d *dirUI) connect() {
	d.dir = nil
	if strings.TrimSpace(d.url) != "" {
		d.dir = &dirclient.HTTP{Base: d.url, Token: d.sessionToken}
	}
}

// sessionToken is the token the person set, or, for the demo server (it hands one out at
// /demo/provider-session; a real server has no such route), that one.
func (d *dirUI) sessionToken(ctx context.Context) (string, error) {
	if d.token != "" {
		return d.token, nil
	}
	if d.demoToken != "" {
		return d.demoToken, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.url, "/")+"/demo/provider-session", nil)
	if err != nil {
		return "", nil
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil // not a demo server: no token, and the directory says sign in
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", nil
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<10))
	if err != nil {
		return "", nil
	}
	d.demoToken = strings.TrimSpace(string(b))
	return d.demoToken, nil
}

// dirErr puts the directory's failures in the interface's language.
func dirErr(err error) error {
	var msg string
	switch {
	case errors.Is(err, dirclient.ErrNoDirectory):
		msg = T("dir.err.none", "That server has no key directory (its directory routes are absent), so keys cannot be looked up or published there.")
	case errors.Is(err, dirclient.ErrSignInRequired):
		msg = T("dir.err.signIn", "The key directory needs a signed-in account: set a session token below.")
	case errors.Is(err, dirclient.ErrNotAllowed):
		msg = T("dir.err.notAllowed", "This account may not do that: the directory needs an account with a verified e-mail from an identity provider (a passkey account cannot look up or publish).")
	case errors.Is(err, dirclient.ErrRateLimited):
		msg = T("dir.err.rate", "The key directory is rate limited: try again in a minute.")
	default:
		return tErr(err)
	}
	return &shownError{msg: msg, err: err}
}

// wire plugs the directory into the Recipients props: the picker, and Publish.
func (d *dirUI) wire(props *components.RecipientsProps, e *encUI) {
	if d.dir == nil {
		return
	}
	props.Directory = func(ctx context.Context, email string) ([]components.DirRow, error) {
		keys, err := d.dir.Lookup(ctx, email)
		if err != nil {
			return nil, dirErr(err)
		}
		rows := make([]components.DirRow, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, components.DirRow{Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Key: k.Key, Verified: k.Verified})
		}
		return rows, nil
	}
	props.DirEmail = d.email
	props.OnDirEmail = func(v string) { d.email = v; e.render() }
	props.OnDirResults = func(email string, rows []components.DirRow) {
		d.rows, d.searched = rows, true
		d.verified = map[string]bool{}
		for _, r := range rows {
			if r.Verified {
				d.verified[r.Fingerprint] = true
			}
		}
	}
	props.DirResults, props.DirSearched = d.rows, d.searched
	props.OnPick = func(r components.DirRow) { e.draft, e.confirm = r.Key, ""; e.render() }
	props.Publish = func(ctx context.Context) error { return d.publish(ctx, e) }
	props.PublishNotice = d.notice
}

// publish publishes the owner's keys that are meant to be found (a passkey's and an SSH key's;
// never a recovery key, which stays offline) for the signed-in account's address.
func (d *dirUI) publish(ctx context.Context, e *encUI) error {
	d.notice = ""
	n := 0
	for _, o := range e.store.Owners() {
		if o.Kind == encstore.KindRecovery {
			continue
		}
		if _, err := d.dir.Publish(ctx, o.Public, o.Label); err != nil {
			return dirErr(err)
		}
		n++
	}
	if n == 0 {
		d.notice = T("dir.nothingToPublish", "There is no key to publish: recovery keys are never published.")
		return nil
	}
	d.notice = T("dir.published", "%d key(s) published for your address. Others who look you up see them marked verified.", n)
	return nil
}

// settingsPanel is where the directory server and token are set.
func (d *dirUI) settingsPanel(e *encUI) []xango.Node {
	status := T("dir.unset", "No key directory is set: pick people by pasting their keys. Set a docstore server to find them by e-mail and to publish your keys.")
	if d.dir != nil {
		status = T("dir.using", "Key directory: %s", d.url)
	}
	input := func(id, label, value string, on func(string)) xango.Node {
		return xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("id", id), xango.A("value", value), xango.A("aria-label", label), xango.A("placeholder", label),
			xango.A("autocomplete", "off"), xango.A("style", "width:100%;box-sizing:border-box;font-size:12px;margin:2px 0"),
			xango.On("input", func(ev xango.Event) { on(xango.EventValue[string](ev)) })})
	}
	return []xango.Node{xango.El("div", []xango.Attr{xango.A("id", "dir-settings"), xango.A("style", "margin-top:1em;border-top:1px solid #d1d5db;padding-top:8px")},
		para("dir-status", status, false),
		input("dir-url", T("dir.urlLabel", "Docstore server address (https://…)"), d.urlDraft, func(v string) { d.urlDraft = v }),
		input("dir-token", T("dir.tokenLabel", "Session token, if the server needs one"), d.tokenDraft, func(v string) { d.tokenDraft = v }),
		btn("dir-save", T("dir.save", "Use this key directory"), func() {
			d.url, d.token = strings.TrimSpace(d.urlDraft), strings.TrimSpace(d.tokenDraft)
			d.demoToken, d.rows, d.searched, d.notice = "", nil, false, ""
			d.verified = map[string]bool{}
			lsSet(dirURLKey, d.url)
			lsSet(dirTokenKey, d.token)
			d.connect()
			e.render()
		}, false))}
}
