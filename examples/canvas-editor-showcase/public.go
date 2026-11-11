//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore/public/client"
	"github.com/elaous/gojiberry/xango"
)

const (
	publicKey   = "showcase:public" // localStorage: the token and what is known of the document
	publicDelay = 4 * time.Second
	publicTick  = 20 * time.Second // how often the countdown is drawn again
	publicMax   = 8 * time.Second
)

type publicUI struct {
	u      *ui
	render func()
	cl     *client.Client

	mu       sync.Mutex
	doc      client.Doc // what the server said of the document
	has      bool       // the session has a live public document
	viewing  bool
	viewID   string
	busy     bool
	err      string
	note     string
	timer    *time.Timer
	reported bool

	reportOpen   bool
	reportCat    string
	reportReason string
}

func newPublic(u *ui, renderUI func()) *publicUI {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	base := ""
	if q.Call("has", "publicapi").Bool() {
		base = pageEndpoint(strings.TrimSpace(q.Call("get", "publicapi").String()))
	}
	if base == "" {
		base = js.Global().Get("location").Get("origin").String()
	}
	p := &publicUI{u: u, render: renderUI, cl: &client.Client{BaseURL: base}, reportCat: client.CategorySpam}
	p.restore()
	go func() {
		for range time.Tick(publicTick) {
			p.mu.Lock()
			live := p.has || p.viewing
			p.mu.Unlock()
			if live {
				p.render()
			}
		}
	}()
	return p
}

type publicSaved struct {
	Token   string    `json:"token"`
	ViewID  string    `json:"view,omitempty"`
	Expires time.Time `json:"expires,omitempty"`
}

func publicStore(f func(js.Value)) {
	defer func() { _ = recover() }()
	if ls := js.Global().Get("localStorage"); !ls.IsUndefined() && !ls.IsNull() {
		f(ls)
	}
}

func (p *publicUI) restore() {
	publicStore(func(ls js.Value) {
		v := ls.Call("getItem", publicKey)
		if v.IsNull() || v.IsUndefined() {
			return
		}
		var s publicSaved
		if json.Unmarshal([]byte(v.String()), &s) != nil || s.Token == "" {
			return
		}
		p.cl.Token = s.Token
		if s.Expires.After(time.Now()) { // a deadline that has passed means the document is gone
			p.doc.ViewID, p.doc.Expires, p.has = s.ViewID, s.Expires, true
		}
	})
}

func (p *publicUI) remember() {
	publicStore(func(ls js.Value) {
		if p.cl.Token == "" {
			ls.Call("removeItem", publicKey)
			return
		}
		b, _ := json.Marshal(publicSaved{Token: p.cl.Token, ViewID: p.doc.ViewID, Expires: p.doc.Expires})
		ls.Call("setItem", publicKey, string(b))
	})
}

func (p *publicUI) forget() {
	p.cl.Token = ""
	p.has, p.doc = false, client.Doc{}
	p.remember()
}

func (p *publicUI) bytesOfDoc() ([]byte, error) { return docfile.Open.Marshal(p.u.current()) }

func (p *publicUI) words(err error) string {
	var e *client.Error
	wait := ""
	if errors.As(err, &e) && e.RetryAfter > 0 {
		wait = client.Countdown(e.RetryAfter)
	}
	switch {
	case errors.Is(err, client.ErrTooLarge):
		return T("public.err.tooLarge", "The document is too large for the public offer.")
	case errors.Is(err, client.ErrRateLimited):
		if wait != "" {
			return T("public.err.rateWait", "Too many requests. Try again in %s.", wait)
		}
		return T("public.err.rate", "Too many requests, or today's allowance is used up. Try again later.")
	case errors.Is(err, client.ErrBusy):
		return T("public.err.busy", "The public offer is busy or full. Try again later.")
	case errors.Is(err, client.ErrGone):
		return T("public.err.gone", "This document was taken down.")
	case errors.Is(err, client.ErrRefused):
		return T("public.err.refused", "The public offer does not take this content.")
	case errors.Is(err, client.ErrNotFound), errors.Is(err, client.ErrForbidden):
		return T("public.err.expired", "The public copy is gone or expired.")
	}
	return T("public.err.other", "The public offer could not be reached: %s", err.Error())
}

