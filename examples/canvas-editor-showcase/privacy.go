//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/telemetry"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// Usage statistics (roadmap 57). The default is to send nothing: the collector keeps no count while the
// person has not opted in, and sends only to the docstore server they confirmed (never anywhere else).
// The Privacy tab shows what would be sent as the exact JSON before it is, and what could ever be sent.

const (
	telemetryKey = "showcase:telemetry"        // "on" once the person opted in
	flushEvery   = 60 * time.Second            // an opted-in page sends its counts at most this often
	sendTimeout  = 15 * time.Second            // a send that takes longer is given up (the counts are kept)
	sampleKey    = "showcase:telemetry-sample" // "1", "10" or "100": send from one visit in so many
	crashKey     = "showcase:crash"            // "on" once the person agreed to send crash codes (a choice apart from the counts)
	crashPending = "showcase:crash-pending"    // codes of crashes since the last visit, written by the page's own handler (index.html) only while crashKey is on
	telemetryOn  = "on"
	telemetryOff = "off"
)

// sampleRates are the choices of how many visits send (one in n); the first is every visit.
var sampleRates = []int{1, 10, 100}

func sampleLabel(n int) string {
	switch n {
	case 10:
		return T("privacy.sample10", "About 1 visit in 10")
	case 100:
		return T("privacy.sample100", "About 1 visit in 100")
	}
	return T("privacy.sample1", "Every visit")
}

// privacyUI is the state of the Privacy tab and the collector behind it.
type privacyUI struct {
	col      *telemetry.Collector
	started  time.Time
	lastTab  string
	endpoint func() string // the address counts go to ("" until a server is confirmed)
	host     func() string
	render   func()
	message  string
}

// newPrivacy builds the collector with consent off, restores the person's earlier choice and applies
// what the browser asks for (Do Not Track, Global Privacy Control).
func newPrivacy(tab string, dir *dirUI) *privacyUI {
	p := &privacyUI{col: telemetry.NewCollector(nil), started: time.Now(), lastTab: tab}
	p.endpoint = func() string {
		if dir.url == "" {
			return ""
		}
		return strings.TrimRight(dir.url, "/") + "/telemetry"
	}
	p.host = dir.host
	if n, err := strconv.Atoi(lsGet(sampleKey)); err == nil && slices.Contains(sampleRates, n) {
		p.col.SetSampling(n, nil)
	}
	nav := js.Global().Get("navigator")
	p.col.BrowserSignals(dntOn(nav.Get("doNotTrack").String()), nav.Get("globalPrivacyControl").Truthy())
	if lsGet(telemetryKey) == telemetryOn && !p.col.Blocked() {
		_ = p.col.SetConsent(true)
		p.recordSession()
	}
	if lsGet(crashKey) == telemetryOn && !p.col.Blocked() {
		_ = p.col.SetCrashConsent(true)
	}
	p.drainCrashes()
	return p
}

// drainCrashes takes the crash codes the page's handler kept (a crash that ended the program cannot send them
// itself), counts those on the allowlist if the person agreed, and clears them either way.
func (p *privacyUI) drainCrashes() {
	raw := lsGet(crashPending)
	if raw == "" {
		return
	}
	lsSet(crashPending, "")
	var codes []string
	if json.Unmarshal([]byte(raw), &codes) != nil {
		return
	}
	for _, c := range codes {
		_ = p.col.Record(telemetry.Crash, c) // a code outside the allowlist, or with no agreement, is dropped
	}
}

// chooseCrash applies the person's choice about crash codes, apart from the usage counts.
func (p *privacyUI) chooseCrash(on bool, u *ui) {
	if on {
		if err := p.col.SetCrashConsent(true); err != nil {
			u.announce(T("privacy.blocked", "Your browser asks not to be tracked, so nothing can be sent."))
			p.render()
			return
		}
		lsSet(crashKey, telemetryOn)
		u.announce(T("privacy.crashOn", "A code will be sent when the page crashes."))
	} else {
		_ = p.col.SetCrashConsent(false)
		lsSet(crashKey, "")
		lsSet(crashPending, "")
		u.announce(T("privacy.crashOff", "No crash codes will be sent."))
	}
	p.render()
}

