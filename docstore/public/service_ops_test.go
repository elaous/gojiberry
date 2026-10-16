package public

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore/public/pow"
)

func TestAdminListsReportsPriorityFirst(t *testing.T) {
	e := newEnv(t, nil)
	tok1, _ := e.session()
	a := e.create(tok1, "one")
	tok2, _ := e.session()
	b := e.create(tok2, "two")
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+a.ViewID+`","reason":"meh","category":"spam"}`)), 202)
	e.clk.Add(time.Minute)
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+b.ViewID+`","reason":"bad","category":"illegal"}`)), 202)

	e.want(e.do("GET", "/admin/reports", "", nil), 403)
	e.want(e.do("POST", "/admin/reports", "", nil, AdminHeader, adminToken), 405)
	e.want(e.do("GET", "/admin/reports?limit=0", "", nil, AdminHeader, adminToken), 400)
	r := e.want(e.do("GET", "/admin/reports", "", nil, AdminHeader, adminToken), 200)
	var got struct {
		Total   int      `json:"total"`
		Reports []Report `json:"reports"`
	}
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Reports) != 2 || got.Reports[0].ViewID != b.ViewID || got.Reports[1].ViewID != a.ViewID {
		t.Fatalf("reports: %+v", got)
	}
	r = e.want(e.do("GET", "/admin/reports?limit=1&category=spam", "", nil, AdminHeader, adminToken), 200)
	got.Reports = nil
	_ = json.Unmarshal(r.body, &got)
	if got.Total != 1 || got.Reports[0].ViewID != a.ViewID {
		t.Fatalf("filtered: %+v", got)
	}
}

