package public

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (e *env) report(id, reason string, ip string) resp {
	e.t.Helper()
	return e.do("POST", "/public/report", "", []byte(fmt.Sprintf(`{"id":%q,"reason":%q}`, id, reason)), xff(ip)...)
}

func (e *env) reportCat(id, category, reason string, ip string) resp {
	e.t.Helper()
	return e.do("POST", "/public/report", "", []byte(fmt.Sprintf(`{"id":%q,"reason":%q,"category":%q}`, id, reason, category)), xff(ip)...)
}

func (e *env) reportsOf(doc string) []Report {
	e.t.Helper()
	var out []Report
	for _, k := range e.keys("report/") {
		d, err := e.inner.Load(context.Background(), k)
		if err != nil {
			continue
		}
		var r Report
		if json.Unmarshal(d.Data, &r) == nil && (doc == "" || r.DocID == doc) {
			out = append(out, r)
		}
	}
	return out
}

func TestDuplicateReportsCountOnce(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	first := e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202)
	for _, again := range []string{"spam", "  SPAM ", "Spam", "spam\t"} {
		r := e.want(e.report(d.ViewID, again, "203.0.113.1"), 202)
		if string(r.body) != string(first.body) {
			t.Fatalf("a duplicate was answered %s, want the first report's %s", r.body, first.body)
		}
	}
	e.want(e.report(d.ID, "spam", "203.0.113.1"), 202) // by document id: the same document
	if n := len(e.reportsOf(d.ID)); n != 1 {
		t.Fatalf("%d reports stored for one reporter and one reason, want 1", n)
	}
	if n := len(e.reportsOf(d.ID)); n != 1 {
		t.Fatalf("%d reports", n)
	}
	e.want(e.report(d.ViewID, "a different wording", "203.0.113.1"), 202)               // other again: duplicate
	e.want(e.reportCat(d.ViewID, CategorySpam, "another category", "203.0.113.1"), 202) // a new category: new
	e.want(e.report(d.ViewID, "spam", "203.0.113.2"), 202)                              // another reporter: new
	if n := len(e.reportsOf(d.ID)); n != 3 {
		t.Fatalf("%d reports, want 3", n)
	}
	for _, r := range e.reportsOf(d.ID) {
		if r.Reporter == "" || strings.Contains(r.Reporter, "203.0.113") {
			t.Fatalf("reporter %q", r.Reporter)
		}
	}
	e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202)
	e.want(e.report(d.ViewID, "novel", "2001:db8:1:2::1"), 202)
	e.want(e.report(d.ViewID, "novel", "2001:db8:1:2::ffff"), 202)
	if n := len(e.reportsOf(d.ID)); n != 4 {
		t.Fatalf("%d reports, want 4", n)
	}
}

func TestReportSlotsAreReservedForOtherAddresses(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	for _, cat := range []string{CategorySpam, CategoryOther} {
		for a := 1; a <= 8; a++ {
			e.want(e.reportCat(d.ViewID, cat, "x", fmt.Sprintf("203.0.113.%d", a)), 202)
		}
	}
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc-5 {
		t.Fatalf("one address stored %d reports, want %d", n, maxReportsOfDoc-5)
	}
	for i := 1; i <= 5; i++ {
		e.want(e.report(d.ViewID, "this is illegal here", fmt.Sprintf("198.51.100.%d", i)), 202)
	}
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc {
		t.Fatalf("%d reports after the real ones, want %d", n, maxReportsOfDoc)
	}
	e.want(e.report(d.ViewID, "late", "198.51.100.99"), 202)
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc {
		t.Fatalf("%d reports, want %d", n, maxReportsOfDoc)
	}
}

