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

const (
	dirURLKey      = "showcase-enc:docstore"
	dirOKKey       = "showcase-enc:docstore-ok"   // the address the person confirmed
	dirTokenKey    = "showcase-enc:docstoken"     // the session token
	dirTokenURLKey = "showcase-enc:docstoken-for" // the address the token was given for
	dirRequireKey  = "showcase-enc:require-owner"
)

type dirUI struct {
	url, token     string
	urlDraft       string
	requireOwner   bool
	tokenDraft     string
	dir            dirclient.Directory
	offer, refused string
	ask            func(label, message string, do func())
	render         func()
	changed        func()

	email             string
	rows              []components.DirRow
	searched          bool
	verified          map[string]bool
	verifiedAdded     map[string]bool
	marks, marksAdded map[string]dirMarks
	keys              []dirclient.Key
	keysEmail         string
	notice            string

	owner ownerUI // the owner key of my address (encowner.go)

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
	d := dirUI{verified: map[string]bool{}, verifiedAdded: map[string]bool{}, marks: map[string]dirMarks{}, marksAdded: map[string]dirMarks{}}
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
	d.requireOwner = lsGet(dirRequireKey) == "1"
	d.token = lsGet(dirTokenKey)
	if d.token != "" && (d.url == "" || lsGet(dirTokenURLKey) != d.url) {
		d.token = ""
		lsSet(dirTokenKey, "")
		lsSet(dirTokenURLKey, "")
	}
	if q := js.Global().Get("location").Get("search").String(); q != "" {
		if v, err := url.ParseQuery(strings.TrimPrefix(q, "?")); err == nil {
			if u := strings.TrimSpace(v.Get("docstore")); u != "" {
				// never applied from a link: offered, and the person decides
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

func (d *dirUI) setRequireOwner(on bool, e *encUI) {
	d.requireOwner = on
	if on {
		lsSet(dirRequireKey, "1")
	} else {
		lsSet(dirRequireKey, "")
	}
	d.rows, d.searched = nil, false // the last answer was read under the other setting
	d.connect()
	e.render()
}

func (d *dirUI) notifyChanged() {
	if d.changed != nil {
		d.changed()
	}
}

func (d *dirUI) host() string {
	if _, h, err := checkDirURL(d.url); err == nil {
		return h
	}
	return ""
}

func (d *dirUI) verifiedBy(verified bool) string {
	if verified {
		return d.host()
	}
	return ""
}

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
	d.marks, d.marksAdded, d.keys = map[string]dirMarks{}, map[string]dirMarks{}, nil
	lsSet(dirURLKey, clean)
	lsSet(dirOKKey, clean)
	lsSet(dirTokenKey, d.token)
	if d.token != "" {
		lsSet(dirTokenURLKey, clean)
	} else {
		lsSet(dirTokenURLKey, "")
	}
	d.connect()
	d.notifyChanged()
}

func (d *dirUI) clear() {
	d.url, d.token, d.offer, d.refused = "", "", "", ""
	d.urlDraft, d.tokenDraft, d.demoToken, d.rows, d.searched, d.notice = "", "", "", nil, false, ""
	d.verified, d.verifiedAdded = map[string]bool{}, map[string]bool{}
	d.marks, d.marksAdded, d.keys = map[string]dirMarks{}, map[string]dirMarks{}, nil
	for _, k := range []string{dirURLKey, dirOKKey, dirTokenKey, dirTokenURLKey} {
		lsSet(k, "")
	}
	d.connect()
	d.notifyChanged()
}

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
		c := &dirclient.Client{Base: d.url, Token: d.sessionToken, RequireOwner: d.requireOwner}
		c.OnState = func(st string) { lsSet(watchStateKey+d.url, st) }
		d.dir = c
	}
	d.loadOwner()
	d.loadWatch()
}

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
		return "", nil
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

func dirErr(err error) error {
	var msg string
	switch {
	case errors.Is(err, dirclient.ErrNoDirectory):
		msg = T("dir.err.none", "That server has no key directory (its directory routes are absent), so keys cannot be looked up or published there.")
	case errors.Is(err, dirclient.ErrSignInRequired):
		msg = T("dir.err.signIn", "The key directory needs a signed-in account: set a session token below.")
	case errors.Is(err, dirclient.ErrNotAllowed):
		msg = T("dir.err.notAllowed", "This account may not do that: the directory needs an account with a verified e-mail from an identity provider (a passkey account cannot look up or publish).")
	case errors.Is(err, dirclient.ErrProofRequired):
		msg = T("dir.err.proof", "The directory wants proof that you hold this key, and it is locked: unlock it (with its passkey or key file) and publish again.")
	case errors.Is(err, dirclient.ErrBadProof):
		msg = T("dir.err.badProof", "The directory did not accept the proof that you hold this key.")
	case errors.Is(err, dirclient.ErrOwnerRequired):
		msg = T("dir.err.owner", "This address has an owner key, so the directory only takes entries signed by it. Make or restore its owner key in the settings below.")
	case errors.Is(err, dirclient.ErrRateLimited):
		msg = T("dir.err.rate", "The key directory is rate limited: try again in a minute.")
	default:
		return tErr(err)
	}
	return &shownError{msg: msg, err: err}
}

type dirMarks struct{ Attested, OwnerSigned, OwnerEnrolled bool }

func (d *dirUI) dirRows(keys []dirclient.Key) []components.DirRow {
	rows := make([]components.DirRow, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, components.DirRow{Label: k.Label, Type: k.Type, Fingerprint: k.Fingerprint, Key: k.Key, Verified: k.Verified,
			VerifiedBy: d.verifiedBy(k.Verified), Attested: k.Attested, OwnerSigned: k.OwnerSigned, OwnerEnrolled: k.OwnerEnrolled, Note: seqNote(k)})
	}
	return rows
}

