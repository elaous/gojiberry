package public

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ---- 5. priority reports: a fixed category, caps, and a flag for the operator

func (e *env) storedReport(id string) Report { return e.mustReport(id) }

func reportID(t *testing.T, r resp) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatal(err)
	}
	return m["reportId"]
}

func TestOnlyTheCategoryMakesAReportAPriorityOne(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	for i := 0; i < maxReportsOfDoc; i++ {
		e.want(e.report(d.ViewID, "meh", fmt.Sprintf("203.0.113.%d", i+1)), 202)
	}
	// the word "csam" in the free text, with no category (or with another one), is not priority:
	// the full document refuses it
	e.want(e.report(d.ViewID, "csam", "198.51.100.1"), 202)
	e.want(e.reportCat(d.ViewID, CategorySpam, "illegal csam", "198.51.100.2"), 202)
	for _, r := range e.reportsOf(d.ID) {
		if r.Priority || strings.Contains(strings.ToLower(r.Reason), "csam") {
			t.Fatalf("free text made a priority report: %+v", r)
		}
	}
	// the category does, and an unknown one is "other"
	id := reportID(t, e.want(e.reportCat(d.ViewID, " CSAM", "see it", "198.51.100.3"), 202))
	if r := e.storedReport(id); !r.Priority || r.Category != CategoryCSAM {
		t.Fatalf("%+v", r)
	}
	e.want(e.reportCat(d.ViewID, "terrorism", "x", "198.51.100.4"), 202)
	e.want(e.reportCat(d.ViewID, "{\"a\":1}", "y", "198.51.100.5"), 202)
	for _, r := range e.reportsOf(d.ID) {
		if !validCategory(r.Category) {
			t.Fatalf("category %q stored", r.Category)
		}
	}
	// a fresh document: an unknown category is stored as "other"
	tok2, _ := e.session()
	d2 := e.create(tok2, "y")
	id = reportID(t, e.want(e.reportCat(d2.ViewID, "terrorism", "x", "198.51.100.6"), 202))
	if r := e.storedReport(id); r.Category != CategoryOther || r.Priority {
		t.Fatalf("%+v", r)
	}
	id = reportID(t, e.want(e.report(d2.ViewID, "no category at all", "198.51.100.7"), 202))
	if r := e.storedReport(id); r.Category != CategoryOther {
		t.Fatalf("%+v", r)
	}
	if got := fmt.Sprint(ReportCategories); got != "[csam illegal spam other]" {
		t.Fatal(got)
	}
	// the category is part of the dedupe: the same reporter and text in another category is new
	d3 := e.create(func() string { tk, _ := e.session(); return tk }(), "z")
	a := reportID(t, e.want(e.reportCat(d3.ViewID, CategorySpam, "same words", "198.51.100.8"), 202))
	b := reportID(t, e.want(e.reportCat(d3.ViewID, CategorySpam, "same words", "198.51.100.8"), 202))
	c := reportID(t, e.want(e.reportCat(d3.ViewID, CategoryIllegal, "same words", "198.51.100.8"), 202))
	if a != b || a == c {
		t.Fatalf("dedupe over category: %s %s %s", a, b, c)
	}
	// the configuration accepts only known categories
	if _, err := FromEnv(func(k string) string {
		if k == "GOJIBERRY_DOCSTORE_PUBLIC_REPORT_PRIORITY_CATEGORIES" {
			return "csam,telepathy"
		}
		return ""
	}); err == nil || !strings.Contains(err.Error(), "REPORT_PRIORITY_CATEGORIES") {
		t.Fatalf("an unknown priority category: %v", err)
	}
}

