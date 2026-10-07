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
// in this browser (localStorage). The calls go through dirclient.Directory, so they follow the API
// when it changes.
//
// What the directory is given, and what it can say, makes its address a trust decision, so a link
// does not make it: a page opened with ?docstore=https://host only OFFERS the address, and nothing
// is sent to it until the person has confirmed it in a dialog that names the host. The address must
// be https (http only for a host on this machine), it is confirmed again whenever it changes, a
// session token belongs to one address and is dropped when the address changes (the token field is
// a password field), and every "verified" mark says which host it is that vouches for it.

const (
	dirURLKey      = "showcase-enc:docstore"
	dirOKKey       = "showcase-enc:docstore-ok"   // the address the person confirmed
	dirTokenKey    = "showcase-enc:docstoken"     // the session token
	dirTokenURLKey = "showcase-enc:docstoken-for" // the address the token was given for
)

type dirUI struct {
	url, token string // the confirmed directory and its token: the only address anything is sent to
	urlDraft   string
	tokenDraft string
	dir        dirclient.Directory
	// offer is an address that was put forward (by a link, or found in storage without the
	// person's confirmation) and waits for the person to confirm it; refused says why one was not
	// even offered.
	offer, refused string
	// ask is the page's confirmation dialog (Asker.AskWith).
	ask func(label, message string, do func())

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
	stored, ok := lsGet(dirURLKey), lsGet(dirOKKey)
	if stored != "" {
		switch clean, _, err := checkDirURL(stored); {
		case err != nil:
			d.refused = T("dir.refused", "The key directory address %q was not used: %s", stored, err.Error())
			lsSet(dirURLKey, "")
		case ok != clean:
			d.offer = clean // saved, never confirmed here: asked about like a new one
		default:
			d.url = clean
		}
	}
	// The token belongs to the address it was given for.
	d.token = lsGet(dirTokenKey)
	if d.token != "" && (d.url == "" || lsGet(dirTokenURLKey) != d.url) {
		d.token = ""
		lsSet(dirTokenKey, "")
		lsSet(dirTokenURLKey, "")
	}
	if q := js.Global().Get("location").Get("search").String(); q != "" {
		if v, err := url.ParseQuery(strings.TrimPrefix(q, "?")); err == nil {
			if u := strings.TrimSpace(v.Get("docstore")); u != "" {
				// Never applied from a link: offered, and the person decides.
				if clean, _, err := checkDirURL(u); err != nil {
					d.refused = T("dir.refused", "The key directory address %q was not used: %s", u, err.Error())
				} else if clean != d.url {
					d.offer = clean
				}
			}
		}
	}
	d.urlDraft = d.url
	d.connect()
	return d
}

// host is the host of the confirmed directory, which every verified mark names.
func (d *dirUI) host() string {
	if _, h, err := checkDirURL(d.url); err == nil {
		return h
	}
	return ""
}

// verifiedBy is the host to put on a verified mark (none for a mark that is not one).
func (d *dirUI) verifiedBy(verified bool) string {
	if verified {
		return d.host()
	}
	return ""
}

// use makes clean the directory (the person confirmed it). A new address drops the session token:
// it was given for the old one. The same address keeps it.
func (d *dirUI) use(clean, token string) {
	if clean != d.url {
		d.token = ""
	}
	if token != "" {
		d.token = token
	}
	d.url, d.offer, d.refused = clean, "", ""
	d.urlDraft, d.tokenDraft = clean, ""
	d.demoToken, d.rows, d.searched, d.notice = "", nil, false, ""
	d.verified, d.verifiedAdded = map[string]bool{}, map[string]bool{}
	lsSet(dirURLKey, clean)
	lsSet(dirOKKey, clean)
	lsSet(dirTokenKey, d.token)
	if d.token != "" {
		lsSet(dirTokenURLKey, clean)
	} else {
		lsSet(dirTokenURLKey, "")
	}
	d.connect()
}

// clear forgets the directory and its token.
func (d *dirUI) clear() {
	d.url, d.token, d.offer, d.refused = "", "", "", ""
	d.urlDraft, d.tokenDraft, d.demoToken, d.rows, d.searched, d.notice = "", "", "", nil, false, ""
	d.verified, d.verifiedAdded = map[string]bool{}, map[string]bool{}
	for _, k := range []string{dirURLKey, dirOKKey, dirTokenKey, dirTokenURLKey} {
		lsSet(k, "")
	}
	d.connect()
}