func (p *publicUI) run(f func(ctx context.Context) error) {
	p.mu.Lock()
	if p.busy {
		p.mu.Unlock()
		return
	}
	p.busy, p.err, p.note = true, "", ""
	p.mu.Unlock()
	p.render()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		err := f(ctx)
		p.mu.Lock()
		p.busy = false
		if err != nil {
			p.err = p.words(err)
			if errors.Is(err, client.ErrNotFound) || errors.Is(err, client.ErrGone) {
				p.forget()
			}
		}
		p.mu.Unlock()
		p.render()
	}()
}

func (p *publicUI) publish() {
	p.run(func(ctx context.Context) error { return p.push(ctx, true) })
}

func (p *publicUI) push(ctx context.Context, fresh bool) error {
	data, err := p.bytesOfDoc()
	if err != nil {
		return err
	}
	p.mu.Lock()
	has := p.has
	p.mu.Unlock()
	if has {
		exp, err := p.cl.Save(ctx, data)
		if err != nil {
			return err
		}
		p.mu.Lock()
		p.doc.Expires = exp
		p.mu.Unlock()
		p.remember()
		if fresh {
			p.note = T("public.updated", "The public copy is up to date.")
		}
		return nil
	}
	if p.cl.Token == "" {
		if err := p.cl.NewSession(ctx); err != nil {
			return err
		}
	}
	d, err := p.cl.Create(ctx, data)
	if errors.Is(err, client.ErrForbidden) { // the stored session ended: start again
		p.cl.Token = ""
		if err = p.cl.NewSession(ctx); err == nil {
			d, err = p.cl.Create(ctx, data)
		}
	}
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.doc, p.has = d, true
	p.mu.Unlock()
	p.remember()
	p.note = T("public.published", "A copy is on the public offer now. Share one of the links below.")
	return nil
}

func (p *publicUI) edited() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.has || p.viewing {
		return
	}
	if p.timer != nil {
		p.timer.Reset(publicDelay)
		return
	}
	p.timer = time.AfterFunc(publicDelay, func() {
		p.mu.Lock()
		p.timer = nil
		busy := p.busy
		p.mu.Unlock()
		if busy {
			p.edited()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), publicMax)
		defer cancel()
		if err := p.push(ctx, false); err != nil {
			p.mu.Lock()
			p.err = p.words(err)
			p.mu.Unlock()
			if errors.Is(err, client.ErrNotFound) {
				p.forget()
			}
		}
		p.render()
	})
}

func (p *publicUI) discard() {
	p.run(func(ctx context.Context) error {
		if err := p.cl.Discard(ctx); err != nil && !errors.Is(err, client.ErrNotFound) {
			return err
		}
		p.mu.Lock()
		p.forget()
		p.note = T("public.discarded", "The public copy was deleted.")
		p.mu.Unlock()
		return nil
	})
}

func (p *publicUI) keep() {
	p.u.tab = "save"
	p.u.saver.Pending.Run(p.u.saver.Save)
	p.note = T("public.kept", "The document is saved in the store chosen in the Save tab. Choose a store you control there to keep it for good.")
	p.render()
}