func TestOneReporterHasADailyCapOnPriorityReports(t *testing.T) {
	e := newEnv(t, behindProxy)
	var docs []docResponse
	for i := 0; i < 5; i++ {
		tok, _ := e.session()
		docs = append(docs, e.create(tok, "x"))
	}
	prio := func(d docResponse, ip string) bool {
		return e.storedReport(reportID(t, e.want(e.reportCat(d.ViewID, CategoryCSAM, "this one", ip), 202))).Priority
	}
	got := []bool{}
	for _, d := range docs[:4] {
		got = append(got, prio(d, "203.0.113.50"))
	}
	if fmt.Sprint(got) != "[true true true false]" {
		t.Fatalf("priority flags of one reporter's reports: %v, want three and then an ordinary one", got)
	}
	// the over-cap report is still stored, as an ordinary one
	if n := len(e.reportsOf(docs[3].ID)); n != 1 {
		t.Fatalf("%d reports", n)
	}
	// another reporter has its own allowance
	if !prio(docs[4], "198.51.100.9") {
		t.Fatal("another reporter's priority report was not priority")
	}
	// after a day the allowance is back (the clock moves; the stored reports tell the same after a recount)
	e.clk.Add(23 * time.Hour)
	tok, _ := e.session()
	d := e.create(tok, "fresh")
	e.clk.Add(2 * time.Hour)
	if !prio(d, "203.0.113.50") {
		t.Fatal("the daily allowance did not come back")
	}
	// a restart (a recount from the stored reports) does not give the allowance back either
	tok2, _ := e.session()
	d2 := e.create(tok2, "again")
	for i := 0; i < 2; i++ {
		tk, _ := e.session()
		prio(e.create(tk, "n"), "192.0.2.77")
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.svc.prio = prioTracker{} // as in a new process
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !prio(d2, "192.0.2.77") {
		t.Fatal("the third priority report of the day was not priority")
	}
	tk3, _ := e.session()
	if prio(e.create(tk3, "n"), "192.0.2.77") {
		t.Fatal("a fourth priority report in a day after a recount")
	}
}

func TestPriorityBurstFromOneNetworkIsFlaggedForTheOperator(t *testing.T) {
	logs := captureLog(t)
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.PriorityBurstDocs = 3 })
	var docs []docResponse
	for i := 0; i < 6; i++ {
		tok, _ := e.session()
		docs = append(docs, e.create(tok, "x"))
	}
	for i, d := range docs[:3] {
		e.want(e.reportCat(d.ViewID, CategoryIllegal, "x", fmt.Sprintf("203.0.113.%d", i+1)), 202)
	}
	if e.svc.Health().PriorityBursts != 0 || strings.Contains(logs.String(), "priority reports for more than") {
		t.Fatal("flagged at the limit")
	}
	// reports for other documents from the same /24 (different clients: the point is the network)
	e.want(e.reportCat(docs[3].ViewID, CategoryCSAM, "x", "203.0.113.10"), 202)
	e.want(e.reportCat(docs[4].ViewID, CategoryCSAM, "x", "203.0.113.11"), 202)
	if h := e.svc.Health(); h.PriorityBursts != 1 || !h.OK {
		t.Fatalf("health %+v: want one burst and no problem (public health stays 200)", h)
	}
	if n := strings.Count(logs.String(), "priority reports for more than 3 different documents"); n != 1 {
		t.Fatalf("logged %d times: %s", n, logs.String())
	}
	if !strings.Contains(string(e.want(e.do("GET", "/admin/health", "", nil, AdminHeader, adminToken), 200).body), `"priorityBursts":1`) {
		t.Fatal("not in /admin/health")
	}
	// ordinary reports and another network do not count
	e.want(e.report(docs[5].ViewID, "meh", "198.51.100.1"), 202)
	if e.svc.Health().PriorityBursts != 1 {
		t.Fatal("an ordinary report counted")
	}
	e.clk.Add(61 * time.Minute)
	if e.svc.Health().PriorityBursts != 0 {
		t.Fatal("the flag did not age out")
	}
}

// ---- 6. evicted reports leave the document's record

// refsOf is the report ids in the document's record, and its report count.
func refsOf(t *testing.T, e *env, docID string) (ids []string, count int) {
	t.Helper()
	d, err := e.inner.Load(context.Background(), docRecordPrefix+docID)
	if err != nil {
		t.Fatal(err)
	}
	var m docMeta
	if err := json.Unmarshal(d.Data, &m); err != nil {
		t.Fatal(err)
	}
	for _, r := range m.ReportRefs {
		ids = append(ids, r.ID)
	}
	return ids, m.Reports
}