// confirm asks about an address before it is used: the dialog names the host, and says what the
// directory is given.
func (d *dirUI) confirm(clean, token string, e *encUI) {
	_, host, _ := checkDirURL(clean)
	do := func() { d.use(clean, token); e.render() }
	if d.ask == nil {
		return
	}
	d.ask(T("dir.useHost", "Use %s", host),
		T("dir.confirmUse", "Use %s as the key directory? It is sent the e-mail addresses you look up and your session token, and the keys it answers with are shown as verified by it. Only use a server you trust: an address you were sent in a link is not a reason to.", host), do)
	e.render()
}

func (d *dirUI) connect() {
	d.dir = nil
	if _, _, err := checkDirURL(d.url); err == nil {
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
			by := ""
			if k.Verified {
				by = d.host()
			}
			rows = append(rows, components.DirRow{Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Key: k.Key, Verified: k.Verified, VerifiedBy: by})
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
	input := func(id, typ, label, value string, on func(string)) xango.Node {
		return xango.El("input", []xango.Attr{xango.A("type", typ), xango.A("id", id), xango.A("value", value), xango.A("aria-label", label), xango.A("placeholder", label),
			xango.A("autocomplete", "off"), xango.A("style", "width:100%;box-sizing:border-box;font-size:12px;margin:2px 0"),
			xango.On("input", func(ev xango.Event) { on(xango.EventValue[string](ev)) })})
	}
	nodes := []xango.Node{para("dir-status", status, false)}
	if d.refused != "" {
		nodes = append(nodes, para("dir-refused", d.refused, true))
	}
	if d.offer != "" {
		_, host, _ := checkDirURL(d.offer)
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "dir-offer"), xango.A("style", "border:1px solid #d97706;border-radius:6px;padding:8px;margin:6px 0")},
			para("dir-offer-text", T("dir.offer", "This page was opened with, or has saved, the key directory address %s. Nothing has been sent to it. Use it only if you trust %s.", d.offer, host), false),
			xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px")},
				btn("dir-offer-review", T("dir.offerReview", "Review and use %s…", host), func() { d.confirm(d.offer, "", e) }, false),
				btn("dir-offer-ignore", T("dir.offerIgnore", "Ignore it"), func() {
					d.offer = ""
					if lsGet(dirURLKey) != "" && lsGet(dirOKKey) != lsGet(dirURLKey) {
						lsSet(dirURLKey, "")
					}
					e.render()
				}, false))))
	}
	nodes = append(nodes,
		input("dir-url", "text", T("dir.urlLabel", "Docstore server address (https://…)"), d.urlDraft, func(v string) { d.urlDraft = v }),
		input("dir-token", "password", tokenLabel(d), d.tokenDraft, func(v string) { d.tokenDraft = v }),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px")},
			btn("dir-save", T("dir.save", "Use this key directory"), func() { d.save(e) }, false),
			btn("dir-clear", T("dir.clear", "Stop using a key directory"), func() { d.clear(); e.render() }, d.url == "" && d.offer == "")))
	return []xango.Node{xango.El("div", []xango.Attr{xango.A("id", "dir-settings"), xango.A("style", "margin-top:1em;border-top:1px solid #d1d5db;padding-top:8px")}, nodes...)}
}

func tokenLabel(d *dirUI) string {
	if d.token != "" {
		return T("dir.tokenSet", "A session token is set for this server (type a new one to replace it)")
	}
	return T("dir.tokenLabel", "Session token, if the server needs one")
}

// save applies what was typed: an unchanged address only changes the token; a new one is checked
// and then confirmed in a dialog that names its host before anything uses it.
func (d *dirUI) save(e *encUI) {
	raw := strings.TrimSpace(d.urlDraft)
	if raw == "" {
		d.clear()
		e.render()
		return
	}
	clean, _, err := checkDirURL(raw)
	if err != nil {
		d.refused = T("dir.refused", "The key directory address %q was not used: %s", raw, err.Error())
		e.render()
		return
	}
	d.refused = ""
	token := strings.TrimSpace(d.tokenDraft)
	if clean == d.url {
		d.use(clean, token)
		e.render()
		return
	}
	d.confirm(clean, "", e) // a token typed for another address is dropped: it is given once the address is confirmed
}
