//go:build !(js && wasm)

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/telemetry"
)

// The local log (roadmap 57, step 5): off by default, only for a person who opted in, it holds what would be
// or was sent, and the person can download it. It is never sent and it goes with the opt-in.
func TestLocalLogShowsAndExportsWhatWasSent(t *testing.T) {
	ts := newTelemetrySite(t)
	d := openWithServer(t, ts)
	confirmServer(d)
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#telemetry-log")
	d.OK(d.Disabled("#telemetry-log") && !d.Checked("#telemetry-log"), "the log is off, and cannot be turned on while nothing is shared")
	d.OK(d.Disabled("#privacy-log-download"), "there is nothing to download")

	d.Click("#telemetry-on")
	d.WaitFor("the log box", `!document.getElementById('telemetry-log').disabled`)
	d.Click("#telemetry-log")
	d.WaitFor("the log on", `JSON.parse(document.getElementById('state').textContent).usage.logOn`)
	for _, tab := range []string{"find", "stats", "privacy"} {
		d.Click(`button[data-tab="` + tab + `"]`)
	}
	d.Click("#privacy-send")
	d.WaitFor("the counts to be sent", `JSON.parse(document.getElementById('state').textContent).usage.sent === 1`)
	d.WaitFor("a log entry", `document.getElementById('privacy-log-count').textContent === 'The log has 1 entry.'`)
	d.OK(!d.Disabled("#privacy-log-download"), "the log can be downloaded")

	name, data := d.Download(func() { d.Click("#privacy-log-download") })
	d.OK(name == "gojiberry-usage-log.json", "the file name: "+name)
	var file struct {
		Note    string
		Entries []telemetry.LogEntry
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("the download is not JSON: %v\n%s", err, data)
	}
	d.OK(strings.Contains(file.Note, "never sent") && len(file.Entries) == 1 && file.Entries[0].Outcome == "sent", "one entry, sent: "+string(data))
	var sent telemetry.Batch
	if err := json.Unmarshal([]byte(ts.bodies[0]), &sent); err != nil {
		t.Fatal(err)
	}
	d.OK(len(file.Entries[0].Counts) == len(sent.Counts) && file.Entries[0].Day == sent.Day, "the log holds the batch that was sent")
	if ts.count() != 1 {
		t.Fatalf("the log itself made a request: %d requests", ts.count())
	}

	// Choosing to send nothing forgets the log; it does not come back after a reload.
	d.Click("#telemetry-off")
	d.WaitFor("the log gone", `JSON.parse(document.getElementById('state').textContent).usage.log === 0 && document.getElementById('telemetry-log').disabled`)
	d.Reload()
	d.Click(`button[data-tab="privacy"]`)
	d.OK(d.Disabled("#privacy-log-download") && !d.Checked("#telemetry-log"), "no log after a reload")
	d.NoErrors()
}

// Sampling: a visit outside the sample says so, keeps no preview, and sends nothing even when asked; the
// local log still shows what it would have sent. The rate is kept in this browser.
func TestSamplingKeepsAVisitOutAndSaysSo(t *testing.T) {
	ts := newTelemetrySite(t)
	d := openWithServer(t, ts)
	confirmServer(d)
	d.Click(`button[data-tab="privacy"]`)
	d.WaitSelector("#telemetry-sample")
	d.OK(d.Value("#telemetry-sample") == "1", "every visit is the default")
	d.Click("#telemetry-on")
	d.WaitFor("opted in", `JSON.parse(document.getElementById('state').textContent).usage.consent`)
	d.OK(d.State().Bool("usage.sampled") && !d.Has("#privacy-notsampled"), "at one in one every visit is in")
	d.Click("#telemetry-log")

	// Each change of the rate draws again; at one in a hundred nearly every draw is outside.
	for i := 0; i < 30 && d.State().Bool("usage.sampled"); i++ {
		v := "100"
		if i%2 == 1 {
			v = "10"
		}
		d.Select("#telemetry-sample", v)
		d.Sleep(50)
	}
	if d.State().Bool("usage.sampled") {
		t.Skip("30 draws at one in ten and one in a hundred were all in the sample; astronomically unlikely, try again")
	}
	d.WaitSelector("#privacy-notsampled")
	for _, tab := range []string{"find", "stats", "privacy"} {
		d.Click(`button[data-tab="` + tab + `"]`)
	}
	d.OK(strings.Contains(d.Text("#privacy-notsampled"), "not in the sample"), "it says so: "+d.Text("#privacy-notsampled"))
	d.OK(d.Disabled("#privacy-send") && !d.State().Bool("usage.pending"), "no request is prepared outside the sample")
	d.OK(strings.HasPrefix(d.Text("#privacy-preview"), "Nothing yet"), "the preview is empty: "+d.Text("#privacy-preview"))
	d.Sleep(800)
	if ts.count() != 0 {
		t.Fatalf("a visit outside the sample sent %d request(s)", ts.count())
	}

	// Reloading keeps the rate; opting in again (a fresh draw at 1 in 1 after choosing every visit) sends.
	d.Select("#telemetry-sample", "1")
	d.WaitFor("every visit", `JSON.parse(document.getElementById('state').textContent).usage.oneIn === 1 && JSON.parse(document.getElementById('state').textContent).usage.sampled`)
	d.Reload()
	d.Click(`button[data-tab="privacy"]`)
	d.OK(d.Value("#telemetry-sample") == "1", "the rate survives a reload")
	d.NoErrors()
}

// The Privacy settings are in the display preferences menu, as the other personal settings are.
func TestPrivacySettingsAreInThePreferencesMenu(t *testing.T) {
	ts := newTelemetrySite(t)
	d := openWithServer(t, ts)
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Display preferences")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Privacy settings")`)
	d.WaitSelector("#privacy-heading")
	d.NoErrors()
}