// recordSession counts the facts of this visit: browser family, interface language, start-up time.
func (p *privacyUI) recordSession() {
	_ = p.col.Record(telemetry.Browser, browserFamily(js.Global().Get("navigator").Get("userAgent").String()))
	lang := locale
	if lang != "en" && lang != "de" && lang != "fr" && lang != "es" {
		lang = "other"
	}
	_ = p.col.Record(telemetry.Locale, lang)
	_ = p.col.Record(telemetry.Load, loadBucket(time.Since(p.started)))
}

// browserFamily is the family of a browser from its user agent, and nothing of the string is kept.
func browserFamily(ua string) string {
	switch {
	case strings.Contains(ua, "Firefox/"):
		return "firefox"
	case strings.Contains(ua, "Chrome/") || strings.Contains(ua, "Chromium/") || strings.Contains(ua, "Edg/"):
		return "chromium"
	case strings.Contains(ua, "Safari/"):
		return "safari"
	}
	return "other"
}

// loadBucket is how long the start took, in the buckets of the allowlist.
func loadBucket(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under_1s"
	case d < 3*time.Second:
		return "1_to_3s"
	case d < 10*time.Second:
		return "3_to_10s"
	}
	return "over_10s"
}

// tabValue is the allowlist value of a side tab, "" for one that is not counted.
func tabValue(tab string) string {
	if (telemetry.Event{Name: telemetry.Feature, Value: tab}).Valid() {
		return tab
	}
	return ""
}

// noteTab counts a side panel being opened (called on every render; only a change counts).
func (p *privacyUI) noteTab(tab string) {
	if tab == p.lastTab {
		return
	}
	p.lastTab = tab
	if v := tabValue(tab); v != "" {
		_ = p.col.Record(telemetry.Feature, v)
	}
}

// send is the one place a usage count leaves the page: a POST of the exact body to the confirmed
// server's /telemetry, with no cookie and no sign-in token.
func (p *privacyUI) send(ctx context.Context, body []byte) error {
	target := p.endpoint()
	if target == "" {
		return context.Canceled // no server confirmed: nothing leaves
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("js.fetch:credentials", "omit")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return &sendError{res.StatusCode}
	}
	return nil
}

type sendError struct{ status int }

func (e *sendError) Error() string { return "the server answered " + http.StatusText(e.status) }

// flush sends what is pending, when the person opted in and a server is confirmed.
func (p *privacyUI) flush(ctx context.Context) {
	p.drainCrashes()
	if !p.col.Consent() && !p.col.CrashConsent() {
		return
	}
	if p.endpoint() == "" {
		if p.col.LogOn() { // with the local log on, say what would have been sent
			p.col.Withhold(telemetry.OutcomeNoServer)
			p.render()
		}
		return
	}
	n, err := p.col.Flush(ctx, p.send)
	switch {
	case err != nil:
		_ = p.col.Record(telemetry.Error, "send_failed")
		p.message = T("privacy.failed", "Could not send the counts; they are kept for the next try.")
	case n > 0:
		p.message = T("privacy.sent", "Sent %d counts to %s.", n, p.host())
	default:
		p.message = T("privacy.nothing", "There was nothing to send.")
	}
	p.render()
}

// run sends the counts every flushEvery while the page lives.
func (p *privacyUI) run() {
	for {
		time.Sleep(flushEvery)
		p.flush(context.Background())
	}
}

// choose applies the person's choice: "on" or "off". Off forgets everything not yet sent.
func (p *privacyUI) choose(value string, u *ui) {
	if value == telemetryOn {
		if err := p.col.SetConsent(true); err != nil {
			u.announce(T("privacy.blocked", "Your browser asks not to be tracked, so nothing can be sent."))
			p.render()
			return
		}
		lsSet(telemetryKey, telemetryOn)
		p.recordSession()
		p.message = ""
		u.announce(T("privacy.nowOn", "Anonymous counts will be sent to %s. You can stop at any time.", p.host()))
	} else {
		_ = p.col.SetConsent(false)
		lsSet(telemetryKey, "")
		p.message = ""
		u.announce(T("privacy.nowOff", "Nothing will be sent."))
	}
	p.render()
}