func TestPriorityReportsTakeTheSlotOfAnOrdinaryOne(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	for i := 0; i < maxReportsOfDoc; i++ {
		e.want(e.report(d.ViewID, "meh", fmt.Sprintf("203.0.113.%d", i+1)), 202)
		e.clk.Add(time.Second) // reports carry the clock's time: the first is the oldest
	}
	oldest := e.reportsOf(d.ID)
	if len(oldest) != maxReportsOfDoc {
		t.Fatalf("%d", len(oldest))
	}
	var firstID string
	for _, r := range oldest {
		if firstID == "" || r.At.Before(e.mustReport(firstID).At) {
			firstID = r.ID
		}
	}
	e.want(e.reportCat(d.ViewID, "  CSAM ", "see the link", "198.51.100.1"), 202)
	now := e.reportsOf(d.ID)
	if len(now) != maxReportsOfDoc {
		t.Fatalf("%d reports, want %d", len(now), maxReportsOfDoc)
	}
	var csam int
	for _, r := range now {
		if r.ID == firstID {
			t.Fatal("the oldest ordinary report was kept")
		}
		if r.Priority && r.Category == CategoryCSAM {
			csam++
		}
	}
	if csam != 1 {
		t.Fatalf("%d priority reports", csam)
	}
	e.want(e.report(d.ViewID, "late", "198.51.100.2"), 202)
	if n := len(e.reportsOf(d.ID)); n != maxReportsOfDoc {
		t.Fatalf("%d", n)
	}
	for i := 0; i < maxReportsOfDoc+3; i++ {
		e.want(e.reportCat(d.ViewID, CategoryIllegal, "illegal here", fmt.Sprintf("192.0.2.%d", i+1)), 202)
	}
	all := e.reportsOf(d.ID)
	if len(all) != maxReportsOfDoc {
		t.Fatalf("%d", len(all))
	}
	prio := 0
	for _, r := range all {
		if r.Priority {
			prio++
		}
	}
	if prio != maxPriorityOfDoc {
		t.Fatalf("%d priority reports in a document, want the half cap %d", prio, maxPriorityOfDoc)
	}
	e2 := newEnv(t, func(o *Options) { behindProxy(o); o.Config.ReportPriorityCategories = []string{"Spam"} })
	if !e2.svc.isPriority(" spam") || e2.svc.isPriority("csam") {
		t.Fatal("configured priority categories")
	}
	e3 := newEnv(t, func(o *Options) { o.Config.ReportPriorityCategories = []string{} })
	if e3.svc.isPriority("csam") {
		t.Fatal("an empty list means no priority categories")
	}
}

func (e *env) mustReport(id string) Report {
	d, err := e.inner.Load(context.Background(), "report/"+id)
	if err != nil {
		e.t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(d.Data, &r); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func TestGlobalEvictionKeepsPriorityReportsLongest(t *testing.T) {
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.MaxReports = 10 })
	var docs []docResponse
	for i := 0; i < 3; i++ {
		tok, _ := e.session()
		docs = append(docs, e.create(tok, "x"))
	}
	r := e.want(e.reportCat(docs[0].ViewID, CategoryIllegal, "illegal", "203.0.113.200"), 202) // the oldest of all
	var prio map[string]string
	_ = json.Unmarshal(r.body, &prio)
	time.Sleep(2 * time.Millisecond)
	for i := 0; i < 14; i++ {
		e.want(e.report(docs[i%3].ViewID, "meh", fmt.Sprintf("203.0.113.%d", i+1)), 202)
		time.Sleep(time.Millisecond)
	}
	keys := e.keys("report/")
	if len(keys) > 10 {
		t.Fatalf("%d reports stored, the bound is 10", len(keys))
	}
	if !contains(keys, "report/"+prio["reportId"]) {
		t.Fatal("the oldest report was evicted although it is a priority one and ordinary ones remained")
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !contains(e.keys("report/"), "report/"+prio["reportId"]) {
		t.Fatal("the sweep evicted the priority report")
	}
}

func TestGlobalReportReserveIsForNewReporters(t *testing.T) {
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.MaxReports = 10 })
	tok, _ := e.session()
	d := e.create(tok, "x")
	for _, cat := range []string{CategorySpam, CategoryOther} {
		for a := 50; a < 54; a++ {
			e.want(e.reportCat(d.ViewID, cat, "x", fmt.Sprintf("203.0.113.%d", a)), 202)
		}
	}
	if n := len(e.keys("report/")); n != 8 {
		t.Fatalf("four addresses hold %d of the 10 reports, want 8", n)
	}
	e.want(e.report(d.ViewID, "real one", "198.51.100.1"), 202)
	e.want(e.report(d.ViewID, "real two", "198.51.100.2"), 202)
	if n := len(e.keys("report/")); n != 10 {
		t.Fatalf("%d reports, want 10", n)
	}
	e2 := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Config.MaxReports = 10
		o.Config.ReportReservePercent = -1
		o.Config.ReportReservedPerDoc = -1
	})
	tok2, _ := e2.session()
	d2 := e2.create(tok2, "x")
	for _, cat := range []string{CategorySpam, CategoryOther} {
		for a := 50; a < 56; a++ {
			e2.want(e2.reportCat(d2.ViewID, cat, "x", fmt.Sprintf("203.0.113.%d", a)), 202)
		}
	}
	if n := len(e2.keys("report/")); n != 10 {
		t.Fatalf("without a reserve the reports stop at %d, want them to fill the 10", n)
	}
}