func (p *publicUI) openLink(ready func() bool) {
	kind, val := client.ParseLink(js.Global().Get("location").Get("hash").String())
	if kind == "" {
		return
	}
	setNoindex()
	for range 100 { // the editor is drawn a moment after the page starts
		if ready() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch kind {
	case "view":
		data, exp, err := p.cl.View(ctx, val)
		if err != nil {
			p.mu.Lock()
			p.u.tab, p.err = "public", p.words(err)
			p.mu.Unlock()
			p.render()
			return
		}
		doc, err := decodePublic(data)
		if err != nil {
			p.mu.Lock()
			p.u.tab, p.err = "public", T("public.err.unreadable", "The shared document could not be read.")
			p.mu.Unlock()
			p.render()
			return
		}
		p.mu.Lock()
		p.viewing, p.viewID, p.doc.Expires = true, val, exp
		p.u.tab = "public"
		p.mu.Unlock()
		p.u.load(doc)
		p.u.setRole(canvas.RoleViewer)
	case "edit":
		p.cl.Token = val
		data, d, err := p.cl.Download(ctx)
		if err != nil {
			p.cl.Token = ""
			p.mu.Lock()
			p.u.tab, p.err = "public", p.words(err)
			p.mu.Unlock()
			p.render()
			return
		}
		doc, err := decodePublic(data)
		if err != nil {
			p.mu.Lock()
			p.u.tab, p.err = "public", T("public.err.unreadable", "The shared document could not be read.")
			p.mu.Unlock()
			p.render()
			return
		}
		p.mu.Lock()
		p.doc, p.has = d, true
		p.u.tab = "public"
		p.mu.Unlock()
		p.remember()
		p.u.load(doc)
	}
	// the token does not stay in the address bar or history
	js.Global().Get("history").Call("replaceState", nil, "", js.Global().Get("location").Get("pathname").String()+js.Global().Get("location").Get("search").String())
	p.render()
}

func decodePublic(data []byte) (docfile.Document, error) {
	if d, err := docfile.Open.Unmarshal(data); err == nil {
		return d, nil
	}
	return docfile.JSON.Unmarshal(data)
}

func setNoindex() {
	doc := js.Global().Get("document")
	m := doc.Call("createElement", "meta")
	m.Set("name", "robots")
	m.Set("content", "noindex, nofollow, noarchive")
	doc.Get("head").Call("appendChild", m)
}

func (p *publicUI) report() {
	p.run(func(ctx context.Context) error {
		p.mu.Lock()
		id, cat, reason := p.viewID, p.reportCat, p.reportReason
		p.mu.Unlock()
		if err := p.cl.Report(ctx, id, cat, reason); err != nil {
			return err
		}
		p.mu.Lock()
		p.reported, p.reportOpen, p.reportReason = true, false, ""
		p.mu.Unlock()
		return nil
	})
}

func publicPanel(p *publicUI) xango.Node {
	p.mu.Lock()
	defer p.mu.Unlock()
	nodes := []xango.Node{xango.El("h4", []xango.Attr{xango.A("id", "public-heading")}, xango.Text(T("public.heading", "Share with a public link")))}
	if p.viewing {
		nodes = append(nodes, p.viewerNodes()...)
	} else {
		nodes = append(nodes, p.ownerNodes()...)
	}
	if p.err != "" {
		nodes = append(nodes, para("public-error", p.err, true))
	}
	if p.note != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "public-note"), xango.A("role", "status"), xango.A("style", "font-size:13px")}, xango.Text(p.note)))
	}
	if t := p.cl.Terms; t != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("style", "font-size:13px")},
			xango.El("a", []xango.Attr{xango.A("id", "public-terms"), xango.A("href", t), xango.A("target", "_blank"), xango.A("rel", "noopener noreferrer")}, xango.Text(T("public.terms", "Terms of the public offer")))))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "public-panel")}, nodes...)
}

func (p *publicUI) countdown() xango.Node {
	left := client.Remaining(p.doc.Expires, time.Now())
	text := T("public.countdown", "Deleted in %s unless edited.", client.Countdown(left))
	attrs := []xango.Attr{xango.A("id", "public-countdown"), xango.A("style", "font-size:13px;margin:0.4em 0")}
	if left <= 0 {
		text = T("public.gone", "The public copy is gone.")
	} else if client.Warn(left) {
		text = T("public.countdownWarn", "Deleted in %s unless edited. Download it or keep it now.", client.Countdown(left))
		attrs = append(attrs, xango.A("role", "alert"), xango.A("class", "gj-doc-error"), xango.A("data-warn", "true"))
	} else {
		attrs = append(attrs, xango.A("role", "timer"))
	}
	return xango.El("p", attrs, xango.Text(text))
}

