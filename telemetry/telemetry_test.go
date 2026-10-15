package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/audit"
)

var noon = time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC)

func post(h http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/telemetry", strings.NewReader(body)))
	return rec
}

func receiver() (*Receiver, *Counters) {
	c := NewCounters()
	return NewReceiver(c).WithClock(func() time.Time { return noon }), c
}

func total(c *Counters, k int) (n int) {
	for _, r := range c.Report(k) {
		n += r.N
	}
	return
}

func TestReceiverTakesAnAllowlistedBatchAndOnlyIncrementsCounters(t *testing.T) {
	r, c := receiver()
	for range 2 {
		if rec := post(r, `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":3},{"name":"locale","value":"de","n":1}]}`); rec.Code != http.StatusNoContent {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	}
	got := map[string]int{}
	for _, row := range c.Report(1) {
		got[row.Day+" "+string(row.Name)+" "+row.Value] = row.N
	}
	if got["2030-05-01 feature find"] != 6 || got["2030-05-01 locale de"] != 2 || len(got) != 2 {
		t.Fatalf("totals = %v", got)
	}
	if tt := c.Totals(); tt.Accepted != 2 || tt.Refused != 0 {
		t.Fatalf("request totals = %+v", tt)
	}
}

func TestReceiverRefusesAnythingOutsideTheAllowlist(t *testing.T) {
	cases := map[string]string{
		"unknown name":        `{"day":"2030-05-01","counts":[{"name":"email","value":"find","n":1}]}`,
		"unknown value":       `{"day":"2030-05-01","counts":[{"name":"feature","value":"ana@example.com","n":1}]}`,
		"a value of another":  `{"day":"2030-05-01","counts":[{"name":"locale","value":"find","n":1}]}`,
		"extra field on top":  `{"day":"2030-05-01","user":"ana","counts":[{"name":"feature","value":"find","n":1}]}`,
		"extra field on item": `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1,"id":"x"}]}`,
		"a good one and a bad": `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1},` +
			`{"name":"feature","value":"free text here","n":1}]}`,
		"count too big":     `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1001}]}`,
		"count zero":        `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":0}]}`,
		"count negative":    `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":-4}]}`,
		"listed twice":      `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1},{"name":"feature","value":"find","n":1}]}`,
		"no counts":         `{"day":"2030-05-01","counts":[]}`,
		"a time, not a day": `{"day":"2030-05-01T12:34:56Z","counts":[{"name":"feature","value":"find","n":1}]}`,
		"an old day":        `{"day":"2030-01-01","counts":[{"name":"feature","value":"find","n":1}]}`,
		"a future day":      `{"day":"2031-05-01","counts":[{"name":"feature","value":"find","n":1}]}`,
		"not json":          `hello`,
		"two documents":     `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1}]}{}`,
		"trailing text":     `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1}]} x`,
	}
	for name, body := range cases {
		r, c := receiver()
		rec := post(r, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
		if got := c.Report(1); len(got) != 0 {
			t.Errorf("%s: a refused request still counted: %v", name, got)
		}
		if tt := c.Totals(); tt.Accepted != 0 || tt.Refused != 1 {
			t.Errorf("%s: totals %+v", name, tt)
		}
	}
	r, c := receiver()
	if rec := post(r, strings.Repeat("x", MaxBody+1)); rec.Code != http.StatusRequestEntityTooLarge || c.Totals().Refused != 1 {
		t.Errorf("a body over the limit: status %d", rec.Code)
	}
}

func TestReceiverReadsNoIdentityAndOnlyAnswersPost(t *testing.T) {
	r, _ := receiver()
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(m, "/telemetry", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", m, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/telemetry", strings.NewReader(`{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":1}]}`))
	req.Header.Set("Cookie", "session=secret")
	req.Header.Set("Authorization", "Bearer secret")
	req.RemoteAddr = "203.0.113.9:4444"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Set-Cookie") != "" || rec.Body.Len() != 0 {
		t.Fatalf("answer: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
}

func TestReceiverRateLimitAndAuditCarryNoContent(t *testing.T) {
	var log bytes.Buffer
	r, c := receiver()
	r.WithAudit(audit.NewLogger(&log)).WithLimit(func(*http.Request) (bool, time.Duration) { return false, 3 * time.Second })
	rec := post(r, `{}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "3" || c.Totals().Refused != 0 {
		t.Fatalf("limited: %d %v", rec.Code, rec.Header())
	}
	r.WithLimit(nil)
	post(r, `{"day":"2030-05-01","counts":[{"name":"feature","value":"ana@example.com","n":1}]}`)
	line := log.String()
	if !strings.Contains(line, `"kind":"telemetry.refused"`) || strings.Contains(line, "ana") || strings.Contains(line, "203.0") {
		t.Fatalf("audit line: %q", line)
	}
}

func TestReportHidesSmallCountsAndRetentionDropsOldDays(t *testing.T) {
	r, c := receiver()
	post(r, `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":4},{"name":"feature","value":"share","n":5}]}`)
	got := c.Report(0) // default k = 5
	if len(got) != 1 || got[0].Value != "share" {
		t.Fatalf("k-anonymity: %v", got)
	}
	if total(c, 1) != 9 {
		t.Fatal("k=1 shows everything")
	}
	later := noon.AddDate(0, 0, RetentionDays+2)
	c.Add(Batch{Day: later.Format(DayFormat), Counts: []Count{{Feature, "find", 1}}}, later)
	for _, row := range c.Report(1) {
		if row.Day == "2030-05-01" {
			t.Fatalf("a day past the retention is kept: %v", row)
		}
	}
}

func TestCollectorSendsNothingUnlessOptedIn(t *testing.T) {
	c := NewCollector(func() time.Time { return noon })
	calls := 0
	send := func(context.Context, []byte) error { calls++; return nil }
	if err := c.Record(Feature, "find"); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Flush(context.Background(), send); n != 0 || err != nil || calls != 0 {
		t.Fatalf("default: sent %d times, n %d, %v", calls, n, err)
	}
	if c.Preview() != nil || c.Body() != nil {
		t.Fatal("nothing is kept while consent is off")
	}
	// Turning on does not send what was recorded before: it was never kept.
	if err := c.SetConsent(true); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.Flush(context.Background(), send); n != 0 || calls != 0 {
		t.Fatal("an event recorded before consent was kept")
	}
	_ = c.Record(Feature, "find")
	_ = c.Record(Feature, "find")
	_ = c.Record(Locale, "de")
	// Turning off drops what is pending and sends nothing.
	if err := c.SetConsent(false); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.Flush(context.Background(), send); n != 0 || calls != 0 || c.Preview() != nil {
		t.Fatal("opting out sent or kept something")
	}
}

func TestCollectorPreviewIsWhatIsSent(t *testing.T) {
	c := NewCollector(func() time.Time { return noon })
	_ = c.SetConsent(true)
	_ = c.Record(Feature, "find")
	_ = c.Record(Feature, "find")
	_ = c.Record(Browser, "firefox")
	var sent []byte
	n, err := c.Flush(context.Background(), func(_ context.Context, b []byte) error { sent = b; return nil })
	if err != nil || n != 3 || c.Sent() != 1 {
		t.Fatalf("flush: %d %v", n, err)
	}
	want := `{"day":"2030-05-01","counts":[{"name":"browser","value":"firefox","n":1},{"name":"feature","value":"find","n":2}]}`
	if string(sent) != want {
		t.Fatalf("body = %s", sent)
	}
	if c.Body() != nil {
		t.Fatal("sent counts are cleared")
	}
	// The preview shown before sending decodes to the same batch as the body.
	_ = c.Record(Feature, "find")
	prev, body := c.Preview(), c.Body()
	var a, b Batch
	if json.Unmarshal(prev, &a) != nil || json.Unmarshal(body, &b) != nil || a.Day != b.Day || len(a.Counts) != 1 || a.Counts[0] != b.Counts[0] {
		t.Fatalf("preview %s vs body %s", prev, body)
	}
	if !bytes.Contains(prev, []byte("\n")) {
		t.Fatal("the preview is meant to be read")
	}
	// What the client sends is what the receiver accepts.
	r, counters := receiver()
	if rec := post(r, string(body)); rec.Code != http.StatusNoContent || total(counters, 1) != 1 {
		t.Fatalf("receiver: %d", rec.Code)
	}
}

func TestCollectorKeepsCountsWhenSendFailsAndRefusesBadEvents(t *testing.T) {
	c := NewCollector(func() time.Time { return noon })
	_ = c.SetConsent(true)
	if err := c.Record("email", "ana@example.com"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a name outside the allowlist: %v", err)
	}
	if err := c.Record(Feature, "free text"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("a value outside the allowlist: %v", err)
	}
	if c.Body() != nil {
		t.Fatal("a refused event was kept")
	}
	_ = c.Record(Error, "save_failed")
	boom := errors.New("offline")
	if _, err := c.Flush(context.Background(), func(context.Context, []byte) error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if c.Body() == nil || c.Sent() != 0 {
		t.Fatal("a failed send must keep the counts")
	}
	// Opted out, an event outside the allowlist is still an error for the caller.
	_ = c.SetConsent(false)
	if err := c.Record(Feature, "free text"); !errors.Is(err, ErrNotAllowed) {
		t.Fatal("bad events are reported with consent off too")
	}
}

func TestDoNotTrackAndGlobalPrivacyControlKeepItOff(t *testing.T) {
	for _, signals := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		c := NewCollector(nil)
		_ = c.SetConsent(true)
		_ = c.Record(Feature, "find")
		c.BrowserSignals(signals[0], signals[1])
		if c.Consent() || c.Body() != nil || !c.Blocked() {
			t.Fatalf("%v: consent survived the signal", signals)
		}
		if err := c.SetConsent(true); !errors.Is(err, ErrNoConsent) || c.Consent() {
			t.Fatalf("%v: consent turned on against the signal: %v", signals, err)
		}
	}
	c := NewCollector(nil)
	c.BrowserSignals(false, false)
	if err := c.SetConsent(true); err != nil {
		t.Fatal(err)
	}
}

func TestTheWireFormatHasNoRoomForAnIdentifier(t *testing.T) {
	// Every field of the wire types is a name from the allowlist, a value from it, a count or a day.
	b, _ := json.Marshal(Batch{Day: "2030-05-01", Counts: []Count{{Feature, "find", 1}}})
	var generic map[string]any
	_ = json.Unmarshal(b, &generic)
	if len(generic) != 2 {
		t.Fatalf("batch fields: %v", generic)
	}
	item := generic["counts"].([]any)[0].(map[string]any)
	if len(item) != 3 {
		t.Fatalf("count fields: %v", item)
	}
	for _, row := range Allowlist() {
		for _, v := range row.Values {
			if len(v) > 20 || strings.ContainsAny(v, "@/ .:") {
				t.Errorf("%s=%q looks like it could carry free text", row.Name, v)
			}
		}
	}
}

func TestDocsListEveryAllowlistedValue(t *testing.T) {
	doc, err := os.ReadFile("../docs/telemetry.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range Allowlist() {
		if !strings.Contains(string(doc), "`"+string(row.Name)+"`") {
			t.Errorf("docs/telemetry.md does not list the name %s", row.Name)
		}
		for _, v := range row.Values {
			if !strings.Contains(string(doc), "`"+v+"`") {
				t.Errorf("docs/telemetry.md does not list %s=%s", row.Name, v)
			}
		}
	}
}

func TestBatchHookSeesOnlyAcceptedBatches(t *testing.T) {
	r, _ := receiver()
	var seen []Batch
	r.WithBatchHook(func(b Batch) { seen = append(seen, b) })
	good := `{"day":"2030-05-01","counts":[{"name":"feature","value":"find","n":2}]}`
	if rec := post(r, good); rec.Code != http.StatusNoContent {
		t.Fatalf("%d", rec.Code)
	}
	for _, bad := range []string{`{`, `{"day":"2030-05-01","counts":[{"name":"feature","value":"x@y.z","n":1}]}`, `{"day":"2030-05-01","user":"a","counts":[{"name":"feature","value":"find","n":1}]}`} {
		if rec := post(r, bad); rec.Code == http.StatusNoContent {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if len(seen) != 1 || seen[0].Counts[0].N != 2 {
		t.Fatalf("hook saw %v", seen)
	}
}

// Crash codes have a consent of their own: with only it on, crash codes are kept and sent and a usage count is not;
// with only the usage consent on, a crash code is dropped; turning one off leaves the other alone; Do Not Track
// turns both off. Nothing outside the part and kind words is accepted.
func TestCrashCodesHaveTheirOwnConsent(t *testing.T) {
	c := NewCollector(func() time.Time { return time.Date(2030, 5, 1, 12, 0, 0, 0, time.UTC) })
	_ = c.Record(Crash, "editor_trap")
	if _, ok := c.Batch(); ok {
		t.Fatal("a crash code was kept with no consent")
	}
	if err := c.SetCrashConsent(true); err != nil {
		t.Fatal(err)
	}
	_ = c.Record(Crash, "editor_trap")
	_ = c.Record(Feature, "find")
	b, ok := c.Batch()
	if !ok || len(b.Counts) != 1 || b.Counts[0].Name != Crash || b.Counts[0].Value != "editor_trap" {
		t.Fatalf("with crash consent only the batch is %+v", b)
	}
	if err := c.SetConsent(true); err != nil {
		t.Fatal(err)
	}
	_ = c.Record(Feature, "find")
	if b, _ := c.Batch(); len(b.Counts) != 2 {
		t.Fatalf("both on: %+v", b)
	}
	_ = c.SetConsent(false) // the usage counts go, the crash code stays
	b, ok = c.Batch()
	if !ok || len(b.Counts) != 1 || b.Counts[0].Name != Crash {
		t.Fatalf("usage off, crash on: %+v", b)
	}
	_ = c.SetCrashConsent(false)
	if _, ok := c.Batch(); ok {
		t.Error("something is left after both were turned off")
	}
	_ = c.SetCrashConsent(true)
	c.BrowserSignals(true, false)
	if c.CrashConsent() || c.SetCrashConsent(true) != ErrNoConsent {
		t.Error("Do Not Track did not turn the crash consent off")
	}
	for _, bad := range []string{"editor", "editor_stack", "other_trap", "editor_trap: boom", ""} {
		if (Event{Crash, bad}).Valid() {
			t.Errorf("crash value %q is accepted", bad)
		}
	}
	if !(Event{Crash, "worker_rejection"}).Valid() {
		t.Error("a code of the allowlist is refused")
	}
}