func liveReportIDs(e *env, docID string) map[string]bool {
	out := map[string]bool{}
	for _, r := range e.reportsOf(docID) {
		out[r.ID] = true
	}
	return out
}

func TestEvictedReportsLeaveTheDocumentsRecordAndDedupeNeverAnswersADeletedID(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Config.MaxReports = 10
		o.Config.ReportReservePercent = -1
		o.Config.ReportReservedPerDoc = -1
	})
	tokA, _ := e.session()
	tokB, _ := e.session()
	a, b := e.create(tokA, "a"), e.create(tokB, "b")
	first := reportID(t, e.want(e.report(a.ViewID, "spam", "203.0.113.1"), 202)) // the oldest report of all
	e.clk.Add(time.Second)
	for i := 2; i <= 8; i++ {
		e.want(e.report(a.ViewID, fmt.Sprintf("a%d", i), fmt.Sprintf("203.0.113.%d", i)), 202)
		e.clk.Add(time.Second)
	}
	for i := 1; i <= 8; i++ {
		e.want(e.report(b.ViewID, fmt.Sprintf("b%d", i), fmt.Sprintf("198.51.100.%d", i)), 202)
		e.clk.Add(time.Second)
		time.Sleep(time.Millisecond)
	}
	if n := len(e.keys("report/")); n > 10 {
		t.Fatalf("%d reports stored, bound 10", n)
	}
	// the oldest was evicted from the store ...
	if contains(e.keys("report/"), "report/"+first) {
		t.Fatal("the oldest report was not evicted; the test needs it to be")
	}
	// ... and from its document's record: no references to reports that are gone
	for _, d := range []docResponse{a, b} {
		ids, count := refsOf(t, e, d.ID)
		live := liveReportIDs(e, d.ID)
		for _, id := range ids {
			if !live[id] {
				t.Fatalf("document %s keeps a reference to the deleted report %s", d.ID[:4], id)
			}
		}
		if count != len(live) || len(ids) != len(live) {
			t.Fatalf("document record counts %d (%d refs), live reports %d", count, len(ids), len(live))
		}
	}
	// the evicted reporter says the same thing again: a new report, not the deleted one's id
	again := reportID(t, e.want(e.report(a.ViewID, "spam", "203.0.113.1"), 202))
	if again == first {
		t.Fatal("the duplicate check answered with the id of a deleted report")
	}
	if !contains(e.keys("report/"), "report/"+again) {
		t.Fatal("the answered report id does not exist")
	}
}

func TestGhostReferencesDoNotCountAgainstTheDocumentsSlots(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	for i := 1; i <= maxReportsOfDoc; i++ {
		e.want(e.report(d.ViewID, fmt.Sprintf("r%d", i), fmt.Sprintf("203.0.113.%d", i)), 202)
	}
	// the reports vanish without the record hearing of it (another replica's eviction, a crash)
	for _, k := range e.keys("report/") {
		if err := e.inner.Delete(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	// the document's 20 slots are free again for what is actually stored
	id := reportID(t, e.want(e.report(d.ViewID, "new", "198.51.100.1"), 202))
	if !contains(e.keys("report/"), "report/"+id) {
		t.Fatal("a document full of references to deleted reports refused a report")
	}
	ids, count := refsOf(t, e, d.ID)
	if len(ids) != 1 || count != 1 {
		t.Fatalf("record keeps %d refs, count %d, want 1 and 1", len(ids), count)
	}
}

func TestDuplicateCheckIgnoresAReferenceToAReportThatIsGone(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	old := reportID(t, e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202))
	if err := e.inner.Delete(context.Background(), "report/"+old); err != nil { // gone behind the record's back
		t.Fatal(err)
	}
	again := reportID(t, e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202))
	if again == old || !contains(e.keys("report/"), "report/"+again) {
		t.Fatalf("the duplicate check answered %s (the deleted report was %s)", again, old)
	}
	if ids, count := refsOf(t, e, d.ID); len(ids) != 1 || ids[0] != again || count != 1 {
		t.Fatalf("record refs %v, count %d", ids, count)
	}
}

