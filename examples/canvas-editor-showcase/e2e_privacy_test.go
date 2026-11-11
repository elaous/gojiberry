//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/e2etest"
	"github.com/elaous/gojiberry/telemetry"
)

type telemetrySite struct {
	*e2etest.Site
	mu       sync.Mutex
	requests int
	bodies   []string
	secrets  []string
	counters *telemetry.Counters
}

func newTelemetrySite(t *testing.T) *telemetrySite {
	t.Helper()
	ts := &telemetrySite{Site: e2etest.NewSite(t), counters: telemetry.NewCounters()}
	ts.UseLocalhost()
	h := docstore.NewServer(docstore.NewMemoryAdapter(), "memory").WithTelemetry(ts.counters).Handler()
	ts.Handle("/docs", h)
	ts.Handle("/telemetry", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ts.mu.Lock()
		ts.requests++
		ts.bodies = append(ts.bodies, string(b))
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			ts.secrets = append(ts.secrets, r.Method)
		}
		ts.mu.Unlock()
		r.Body = io.NopCloser(strings.NewReader(string(b)))
		h.ServeHTTP(w, r)
	}))
	return ts
}

func (ts *telemetrySite) count() int { ts.mu.Lock(); defer ts.mu.Unlock(); return ts.requests }

func confirmServer(d *e2etest.Tab) {
	d.Click(`button[data-tab="people"]`)
	d.WaitSelector("#dir-offer-review")
	d.Click("#dir-offer-review")
	confirmDialog(d)
}

func openWithServer(t *testing.T, ts *telemetrySite, opts ...string) *e2etest.Tab {
	d := ts.OpenURL(t, "/index.html?docstore="+url.QueryEscape(ts.URL))
	d.WaitFor("the page to start", "document.getElementById('state') && document.getElementById('state').textContent !== '(pending)'")
	for _, o := range opts {
		d.OnNewDocument(o)
	}
	return d
}

func TestOptedOutSendsNoRequestAtAll(t *testing.T) {
	ts := newTelemetrySite(t)
	d := openWithServer(t, ts)
	confirmServer(d)
	for _, tab := range []string{"find", "stats", "share", "privacy", "save", "format"} {
		d.Click(`button[data-tab="` + tab + `"]`)
	}
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#privacy-preview")
	d.OK(d.Checked("#telemetry-off") && !d.Checked("#telemetry-on"), "'Send nothing' is the default choice")
	d.OK(strings.Contains(d.Text("#privacy-preview"), "Nothing: you chose to send nothing"), "the preview says nothing is kept or sent: "+d.Text("#privacy-preview"))
	d.OK(d.Disabled("#privacy-send"), "there is nothing to send now")
	s := d.State()
	d.OK(!s.Bool("usage.consent") && !s.Bool("usage.pending"), "no count is kept while it is off")
	d.Sleep(1500)
	if n := ts.count(); n != 0 {
		t.Fatalf("opted out, but %d request(s) reached /telemetry", n)
	}
	d.Reload() // the choice (none) survives a reload
	d.Click(`button[data-tab="privacy"]`)
	d.OK(d.Checked("#telemetry-off"), "still off after a reload")
	d.Sleep(500)
	if n := ts.count(); n != 0 {
		t.Fatalf("after a reload, %d request(s) reached /telemetry", n)
	}
	d.NoErrors()
}