func TestReporterSaltPersistsAcrossRestarts(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	r1 := e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202)
	svc2, err := New(Options{Store: e.store, Now: e.clk.Now, Config: Config{NoProxy: true, ReportKey: testReportKey}, ClientIP: func(r *http.Request) string { return "203.0.113.1" }})
	if err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(svc2.Handler())
	defer srv2.Close()
	resp2, err := http.Post(srv2.URL+"/public/report", "application/json", strings.NewReader(fmt.Sprintf(`{"id":%q,"reason":"spam"}`, d.ViewID)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	if string(body) != string(r1.body) {
		t.Fatalf("after a restart the duplicate was answered %s, want %s", body, r1.body)
	}
	if n := len(e.reportsOf(d.ID)); n != 1 {
		t.Fatalf("%d reports", n)
	}
}

func TestFromEnvMinorsVariables(t *testing.T) {
	get := func(env map[string]string) (Config, error) {
		return FromEnv(func(k string) string { return env[k] })
	}
	c, err := get(map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_NET":       "40",
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_RESERVE_PERCENT":    "0",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_BYTES":         "1048576",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVED_PER_DOC":    "3",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVE_PERCENT":     "30",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES": " CSAM , Spam ,",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES":              "1000",
		"GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES":            "none",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_ADDRESS":   "5",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS":               "1000",
	})
	if err != nil {
		t.Fatal(err)
	}
	d := c.withDefaults()
	if d.MaxSessionsPerNet != 40 || d.SessionReservePercent != -1 || d.MaxInflightBytes != 1<<20 ||
		d.ReportReservedPerDoc != 3 || d.ReportReservePercent != 30 || len(d.ReportPriorityCategories) != 2 ||
		d.ReportPriorityCategories[0] != "csam" || d.ReportPriorityCategories[1] != "spam" {
		t.Fatalf("config %+v", d)
	}
	d = Config{}.withDefaults()
	if d.MaxSessionsPerNet != DefaultMaxPerNet || d.SessionReservePercent != 20 || d.MaxInflightBytes != 256<<20 ||
		d.ReportReservedPerDoc != 5 || d.ReportReservePercent != 20 || len(d.ReportPriorityCategories) != 2 {
		t.Fatalf("defaults %+v", d)
	}
	if c, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES": "none"}); err != nil || c.ReportPriorityCategories == nil || len(c.withDefaults().ReportPriorityCategories) != 0 {
		t.Fatalf("none: %+v %v", c, err)
	}
	for k, v := range map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSION_RESERVE_PERCENT": "95",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVE_PERCENT":  "x",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_RESERVED_PER_DOC": "20",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_BYTES":      "-4",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_NET":    "0",
	} {
		if _, err := get(map[string]string{k: v}); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s=%q: %v", k, v, err)
		}
	}
	d = Config{MaxSessions: 50, MaxSessionsPerAddress: 20, MaxSessionsPerNet: 5}.withDefaults()
	if d.MaxSessionsPerNet != 20 {
		t.Fatalf("net cap %d", d.MaxSessionsPerNet)
	}
	d = Config{MaxSessions: 50, MaxSessionsPerNet: 500}.withDefaults()
	if d.MaxSessionsPerNet != 50 {
		t.Fatalf("net cap %d", d.MaxSessionsPerNet)
	}
}

func TestConfigWarnsAboutLegacyFloorAndWideCap(t *testing.T) {
	has := func(ws []string, sub string) bool {
		for _, w := range ws {
			if strings.Contains(w, sub) {
				return true
			}
		}
		return false
	}
	base := Config{NoProxy: true, ReportKey: testReportKey}
	c, err := FromEnv(func(k string) string {
		if k == "GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS_PER_MINUTE" {
			return "30"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	c.NoProxy, c.ReportKey = true, testReportKey
	if !has(c.Warnings(), "SESSIONS_PER_MINUTE is no longer read") {
		t.Fatalf("no warning for the old variable: %v", c.Warnings())
	}
	low := base
	low.MinUploadRate = 5
	if !has(low.Warnings(), "below the floor") || low.withDefaults().MinUploadRate != MinUploadRateFloor {
		t.Fatalf("rate %d, warnings %v", low.withDefaults().MinUploadRate, low.Warnings())
	}
	off := base
	off.MinUploadRate = -1
	if has(off.Warnings(), "floor") || off.withDefaults().MinUploadRate != -1 {
		t.Fatal("a negative rate (no minimum) must be left alone")
	}
	w := base
	w.MaxSessionsPerNet, w.MaxSessionsPerWide = 400, 200
	if !has(w.Warnings(), "MAX_SESSIONS_PER_WIDE 200") {
		t.Fatalf("no warning for a clamped wide cap: %v", w.Warnings())
	}
	moved := base
	moved.MaxSessionsPerNet = 900
	if !has(moved.Warnings(), "MAX_SESSIONS_PER_WIDE is not set") {
		t.Fatalf("no warning for a wide cap moved by the net cap: %v", moved.Warnings())
	}
	if len(base.Warnings()) != 0 {
		t.Fatalf("defaults warn: %v", base.Warnings())
	}
}