func (p *publicUI) ownerNodes() []xango.Node {
	nodes := []xango.Node{xango.El("p", []xango.Attr{xango.A("id", "public-intro"), xango.A("style", "font-size:13px")},
		xango.Text(T("public.intro", "No account. This puts a copy of the document on a public server for 24 hours. Every edit you make while this page is open renews the 24 hours; reading it does not. After 24 hours without an edit it is deleted for good.")))}
	if !p.has {
		return append(nodes, btn("public-publish", T("public.publish", "Publish a copy"), p.publish, p.busy))
	}
	edit := client.EditLink(pageBase(), p.cl.Token)
	view := client.ViewLink(pageBase(), p.doc.ViewID)
	linkRow := func(id, label, value string) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("style", "margin:6px 0")},
			xango.El("label", []xango.Attr{xango.A("for", id), xango.A("style", "display:block;font-size:13px")}, xango.Text(label)),
			xango.El("input", []xango.Attr{xango.A("id", id), xango.A("readonly", "readonly"), xango.A("style", "width:100%"), xango.A("value", value)}),
			btn(id+"-copy", T("public.copy", "Copy"), func() { p.u.saver.Copy(value) }, false))
	}
	nodes = append(nodes, p.countdown(),
		linkRow("public-edit", T("public.editLink", "Edit link: anyone who has it can change the document"), edit),
		linkRow("public-view", T("public.viewLink", "View link: read only, and reading does not renew it"), view),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin:6px 0")},
			btn("public-update", T("public.update", "Update the copy now"), p.publish, p.busy),
			btn("public-download", T("public.download", "Download in another format…"), func() { p.u.tab = "transfer"; p.render() }, false),
			btn("public-keep", T("public.keep", "Keep this document"), p.keep, false),
			btn("public-discard", T("public.discard", "Delete the public copy now"), p.discard, p.busy)),
		xango.El("p", []xango.Attr{xango.A("id", "public-keep-hint"), xango.A("style", "font-size:13px;color:#6b7280")},
			xango.Text(T("public.keepHint", "Keep this document saves it in the store chosen in the Save tab, which you control. Sign in to a store there to keep it for good."))))
	return nodes
}

func (p *publicUI) viewerNodes() []xango.Node {
	nodes := []xango.Node{
		xango.El("p", []xango.Attr{xango.A("id", "public-viewer"), xango.A("style", "font-size:13px")},
			xango.Text(T("public.viewBanner", "You are reading a document shared with a public link. It is read only, and it is deleted when its author has not edited it for 24 hours."))),
		p.countdown(),
	}
	if p.reported {
		return append(nodes, xango.El("p", []xango.Attr{xango.A("id", "public-reported"), xango.A("role", "status")}, xango.Text(T("public.reportSent", "Thank you. Your report was sent."))))
	}
	if !p.reportOpen {
		return append(nodes, btn("public-report-open", T("public.report", "Report this document"), func() { p.mu.Lock(); p.reportOpen = true; p.mu.Unlock(); p.render() }, false))
	}
	cats := [][2]string{
		{client.CategorySpam, T("public.cat.spam", "Spam")},
		{client.CategoryIllegal, T("public.cat.illegal", "Illegal content")},
		{client.CategoryCSAM, T("public.cat.csam", "Sexual content involving children")},
		{client.CategoryOther, T("public.cat.other", "Something else")},
	}
	return append(nodes, xango.El("fieldset", []xango.Attr{xango.A("id", "public-report-form"), xango.A("style", "border:0;padding:0;margin:6px 0")},
		xango.El("legend", nil, xango.Text(T("public.report", "Report this document"))),
		selectOf("public-report-cat", T("public.reportCategory", "Reason"), p.reportCat, cats, func(v string) { p.mu.Lock(); p.reportCat = v; p.mu.Unlock() }),
		xango.El("label", []xango.Attr{xango.A("for", "public-report-reason"), xango.A("style", "display:block;font-size:13px")}, xango.Text(T("public.reportDetails", "Details (optional)"))),
		xango.El("textarea", []xango.Attr{xango.A("id", "public-report-reason"), xango.A("rows", "3"), xango.A("maxlength", "500"), xango.A("style", "width:100%;box-sizing:border-box"), xango.A("value", p.reportReason),
			xango.On("input", func(e xango.Event) { p.mu.Lock(); p.reportReason = xango.EventValue[string](e); p.mu.Unlock() })}),
		btn("public-report-send", T("public.reportSend", "Send report"), p.report, p.busy)))
}