// panel is the Privacy tab: the choice, what would be sent as the exact JSON, what could ever be sent.
func (p *privacyUI) panel(u *ui) xango.Node {
	host := p.host()
	consent := p.col.Consent()
	selected := telemetryOff
	if consent {
		selected = telemetryOn
	}
	onLabel := T("privacy.optIn", "Send anonymous counts to the server I confirmed")
	if host != "" {
		onLabel = T("privacy.optInHost", "Send anonymous counts to %s", host)
	}
	text := func(id, s string) xango.Node {
		return xango.El("p", []xango.Attr{xango.A("id", id), xango.A("style", "font-size:13px")}, xango.Text(s))
	}
	nodes := []xango.Node{
		xango.El("h3", []xango.Attr{xango.A("id", "privacy-heading")}, xango.Text(T("privacy.heading", "Usage statistics"))),
		text("privacy-intro", T("privacy.intro", "This editor can send its operator a few anonymous counts, such as which panels are opened. They contain no name, address, document, text, identifier or IP address, and nothing is sent unless you choose it here. The choice is kept in this browser.")),
		xango.El("fieldset", []xango.Attr{xango.A("id", "privacy-choice"), xango.A("style", "border:0;margin:8px 0;padding:0")},
			xango.El("legend", nil, xango.Text(T("privacy.legend", "Share anonymous usage counts?"))),
			components.RadioGroup(components.RadioGroupProps{
				Name: "telemetry", Selected: selected, Disabled: p.col.Blocked(),
				Options: []components.RadioOption{
					{Value: telemetryOff, Label: T("privacy.optOut", "Send nothing (default)")},
					{Value: telemetryOn, Label: onLabel},
				},
				OnChange: func(v string) { p.choose(v, u) },
			})),
		xango.El("div", []xango.Attr{xango.A("id", "privacy-crash")},
			components.Checkbox(components.CheckboxProps{Label: T("privacy.crashLabel", "Also send a code when the page crashes"), Checked: p.col.CrashConsent(), Disabled: p.col.Blocked(),
				OnChange: func(v bool) { p.chooseCrash(v, u) }}),
			text("privacy-crash-hint", T("privacy.crashHint", "Apart from the counts: you can agree to one, both or neither. A crash code is two words, the part of the page and the kind of crash. It never holds a message, a page address or anything from a document."))),
	}
	switch {
	case p.col.Blocked():
		nodes = append(nodes, text("privacy-note", T("privacy.dnt", "Your browser asks not to be tracked (Do Not Track or Global Privacy Control), so nothing is sent and this cannot be turned on.")))
	case host == "":
		nodes = append(nodes, text("privacy-note", T("privacy.noServer", "Counts can only go to a docstore server you confirmed in the People tab, and none is confirmed, so nothing can be sent.")))
	case consent:
		nodes = append(nodes, text("privacy-note", T("privacy.where", "Counts go to %s only, at most once a minute, with no cookie and no sign-in token.", host)))
	}

	// What would be sent: the exact bytes of the pending request, or the reason there are none.
	nodes = append(nodes, xango.El("h4", []xango.Attr{xango.A("id", "privacy-preview-heading")}, xango.Text(T("privacy.previewHeading", "Exactly what would be sent"))))
	preview := string(p.col.Preview())
	if preview == "" {
		if consent || p.col.CrashConsent() {
			preview = T("privacy.previewEmpty", "Nothing yet. Counts appear here as you use the editor, before they are sent.")
		} else {
			preview = T("privacy.previewOff", "Nothing: you chose to send nothing, so no count is kept and no request is made.")
		}
	}
	nodes = append(nodes, xango.El("pre", []xango.Attr{xango.A("id", "privacy-preview"), xango.A("tabindex", "0"),
		xango.A("aria-labelledby", "privacy-preview-heading"), xango.A("style", "white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;background:#f4f4f6;padding:8px")}, xango.Text(preview)))
	nodes = append(nodes, p.samplingAndLog(u)...)
	nodes = append(nodes, btn("privacy-send", T("privacy.sendNow", "Send now"), func() { go p.flush(context.Background()) },
		(!consent && !p.col.CrashConsent()) || p.endpoint() == "" || p.col.Body() == nil))
	nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "privacy-message"), xango.A("role", "status"), xango.A("aria-live", "polite"), xango.A("style", "font-size:13px")}, xango.Text(p.message)))

	// Everything that could ever be sent, from the allowlist the code enforces.
	var items []xango.Node
	for _, row := range telemetry.Allowlist() {
		items = append(items, xango.El("li", []xango.Attr{xango.A("data-name", string(row.Name))},
			xango.El("strong", nil, xango.Text(string(row.Name))),
			xango.Text(": "+strings.Join(row.Values, ", ")+" ("+row.Why+")")))
	}
	nodes = append(nodes,
		xango.El("h4", []xango.Attr{xango.A("id", "privacy-allow-heading")}, xango.Text(T("privacy.allowHeading", "Everything that can ever be sent"))),
		xango.El("ul", []xango.Attr{xango.A("id", "privacy-allowlist"), xango.A("aria-labelledby", "privacy-allow-heading"), xango.A("style", "font-size:12px")}, items...),
	)
	return xango.El("section", []xango.Attr{xango.A("id", "privacy"), xango.A("aria-labelledby", "privacy-heading")}, nodes...)
}