func (d *dirUI) decorate(r *components.RecipientRow, added bool) {
	v, m := d.verified[r.Fingerprint], d.marks[r.Fingerprint]
	if added {
		v, m = d.verifiedAdded[r.Fingerprint], d.marksAdded[r.Fingerprint]
	}
	r.Verified, r.VerifiedBy = v, d.verifiedBy(v)
	r.Attested, r.OwnerSigned, r.OwnerEnrolled = v && m.Attested, v && m.OwnerSigned, v && m.OwnerEnrolled
}

func (d *dirUI) wire(props *components.RecipientsProps, e *encUI) {
	if d.dir == nil {
		return
	}
	props.Directory = func(ctx context.Context, email string) ([]components.DirRow, error) {
		keys, err := d.dir.Lookup(ctx, email)
		unenrolled := errors.Is(err, dirclient.ErrUnenrolled)
		if err != nil && !unenrolled {
			return nil, dirErr(err)
		}
		if unenrolled {
			for i := range keys {
				if !keys[i].OwnerEnrolled {
					keys[i].Verified = false
				}
			}
		}
		d.keys, d.keysEmail = keys, email
		rows := d.dirRows(keys)
		if unenrolled {
			for i := range rows {
				if rows[i].Note == "" {
					rows[i].Note = T("dir.unenrolledNote", "This address has no owner key, so only the server's word ties this key to it. Confirm the fingerprint another way (in person, or by a call) before adding it.")
				}
			}
		}
		return rows, nil
	}
	props.DirEmail = d.email
	props.OnDirEmail = func(v string) { d.email = v; e.render() }
	props.OnDirResults = func(email string, rows []components.DirRow) {
		d.rows, d.searched = rows, true
		d.verified, d.marks = map[string]bool{}, map[string]dirMarks{}
		for _, r := range rows {
			if r.Verified {
				d.verified[r.Fingerprint] = true
			}
			d.marks[r.Fingerprint] = dirMarks{Attested: r.Attested, OwnerSigned: r.OwnerSigned, OwnerEnrolled: r.OwnerEnrolled}
		}
	}
	props.DirResults, props.DirSearched = d.rows, d.searched
	props.OnPick = func(r components.DirRow) { e.draft, e.confirm = r.Key, ""; e.render() }
	props.Publish = func(ctx context.Context) error { return d.publish(ctx, e) }
	props.PublishNotice = d.notice
}

// never a recovery key: it stays offline
func (d *dirUI) publish(ctx context.Context, e *encUI) error {
	d.notice = ""
	n := 0
	for _, o := range e.store.Owners() {
		if o.Kind == encstore.KindRecovery {
			continue
		}
		id, _ := e.store.IdentityFor(o.Public)
		req := dirclient.PublishRequest{Key: o.Public, Label: o.Label, Identity: id, Email: d.owner.email, Owner: d.owner.key}
		if _, err := d.dir.Publish(ctx, req); err != nil {
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
	nodes = append(nodes, components.Checkbox(components.CheckboxProps{
		Label:    T("dir.requireOwner", "Do not count a key as verified when its address has no owner key (then confirm its fingerprint another way)"),
		Checked:  d.requireOwner,
		OnChange: func(on bool) { d.setRequireOwner(on, e) },
	}))
	if d.dir != nil {
		if encTarget != "server" {
			nodes = append(nodes, para("dir-server-store", T("dir.serverStore", "Documents can also be saved to this server (Save tab, Save to). The server can read them."), false))
		}
		nodes = append(nodes, d.locationNodes(e)...)
	}
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
	nodes = append(nodes, d.confirmPanel(e)...)
	nodes = append(nodes, d.ownerPanel(e)...)
	return []xango.Node{xango.El("div", []xango.Attr{xango.A("id", "dir-settings"), xango.A("style", "margin-top:1em;border-top:1px solid #d1d5db;padding-top:8px")}, nodes...)}
}

func tokenLabel(d *dirUI) string {
	if d.token != "" {
		return T("dir.tokenSet", "A session token is set for this server (type a new one to replace it)")
	}
	return T("dir.tokenLabel", "Session token, if the server needs one")
}

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
	d.confirm(clean, "", e)
}

func seqNote(k dirclient.Key) string {
	if !k.SeqChanged {
		return ""
	}
	return T("dir.seqChanged", "The directory now names another log entry for this key than before. Nothing signs that number for an address without an owner key: it may be a republication, or the server naming another entry.")
}