func TestReportsPastTheirTTLLeaveTheDocumentsRecord(t *testing.T) {
	e := newEnv(t, func(o *Options) { behindProxy(o); o.Config.ReportTTL = time.Hour })
	tok, _ := e.session()
	d := e.create(tok, "x")
	for i := 1; i <= 4; i++ {
		e.want(e.report(d.ViewID, fmt.Sprintf("r%d", i), fmt.Sprintf("203.0.113.%d", i)), 202)
	}
	e.clk.Add(2 * time.Hour)
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(e.keys("report/")); n != 0 {
		t.Fatalf("%d reports after the TTL", n)
	}
	ids, count := refsOf(t, e, d.ID)
	if len(ids) != 0 || count != 0 {
		t.Fatalf("the record keeps %d references after their reports were deleted (count %d)", len(ids), count)
	}
}

// ---- 8. the reporter salt and key

// reporterOf is the construction documented in reporterHash.
func reporterOf(key, salt []byte, client string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("gojiberry/public/reporter/v1\x00"))
	m.Write(salt)
	m.Write([]byte{0})
	m.Write([]byte(client))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12])
}

func storedSalt(t *testing.T, e *env) []byte {
	t.Helper()
	d, err := e.inner.Load(context.Background(), saltKey)
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Salt []byte `json:"salt"`
	}
	if err := json.Unmarshal(d.Data, &rec); err != nil || len(rec.Salt) < 16 {
		t.Fatalf("salt record %q: %v", d.Data, err)
	}
	return rec.Salt
}

func TestASaltStoreErrorIsNotCachedAndFailsTheReportUntilTheSaltExists(t *testing.T) {
	logs := captureLog(t)
	e, hs := envWith(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	hs.saltFail.Store(true)
	r := e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 503)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	if n := len(e.keys("report/")); n != 0 {
		t.Fatalf("%d reports taken without a salt", n)
	}
	if !strings.Contains(logs.String(), "reporter salt cannot be read or created") {
		t.Fatalf("log %q", logs.String())
	}
	// the store is back, but the backoff has not passed: no attempt, still refused
	hs.saltFail.Store(false)
	e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 503)
	e.clk.Add(2 * time.Second)
	id := reportID(t, e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202))
	// the salt that was made is the stored one (not a process-local one), and the report carries
	// the hash made from it
	salt := storedSalt(t, e)
	if got := e.storedReport(id).Reporter; got != reporterOf(e.svc.reportKey, salt, ClientKey("203.0.113.1")) {
		t.Fatalf("reporter %q is not the hash under the stored salt", got)
	}
	// a second service over the store agrees with it
	svc2, err := New(Options{Store: e.store, Now: e.clk.Now, Config: Config{NoProxy: true, ReportKey: testReportKey}})
	if err != nil {
		t.Fatal(err)
	}
	if h, err := svc2.reporterHash(context.Background(), ClientKey("203.0.113.1")); err != nil || h != e.storedReport(id).Reporter {
		t.Fatalf("second service: %q %v", h, err)
	}
}

func TestTheSaltIsCreatedIfAbsentSoReplicasConverge(t *testing.T) {
	e, hs := envWith(t, behindProxy)
	other := bytes.Repeat([]byte{7}, 32)
	raced := false
	hs.saltRace = func() {
		if raced {
			return
		}
		raced = true
		// another replica creates the salt between our read and our write
		raw, _ := json.Marshal(map[string][]byte{"salt": other})
		if err := hs.Inner.Save(context.Background(), saltKey, raw); err != nil {
			t.Error(err)
		}
	}
	tok, _ := e.session()
	d := e.create(tok, "x")
	id := reportID(t, e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202))
	if !raced {
		t.Fatal("the race did not happen")
	}
	if got := storedSalt(t, e); !bytes.Equal(got, other) {
		t.Fatal("the service overwrote the salt another replica had created")
	}
	if got := e.storedReport(id).Reporter; got != reporterOf(e.svc.reportKey, other, ClientKey("203.0.113.1")) {
		t.Fatalf("the service used a salt of its own: %q", got)
	}
}