// samplingAndLog is the part of the tab that makes sending rarer or shows it: how many visits send, and
// the local log of what would be sent (kept in this page only, downloadable, gone when the page closes).
func (p *privacyUI) samplingAndLog(u *ui) []xango.Node {
	consent := p.col.Consent()
	var rates [][2]string
	for _, n := range sampleRates {
		rates = append(rates, [2]string{strconv.Itoa(n), sampleLabel(n)})
	}
	sample := selectOf("telemetry-sample", T("privacy.sampleLabel", "Send from"), strconv.Itoa(p.col.SampleOneIn()), rates, func(v string) {
		n, _ := strconv.Atoi(v)
		p.col.SetSampling(n, nil)
		lsSet(sampleKey, v)
		u.announce(T("privacy.sampleSet", "Sending from: %s", sampleLabel(n)))
		p.render()
	})
	logBox := xango.El("label", []xango.Attr{xango.A("style", "display:block;margin:4px 0;font-size:13px")},
		xango.El("input", func() []xango.Attr {
			a := []xango.Attr{xango.A("type", "checkbox"), xango.A("id", "telemetry-log"), xango.On("change", func(e xango.Event) {
				p.col.SetLog(e.Checked)
				p.render()
			})}
			if p.col.LogOn() {
				a = append(a, xango.A("checked", "checked"))
			}
			if !consent {
				a = append(a, xango.A("disabled", "disabled"))
			}
			return a
		}()),
		xango.Text(" "+T("privacy.logLabel", "Keep a log of what would be sent, in this page only")))
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("id", "privacy-sample-heading")}, xango.Text(T("privacy.sampleHeading", "How often"))),
		sample,
	}
	if consent && !p.col.Sampled() {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "privacy-notsampled"), xango.A("style", "font-size:13px")},
			xango.Text(T("privacy.notSampled", "This visit is not in the sample, so nothing will be sent from it."))))
	}
	nodes = append(nodes,
		xango.El("h4", []xango.Attr{xango.A("id", "privacy-log-heading")}, xango.Text(T("privacy.logHeading", "Local log"))),
		xango.El("p", []xango.Attr{xango.A("style", "font-size:13px")}, xango.Text(T("privacy.logIntro", "The log is for you. It is never sent. It is gone when you close this page or choose to send nothing."))),
		logBox,
		xango.El("p", []xango.Attr{xango.A("id", "privacy-log-count"), xango.A("role", "status"), xango.A("style", "font-size:13px")},
			xango.Text(Tn("privacy.logEntries", "The log has %d entry.", "The log has %d entries.", len(p.col.Log())))),
		btn("privacy-log-download", T("privacy.logDownload", "Download the log"), func() {
			transfer.Download("gojiberry-usage-log.json", "application/json", p.col.LogJSON())
		}, len(p.col.Log()) == 0),
	)
	return nodes
}

// state is what #state shows of the Privacy tab, for the tests.
func (p *privacyUI) state() map[string]any {
	return map[string]any{"consent": p.col.Consent(), "blocked": p.col.Blocked(), "pending": p.col.Body() != nil, "sent": p.col.Sent(), "endpoint": p.endpoint(),
		"sampled": p.col.Sampled(), "oneIn": p.col.SampleOneIn(), "log": len(p.col.Log()), "logOn": p.col.LogOn()}
}

// dntOn reports whether a navigator.doNotTrack value says "do not track" ("1" or the older "yes").
func dntOn(v string) bool { return v == "1" || v == "yes" }