func TestNotifyHearsOfKeptReportsAndSurvivesAPanic(t *testing.T) {
	var mu sync.Mutex
	var heard []Report
	done := make(chan struct{}, 4)
	e := newEnv(t, func(o *Options) {
		o.Notify = func(r Report) {
			mu.Lock()
			heard = append(heard, r)
			mu.Unlock()
			done <- struct{}{}
			if r.Category == "spam" {
				panic("boom")
			}
		}
	})
	tok, _ := e.session()
	d := e.create(tok, "x")
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"r","category":"spam"}`)), 202)
	<-done
	// the same reporter and category again is a duplicate: kept once, told once
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"again","category":"spam"}`)), 202)
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"r2","category":"illegal"}`)), 202)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 2 || heard[0].ViewID != d.ViewID || heard[1].Category != "illegal" || !heard[1].Priority {
		t.Fatalf("heard %+v", heard)
	}
}

func TestATakedownRefusesTheSameBytesAgainEvenAfterARestart(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "the bad bytes")
	e.want(e.do("DELETE", "/admin/takedown/"+d.ViewID+"?reason=r", "", nil, AdminHeader, adminToken), 204)
	if k := e.keys("public/blocked/"); len(k) != 1 {
		t.Fatalf("one digest is kept: %v", k)
	}
	tok2, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok2, []byte("the bad bytes")), 422)
	e.create(tok2, "other bytes")

	// the digest outlives the sweep and a new service on the same store reads it
	e.clk.Add(72 * hour)
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k := e.keys("public/blocked/"); len(k) != 1 {
		t.Fatalf("the sweep left the digest: %v", k)
	}
	o := Options{Store: e.store, Now: e.clk.Now, Config: Config{AdminToken: adminToken, NoProxy: true, ReportKey: testReportKey}}
	svc2, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if bad, _ := svc2.taken.Blocked(context.Background(), []byte("the bad bytes")); !bad {
		t.Fatal("a restarted service refuses them too")
	}
}

func TestWebhookNotifyPostsTheReportWithoutItsText(t *testing.T) {
	got := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		got <- m
	}))
	defer srv.Close()
	n, err := WebhookNotify(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	n(Report{ID: "r1", ViewID: "v1", Reason: "secret words", Reporter: "h", Category: "csam", Priority: true})
	m := <-got
	if m["view"] != "v1" || m["category"] != "csam" || m["priority"] != true {
		t.Fatalf("posted %v", m)
	}
	if _, has := m["reason"]; has {
		t.Fatal("the reason must not be sent")
	}
	if _, has := m["reporter"]; has {
		t.Fatal("the reporter must not be sent")
	}
	for _, bad := range []string{"http://example.org/x", "ftp://x", "not an address", "/relative"} {
		if _, err := WebhookNotify(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestProofOfWorkAsksOnlyWhenBusyAndAcceptsEachAnswerOnce(t *testing.T) {
	busy := false
	p := &ProofOfWork{Secret: []byte("0123456789abcdef"), Difficulty: 8, Busy: func() bool { return busy }}
	e := newEnv(t, func(o *Options) { o.Challenger = p; p.Now = o.Now })
	e.want(e.do("POST", "/public/session", "", nil), 201) // not busy: nothing is asked
	busy = true
	r := e.want(e.do("POST", "/public/session", "", nil), 403)
	ask := r.Header.Get(ChallengeHeader)
	if ask == "" {
		t.Fatal("a refused request says what to solve")
	}
	answer, ok := pow.Answer(ask)
	if !ok {
		t.Fatalf("cannot answer %q", ask)
	}
	e.want(e.do("POST", "/public/session", "", nil, ChallengeAnswerHeader, answer), 201)
	e.want(e.do("POST", "/public/session", "", nil, ChallengeAnswerHeader, answer), 403) // once only
	e.want(e.do("POST", "/public/session", "", nil, ChallengeAnswerHeader, "1.x 1"), 403)
	// a challenge of an old slot is not taken
	e.clk.Add(10 * time.Minute)
	e.want(e.do("POST", "/public/session", "", nil, ChallengeAnswerHeader, answer), 403)
}

func TestBusyFollowsTheByteBudget(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxTotalBytes = 100 })
	if e.svc.Busy() {
		t.Fatal("empty is not busy")
	}
	tok, _ := e.session()
	e.create(tok, strings.Repeat("x", 80))
	if !e.svc.Busy() {
		t.Fatal("80 of 100 bytes is busy")
	}
}

func TestDocumentsOnly(t *testing.T) {
	good, err := docfile.Open.Marshal(docfile.Document{Title: "t", Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "hi", Format: docfile.Format{Link: "https://example.org/x"}}},
		{Image: &docfile.ImageItem{URL: "data:image/png;base64,AAAA", Alt: "a"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		body []byte
		bad  bool
	}{
		"a document":     {good, false},
		"not a document": {[]byte("hello, I am not a document"), true},
		"empty":          {nil, true},
		"remote image":   {mustOpen(t, docfile.Document{Content: []docfile.Item{{Image: &docfile.ImageItem{URL: "https://tracker.example/p.gif"}}}}), true},
		"svg image":      {mustOpen(t, docfile.Document{Content: []docfile.Item{{Image: &docfile.ImageItem{URL: "data:image/svg+xml;base64,AAAA"}}}}), true},
		"script link":    {mustOpen(t, docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "x", Format: docfile.Format{Link: "javascript:alert(1)"}}}}}), true},
		"remote clip":    {mustOpen(t, docfile.Document{Content: []docfile.Item{{Media: &docfile.MediaItem{URL: "https://cdn.example/a.mp4"}}}}), true},
		"anchor link":    {mustOpen(t, docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "x", Format: docfile.Format{Link: "#top"}}}}}), false},
		"mail link":      {mustOpen(t, docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "x", Format: docfile.Format{Link: "mailto:a@b.c"}}}}}), false},
		"in the history": {mustOpen(t, docfile.Document{History: []docfile.Revision{{Doc: &docfile.Document{Content: []docfile.Item{{Image: &docfile.ImageItem{URL: "http://x/y.png"}}}}}}}), true},
	} {
		bad, err := DocumentsOnly{}.Blocked(context.Background(), c.body)
		if err != nil || bad != c.bad {
			t.Errorf("%s: refused=%v err=%v, want refused=%v", name, bad, err, c.bad)
		}
	}
	e := newEnv(t, func(o *Options) { o.Blocklist = DocumentsOnly{} })
	tok, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok, []byte("plain text")), 422)
	e.want(e.do("POST", "/public/doc", tok, good), 201)
}

func mustOpen(t *testing.T, d docfile.Document) []byte {
	t.Helper()
	b, err := docfile.Open.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