func TestReporterHashNeedsTheKeyThatIsNotInTheStore(t *testing.T) {
	e := newEnv(t, behindProxy)
	tok, _ := e.session()
	d := e.create(tok, "x")
	id := reportID(t, e.want(e.report(d.ViewID, "spam", "203.0.113.1"), 202))
	rep := e.storedReport(id).Reporter
	salt := storedSalt(t, e) // all a dump of the store holds
	// what a dump allows is a hash of the salt and a guessed address; none of the plain
	// constructions an attacker would try matches
	client := ClientKey("203.0.113.1")
	for _, guess := range []string{
		reporterOf(salt, salt, client), // keyed by the salt itself
		reporterOf(nil, salt, client),
		reporterOf([]byte{}, nil, client),
	} {
		if guess == rep {
			t.Fatal("the reporter hash can be recomputed from the store alone")
		}
	}
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(client))
	if base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12]) == rep {
		t.Fatal("the reporter hash is HMAC(salt, client): recoverable from a dump")
	}
	// the key decides: another key over the same store gives another hash for the same client
	svc2, err := New(Options{Store: e.store, Now: e.clk.Now, Config: Config{NoProxy: true, ReportKey: deriveReportKey([]byte("another key entirely"))}})
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := svc2.reporterHash(context.Background(), client); h == rep {
		t.Fatal("two keys gave the same reporter hash")
	}
	// a service with no key makes its own per process (and says so)
	logs := captureLog(t)
	svc3, err := New(Options{Store: e.store, Now: e.clk.Now, Config: Config{NoProxy: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(svc3.reportKey) != 32 || !strings.Contains(logs.String(), "no report key") {
		t.Fatalf("key %d bytes, log %q", len(svc3.reportKey), logs.String())
	}
	if h, _ := svc3.reporterHash(context.Background(), client); h == rep {
		t.Fatal("a process key gave the keyed hash")
	}
}

func TestReportKeyFromTheEnvironment(t *testing.T) {
	get := func(env map[string]string) (Config, error) { return FromEnv(func(k string) string { return env[k] }) }
	a, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY": "a secret of some length"})
	if err != nil || len(a.ReportKey) != 32 {
		t.Fatalf("%+v %v", a, err)
	}
	seed := strings.Repeat("ab", 32)
	b, err := get(map[string]string{"GOJIBERRY_DOCSTORE_DIRECTORY_KEY": seed})
	if err != nil || len(b.ReportKey) != 32 || bytes.Equal(a.ReportKey, b.ReportKey) {
		t.Fatalf("directory seed: %+v %v", b, err)
	}
	// the explicit key wins over the directory's seed
	c, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY": "a secret of some length", "GOJIBERRY_DOCSTORE_DIRECTORY_KEY": seed})
	if err != nil || !bytes.Equal(c.ReportKey, a.ReportKey) {
		t.Fatalf("%+v %v", c, err)
	}
	// the derived key is not the seed
	if raw, _ := hexDecode(seed); bytes.Equal(b.ReportKey, raw) {
		t.Fatal("the report key is the directory seed itself")
	}
	if _, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_KEY": "short"}); err == nil || !strings.Contains(err.Error(), "REPORT_KEY") {
		t.Fatalf("a short key: %v", err)
	}
	none, err := get(nil)
	if err != nil || len(none.ReportKey) != 0 {
		t.Fatalf("%+v %v", none, err)
	}
	found := false
	for _, w := range none.Warnings() {
		found = found || strings.Contains(w, "REPORT_KEY")
	}
	if !found {
		t.Fatal("no warning that the key is per process")
	}
}

func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		var b byte
		if _, err := fmt.Sscanf(s[2*i:2*i+2], "%02x", &b); err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}