func TestOptInSendsExactlyThePreviewedJSON(t *testing.T) {
	ts := newTelemetrySite(t)
	d := openWithServer(t, ts)
	confirmServer(d)
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#privacy-preview")
	d.OK(strings.Contains(d.Text("#privacy-choice"), "Send anonymous counts to localhost"), "the choice names the server: "+d.Text("#privacy-choice"))

	d.Click("#telemetry-on")
	d.WaitFor("the preview to hold counts", `document.getElementById('privacy-preview').textContent.includes('"counts"')`)
	for _, tab := range []string{"find", "stats", "find", "privacy"} {
		d.Click(`button[data-tab="` + tab + `"]`)
	}
	d.WaitFor("the find count", `JSON.parse(document.getElementById('privacy-preview').textContent).counts.some(c => c.name === 'feature' && c.value === 'find' && c.n === 2)`)
	preview := d.Text("#privacy-preview")
	var shown telemetry.Batch
	if err := json.Unmarshal([]byte(preview), &shown); err != nil {
		t.Fatalf("the preview is not JSON: %v\n%s", err, preview)
	}
	if ts.count() != 0 {
		t.Fatalf("something was sent before the person pressed send: %d", ts.count())
	}
	d.OK(!d.Disabled("#privacy-send"), "send is possible once there is something to send")

	d.Click("#privacy-send")
	d.WaitFor("the counts to be sent", `JSON.parse(document.getElementById('state').textContent).usage.sent === 1`)
	if ts.count() != 1 {
		t.Fatalf("%d requests, want 1", ts.count())
	}
	var sent telemetry.Batch
	if err := json.Unmarshal([]byte(ts.bodies[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent, shown) {
		t.Fatalf("the request is not what was shown:\nshown %+v\nsent  %+v", shown, sent)
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(ts.bodies[0]), &raw)
	if len(raw) != 2 {
		t.Fatalf("the request has fields beyond day and counts: %s", ts.bodies[0])
	}
	for _, c := range sent.Counts {
		if !(telemetry.Event{Name: c.Name, Value: c.Value}).Valid() {
			t.Fatalf("a count outside the allowlist was sent: %+v", c)
		}
	}
	if len(ts.secrets) != 0 {
		t.Fatalf("a telemetry request carried a cookie or a token: %v", ts.secrets)
	}
	got := map[string]int{}
	for _, r := range ts.counters.Report(1) {
		got[string(r.Name)+"="+r.Value] = r.N
	}
	if got["feature=find"] != 2 || got["feature=stats"] != 1 {
		t.Fatalf("the server's totals: %v", got)
	}
	d.WaitFor("the empty preview", `document.getElementById('privacy-preview').textContent.startsWith('Nothing yet')`)
	d.OK(strings.Contains(d.Text("#privacy-message"), "Sent "), "the page says what was sent: "+d.Text("#privacy-message"))

	d.Click("#telemetry-off")
	d.WaitFor("off", `!JSON.parse(document.getElementById('state').textContent).usage.consent`)
	for _, tab := range []string{"find", "stats", "privacy"} {
		d.Click(`button[data-tab="` + tab + `"]`)
	}
	d.Sleep(1000)
	d.OK(d.Disabled("#privacy-send") && !d.State().Bool("usage.pending"), "nothing is kept after opting out")
	if ts.count() != 1 {
		t.Fatalf("a request was made after opting out: %d", ts.count())
	}
	d.Reload()
	d.Click(`button[data-tab="privacy"]`)
	d.OK(d.Checked("#telemetry-off"), "the opt-out survives a reload")
	d.NoErrors()
}

func TestGlobalPrivacyControlKeepsItOff(t *testing.T) {
	ts := newTelemetrySite(t)
	d := ts.OpenURL(t, "/index.html?docstore="+url.QueryEscape(ts.URL))
	d.WaitFor("the page to start", "document.getElementById('state') && document.getElementById('state').textContent !== '(pending)'")
	d.OnNewDocument(`Object.defineProperty(navigator, 'globalPrivacyControl', {value: true})`)
	d.Reload()
	confirmServer(d)
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#privacy-note")
	d.OK(strings.Contains(d.Text("#privacy-note"), "asks not to be tracked"), "the page explains: "+d.Text("#privacy-note"))
	d.OK(d.Disabled("#telemetry-on") && d.Disabled("#telemetry-off") && d.Checked("#telemetry-off"), "the choice is locked to 'send nothing'")
	d.OK(d.State().Bool("usage.blocked") && !d.State().Bool("usage.consent"), "the collector is blocked")
	d.Sleep(1000)
	if ts.count() != 0 {
		t.Fatalf("%d request(s) with Global Privacy Control set", ts.count())
	}
	d.NoErrors()
}

func TestPrivacyTabIsAccessible(t *testing.T) {
	ts := newTelemetrySite(t)
	d := openWithServer(t, ts)
	confirmServer(d)
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#privacy")
	d.OK(d.Attr("#privacy", "aria-labelledby") == "privacy-heading" && d.Text("#privacy-heading") == "Usage statistics", "a labelled section")
	d.OK(d.Count("#privacy-choice legend") == 1 && d.Count("#privacy-choice input[type=radio]") == 2, "a fieldset with a legend and two radio buttons")
	d.OK(d.Count(`label[for="telemetry-off"]`) == 1 && d.Count(`label[for="telemetry-on"]`) == 1, "each radio button has a label")
	d.OK(d.Attr("#privacy-preview", "tabindex") == "0", "the JSON can be reached with the keyboard")
	d.OK(d.Attr("#privacy-message", "role") == "status", "results are announced")
	d.OK(d.Count("#privacy-allowlist li") == len(telemetry.Allowlist()), "the allowlist is a real list of every name")
	d.Click("#telemetry-on")
	d.WaitFor("on", `JSON.parse(document.getElementById('state').textContent).usage.consent`)
	for _, v := range d.Axe("", axeDisabled...) {
		t.Errorf("the privacy tab: %s", v)
	}
	d.Focus("#telemetry-on")
	d.Press("ArrowUp")
	d.WaitFor("off by keyboard", `!JSON.parse(document.getElementById('state').textContent).usage.consent`)
	d.OK(d.Checked("#telemetry-off"), "the arrow key chose 'send nothing'")
	d.NoErrors()
}
