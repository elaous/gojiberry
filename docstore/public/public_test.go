package public

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const hour = time.Hour

type env struct {
	t     *testing.T
	clk   *fakeClock
	inner *docstore.MemoryAdapter
	store *docstore.ExpiringAdapter
	svc   *Service
	srv   *httptest.Server
}

const adminToken = "s3cret-admin-token"

var testReportKey = deriveReportKey([]byte("test report key material"))

func newEnv(t *testing.T, mod func(*Options)) *env {
	t.Helper()
	clk := newClock()
	inner := docstore.NewMemoryAdapter()
	ea := docstore.NewExpiringAdapter(inner, docstore.FixedTTL(24*hour))
	ea.Now = clk.Now
	o := Options{
		Store: ea, Now: clk.Now,
		Config: Config{
			AdminToken: adminToken, TermsURL: "https://example.org/terms", NoProxy: true, ReportKey: testReportKey,
			SessionTTL: 48 * hour, SessionMaxAge: 96 * hour,
			SessionsPerHour: 100000, MaxSessionsPerAddress: 100000, WritesPerMinute: 1000, WritesPerMinutePerAddress: 100000, ViewsPerMinute: 1000, ReportsPerMinute: 1000,
		},
	}
	if mod != nil {
		mod(&o)
	}
	svc, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	srv := docstore.NewServer(ea, "memory")
	svc.Mount(srv)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, clk: clk, inner: inner, store: ea, svc: svc, srv: ts}
}

type resp struct {
	*http.Response
	body []byte
}

func (e *env) do(method, path, token string, body []byte, hdr ...string) resp {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return resp{r, b}
}

func (e *env) want(r resp, status int) resp {
	e.t.Helper()
	if r.StatusCode != status {
		e.t.Fatalf("status = %d, want %d (body %q)", r.StatusCode, status, r.body)
	}
	return r
}

func (e *env) session(hdr ...string) (token string, r resp) {
	e.t.Helper()
	r = e.do("POST", "/public/session", "", nil, hdr...)
	e.want(r, 201)
	var s sessionResponse
	if err := json.Unmarshal(r.body, &s); err != nil {
		e.t.Fatal(err)
	}
	return s.Token, r
}

func (e *env) create(token, content string) docResponse {
	e.t.Helper()
	r := e.want(e.do("POST", "/public/doc", token, []byte(content)), 201)
	var d docResponse
	if err := json.Unmarshal(r.body, &d); err != nil {
		e.t.Fatal(err)
	}
	return d
}

func expires(t *testing.T, r resp) time.Time {
	t.Helper()
	v := r.Header.Get("X-Gojiberry-Expires")
	x, err := time.Parse(time.RFC3339, v)
	if err != nil {
		t.Fatalf("X-Gojiberry-Expires = %q", v)
	}
	return x
}

func (e *env) keys(prefix string) []string {
	var out []string
	metas, err := docstore.ListPrefix(context.Background(), e.inner, prefix)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range metas {
		out = append(out, m.ID)
	}
	return out
}

func TestFlow(t *testing.T) {
	e := newEnv(t, nil)
	token, sr := e.session()
	var s sessionResponse
	_ = json.Unmarshal(sr.body, &s)
	if !strings.HasPrefix(s.UserID, "anon:") || len(s.UserID) < 10 || len(s.Token) < 40 {
		t.Fatalf("session = %+v", s)
	}
	if strings.Contains(string(sr.body), "mail") {
		t.Fatalf("an e-mail field in %s", sr.body)
	}

	r := e.want(e.do("POST", "/public/doc", token, []byte("v1 content")), 201)
	var d docResponse
	_ = json.Unmarshal(r.body, &d)
	start := e.clk.Now()
	if got := expires(t, r); !got.Equal(start.Add(24 * hour)) {
		t.Fatalf("expires %v, want %v", got, start.Add(24*hour))
	}
	links := strings.Join(r.Header.Values("Link"), ",")
	if !strings.Contains(links, `<https://example.org/terms>; rel="terms"`) {
		t.Fatalf("Link = %q", links)
	}
	if r.Header.Get("X-Robots-Tag") == "" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", r.Header)
	}
	if d.ViewID == "" || d.ViewID == d.ID || strings.Contains(d.ViewID, d.ID) {
		t.Fatalf("view id %q vs doc id %q", d.ViewID, d.ID)
	}
	// One document per session.
	e.want(e.do("POST", "/public/doc", token, []byte("another")), 409)
	// No token: no document routes.
	e.want(e.do("POST", "/public/doc", "", []byte("x")), 401)
	e.want(e.do("PUT", "/public/doc", "wrong", []byte("x")), 401)

	// edit renews
	e.clk.Add(10 * hour)
	r = e.want(e.do("PUT", "/public/doc", token, []byte("v2 content")), 204)
	if got := expires(t, r); !got.Equal(e.clk.Now().Add(24 * hour)) {
		t.Fatalf("edit did not renew: %v", got)
	}
	editDeadline := expires(t, r)

	e.clk.Add(5 * hour)
	r = e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 200)
	if string(r.body) != "v2 content" {
		t.Fatalf("view body %q", r.body)
	}
	if got := expires(t, r); !got.Equal(editDeadline) {
		t.Fatalf("viewing moved the deadline to %v (was %v)", got, editDeadline)
	}
	whole := string(r.body) + headerText(r.Header)
	for _, secret := range []string{token, s.UserID, strings.TrimPrefix(s.UserID, "anon:"), d.ID} {
		if strings.Contains(whole, secret) {
			t.Fatalf("a view response carries %q: %s", secret, whole)
		}
	}
	e.want(e.do("PUT", "/public/view/"+d.ViewID, token, []byte("hijack")), 405)
	e.want(e.do("DELETE", "/public/view/"+d.ViewID, token, nil), 405)
	e.want(e.do("GET", "/public/view/not-an-id", "", nil), 404)
	e.want(e.do("GET", "/public/view/"+strings.Repeat("A", 22), "", nil), 404)
	if got := expires(t, e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 200)); !got.Equal(editDeadline) {
		t.Fatal("a second view moved the deadline")
	}

	r = e.want(e.do("GET", "/public/doc", token, nil), 200)
	if string(r.body) != "v2 content" || !strings.Contains(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download %q %v", r.body, r.Header)
	}
	if got := expires(t, r); !got.Equal(editDeadline) {
		t.Fatal("the download renewed")
	}

	for _, id := range []string{"pubdoc/" + d.ID, "public/doc/" + d.ID, "public/view/" + d.ViewID, "report/x", "public/tomb/" + d.ViewID} {
		for _, m := range []string{"GET", "PUT", "DELETE"} {
			if r := e.do(m, "/docs/"+id, "", []byte("x")); r.StatusCode != 400 {
				t.Errorf("%s /docs/%s = %d, want 400", m, id, r.StatusCode)
			}
		}
	}
	if r := e.want(e.do("GET", "/docs", "", nil), 200); strings.Contains(string(r.body), "pubdoc") || strings.Contains(string(r.body), "public/") {
		t.Fatalf("/docs lists public records: %s", r.body)
	}

	// report by view id
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"spam"}`)), 202)
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`"}`)), 400)
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+strings.Repeat("A", 22)+`","reason":"x"}`)), 404)
	if len(e.keys("report/")) != 1 {
		t.Fatalf("reports: %v", e.keys("report/"))
	}

	e.clk.Add(24*hour - 5*hour - time.Second) // just before
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 200)
	e.clk.Add(2 * time.Second)
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 404)
	e.want(e.do("GET", "/public/doc", token, nil), 404)
	e.want(e.do("PUT", "/public/doc", token, []byte("zombie")), 404)

	if _, err := e.store.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if k := e.keys("public/"); len(k) != 1 || k[0] != "public/salt" {
		t.Fatalf("records left after expiry: %v", k)
	}
	if k := e.keys("pubdoc/"); len(k) != 0 {
		t.Fatalf("document left: %v", k)
	}
	if len(e.keys("report/")) != 1 {
		t.Fatal("the report must outlive the document")
	}

	d2 := e.create(token, "second document")
	if d2.ID == d.ID || d2.ViewID == d.ViewID {
		t.Fatal("ids reused")
	}
}

func headerText(h http.Header) string {
	var b strings.Builder
	for k, v := range h {
		b.WriteString(k + ": " + strings.Join(v, ",") + "\n")
	}
	return b.String()
}

func TestDiscardFreesTheSession(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "one")
	e.want(e.do("DELETE", "/public/doc", tok, nil), 204)
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 404)
	e.want(e.do("GET", "/public/doc", tok, nil), 404)
	e.create(tok, "two")
	if n := len(e.keys("public/doc/")); n != 1 {
		t.Fatalf("doc records: %d", n)
	}
}

func TestTakedown(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "bad content")
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"abuse"}`)), 202)

	// the admin route needs the token
	path := "/admin/takedown/" + d.ViewID
	e.want(e.do("DELETE", path, "", nil), 403)
	e.want(e.do("DELETE", path, tok, nil), 403) // a session token is not an admin
	e.want(e.do("DELETE", path, "", nil, AdminHeader, "nope"), 403)
	e.want(e.do("GET", path, "", nil, AdminHeader, adminToken), 405)
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 200) // still there

	e.want(e.do("DELETE", path+"?reason=csam-report-123", "", nil, AdminHeader, adminToken), 204)
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 410)
	e.want(e.do("GET", "/public/doc", tok, nil), 410)
	e.want(e.do("PUT", "/public/doc", tok, []byte("again")), 410)
	e.want(e.do("POST", "/public/doc", tok, []byte("another")), 403) // the session is done
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"again"}`)), 410)
	if k := e.keys("pubdoc/"); len(k) != 0 {
		t.Fatalf("document left: %v", k)
	}
	if k := e.keys("public/doc/"); len(k) != 0 {
		t.Fatalf("doc record left: %v", k)
	}
	if k := e.keys("public/view/"); len(k) != 0 {
		t.Fatalf("view record left: %v", k)
	}
	// idempotent
	e.want(e.do("DELETE", path, "", nil, AdminHeader, adminToken), 204)

	e.clk.Add(48 * hour)
	if _, err := e.store.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 410)
	if len(e.keys(TombstonePrefix)) != 1 || len(e.keys("report/")) != 1 {
		t.Fatalf("tombstones %v reports %v", e.keys(TombstonePrefix), e.keys("report/"))
	}
	e.clk.Add(30 * 24 * hour)
	n, err := e.svc.Sweep(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("Sweep = %d, %v; want the tombstone and the report", n, err)
	}
	if len(e.keys(TombstonePrefix)) != 0 || len(e.keys("report/")) != 0 {
		t.Fatal("old records stayed")
	}
}

func TestTakedownByDocumentID(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "x")
	e.want(e.do("DELETE", "/admin/takedown/"+d.ID, "", nil, AdminHeader, adminToken), 204)
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 410)
}

func TestNoAdminTokenNoAdminRoute(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.AdminToken = "" })
	tok, _ := e.session()
	d := e.create(tok, "x")
	e.want(e.do("DELETE", "/admin/takedown/"+d.ViewID, "", nil, AdminHeader, ""), 404)
	e.want(e.do("DELETE", "/admin/takedown/"+d.ViewID, "", nil, AdminHeader, adminToken), 404)
	e.want(e.do("GET", "/public/view/"+d.ViewID, "", nil), 200)
	if (TokenAdmin("")).Allow(httptest.NewRequest("DELETE", "/", nil)) {
		t.Fatal("empty token allowed")
	}
}

type allowAll struct{}

func (allowAll) Allow(*http.Request) bool { return true }

func TestCustomAdminGuard(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.AdminToken = ""; o.Admin = allowAll{} })
	tok, _ := e.session()
	d := e.create(tok, "x")
	e.want(e.do("DELETE", "/admin/takedown/"+d.ViewID, "", nil), 204)
}

func TestSizeCap413(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxDocBytes = 100 })
	tok, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("a"), 101)), 413)
	// chunked (unknown length) too
	req, _ := http.NewRequest("POST", e.srv.URL+"/public/doc", io.NopCloser(strings.NewReader(strings.Repeat("a", 5000))))
	req.Header.Set("Authorization", "Bearer "+tok)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 413 {
		t.Fatalf("chunked: %d", r.StatusCode)
	}
	d := e.create(tok, strings.Repeat("a", 100))
	e.want(e.do("PUT", "/public/doc", tok, bytes.Repeat([]byte("b"), 101)), 413)
	_ = d
	e.want(e.do("POST", "/public/doc", tok, []byte("more")), 409) // the session has its document
	tok2, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok2, nil), 400) // empty
	if k := e.keys("pubdoc/"); len(k) != 1 {
		t.Fatalf("documents: %v", k)
	}
}

func TestVersionCap(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxVersions = 3 })
	tok, _ := e.session()
	e.create(tok, "v1")
	e.want(e.do("PUT", "/public/doc", tok, []byte("v2")), 204)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v3")), 204)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v4")), 403)
	if r := e.want(e.do("GET", "/public/doc", tok, nil), 200); string(r.body) != "v3" {
		t.Fatalf("body %q", r.body)
	}
}

func TestWriteRate429(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.WritesPerMinute = 3 })
	tok, _ := e.session()
	e.create(tok, "v1")
	e.want(e.do("PUT", "/public/doc", tok, []byte("v2")), 204)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v3")), 204)
	r := e.want(e.do("PUT", "/public/doc", tok, []byte("v4")), 429)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// another session has its own budget
	tok2, _ := e.session()
	e.create(tok2, "other")
	// the budget refills
	e.clk.Add(time.Minute)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v5")), 204)
}

func TestIPDailyByteBudget429(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.IPDailyBytes = 100
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	a, _ := e.session()
	e.want(e.do("POST", "/public/doc", a, bytes.Repeat([]byte("a"), 60), "X-Forwarded-For", "203.0.113.5"), 201)
	b, _ := e.session("X-Forwarded-For", "203.0.113.5")
	r := e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 60), "X-Forwarded-For", "203.0.113.5"), 429)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	e.want(e.do("POST", "/public/doc", b, bytes.Repeat([]byte("b"), 40), "X-Forwarded-For", "203.0.113.5"), 201)
	// another address has its own budget
	c, _ := e.session()
	e.want(e.do("POST", "/public/doc", c, bytes.Repeat([]byte("c"), 60), "X-Forwarded-For", "198.51.100.9"), 201)
	e.clk.Add(24 * hour)
	f, _ := e.session("X-Forwarded-For", "203.0.113.5")
	e.want(e.do("POST", "/public/doc", f, bytes.Repeat([]byte("a"), 90), "X-Forwarded-For", "203.0.113.5"), 201)
}

func TestSessionRatePerIP(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.SessionsPerHour = 2
		o.Config.TrustedProxies = []string{"127.0.0.1"}
	})
	hdr := []string{"X-Forwarded-For", "203.0.113.5"}
	e.session(hdr...)
	e.session(hdr...)
	r := e.do("POST", "/public/session", "", nil, hdr...)
	e.want(r, 429)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	e.session("X-Forwarded-For", "198.51.100.1") // another client
	e.clk.Add(30 * time.Minute)                  // two an hour: one is back
	e.session(hdr...)
}

func TestForwardedForIgnoredWithoutTrustedProxies(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.SessionsPerHour = 1 })
	e.session("X-Forwarded-For", "1.1.1.1")
	e.want(e.do("POST", "/public/session", "", nil, "X-Forwarded-For", "2.2.2.2"), 429)
}

func TestGlobalSessionCap(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxSessions = 2 })
	e.session()
	e.session()
	r := e.want(e.do("POST", "/public/session", "", nil), 503)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// expired sessions stop counting
	e.clk.Add(49 * hour)
	e.session()
}

func TestSessionLifetimeIsBounded(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.SessionTTL = 10 * hour; o.Config.SessionMaxAge = 15 * hour })
	tok, _ := e.session()
	e.create(tok, "v1")
	e.clk.Add(9 * hour)
	e.want(e.do("PUT", "/public/doc", tok, []byte("v2")), 204) // extends to +10h, capped at 15h from issue
	e.clk.Add(5 * hour)                                        // 14 h after issue
	e.want(e.do("PUT", "/public/doc", tok, []byte("v3")), 204)
	e.clk.Add(2 * hour) // 16 h after issue: past the max age however busy
	e.want(e.do("PUT", "/public/doc", tok, []byte("v4")), 401)
	// reading does not extend either
	tok2, _ := e.session()
	e.create(tok2, "x")
	e.clk.Add(9 * hour)
	e.want(e.do("GET", "/public/doc", tok2, nil), 200)
	e.clk.Add(2 * hour)
	e.want(e.do("GET", "/public/doc", tok2, nil), 401)
}

func TestBlocklistChallengeAndTerms(t *testing.T) {
	bl := NewHashBlocklist()
	var failBlock bool
	e := newEnv(t, func(o *Options) {
		o.Blocklist = blocklistFunc(func(ctx context.Context, b []byte) (bool, error) {
			if failBlock {
				return false, errors.New("checker down")
			}
			return bl.Blocked(ctx, b)
		})
		o.Challenger = challengeFunc(func(r *http.Request) error {
			if r.Header.Get("X-Solved") == "no" {
				return ErrChallenge
			}
			return nil
		})
	})
	e.want(e.do("POST", "/public/session", "", nil, "X-Solved", "no"), 403)
	tok, _ := e.session()
	e.want(e.do("POST", "/public/doc", tok, []byte("x"), "X-Solved", "no"), 403)
	bl.Add("2D711642B726B04401627CA9FBAC32F5C8530FB1903CC4DB02258717921A4881") // sha256("x") uppercase
	e.want(e.do("POST", "/public/doc", tok, []byte("x")), 422)
	failBlock = true
	e.want(e.do("POST", "/public/doc", tok, []byte("fine")), 503) // fails closed
	failBlock = false
	d := e.create(tok, "fine")
	e.want(e.do("PUT", "/public/doc", tok, []byte("x")), 422) // edits are screened too
	if r := e.want(e.do("GET", "/public/doc", tok, nil), 200); string(r.body) != "fine" {
		t.Fatalf("a refused edit changed the document: %q", r.body)
	}
	_ = d
}

type blocklistFunc func(context.Context, []byte) (bool, error)

func (f blocklistFunc) Blocked(ctx context.Context, b []byte) (bool, error) { return f(ctx, b) }

type challengeFunc func(*http.Request) error

func (f challengeFunc) Verify(r *http.Request) error { return f(r) }

func TestRefusesAStoreWithoutExpiry(t *testing.T) {
	_, err := New(Options{Store: docstore.NewMemoryAdapter()})
	if !errors.Is(err, ErrExpiryRequired) {
		t.Fatalf("err = %v, want ErrExpiryRequired", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("no store accepted")
	}
}

func TestRefusesToKeepADocumentWithoutDeadline(t *testing.T) {
	ea := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.ExpiryPolicyFunc(func(string, time.Time) (time.Duration, bool) { return 0, false }))
	svc, err := New(Options{Store: ea})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()
	r, _ := http.Post(ts.URL+"/public/session", "", nil)
	var s sessionResponse
	_ = json.NewDecoder(r.Body).Decode(&s)
	r.Body.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/public/doc", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+s.Token)
	r, _ = http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 500 {
		t.Fatalf("status %d, want 500", r.StatusCode)
	}
	if metas, _ := ea.List(context.Background()); len(metas) != 0 {
		t.Fatalf("left behind: %v", metas)
	}
}

func TestTermsURLMustBeHTTP(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "https://x.example/>; rel=\"evil", "/relative", "https://x.example/a\r\nSet-Cookie: a=b"} {
		_, err := New(Options{Store: docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(hour)), Config: Config{TermsURL: bad}})
		if err == nil {
			t.Errorf("terms URL %q accepted", bad)
		}
	}
	e := newEnv(t, func(o *Options) { o.Config.TermsURL = "" })
	if r := e.do("POST", "/public/session", "", nil); r.Header.Get("Link") != "" {
		t.Fatalf("Link without terms: %v", r.Header["Link"])
	}
}

func TestPublicKeyspacesAreNotDocumentsButPublicDocumentsExpire(t *testing.T) {
	for _, k := range []string{"public/doc/x", "public/view/x", TombstonePrefix + "x", "report/x", "PUBLIC/doc/x", "Report/x"} {
		if docstore.IsDocumentKey(k) {
			t.Errorf("%q counts as a document: it would expire or be adopted by the sweeper", k)
		}
	}
	if !docstore.IsDocumentKey(docKey("abc")) {
		t.Error("a public document must be an expiring document")
	}
}

func TestHiddenPrefixes(t *testing.T) {
	if h := HiddenPrefixes(); len(h) != 1 || h[0] != docKeyPrefix {
		t.Fatalf("HiddenPrefixes = %v", h)
	}
}

func TestViewIDDerivation(t *testing.T) {
	a, b := viewIDOf("doc-a"), viewIDOf("doc-b")
	if a == b || !validID(a) || viewIDOf("doc-a") != a {
		t.Fatalf("view ids %q %q", a, b)
	}
}

func TestHeadOnView(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "x")
	r := e.want(e.do("HEAD", "/public/view/"+d.ViewID, "", nil), 200)
	if len(r.body) != 0 {
		t.Fatal("HEAD with a body")
	}
	e.want(e.do("PATCH", "/public/doc", tok, nil), 405)
	e.want(e.do("GET", "/public/session", "", nil), 405)
}

func TestReportCapPerDocument(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.TrustedProxies = []string{"127.0.0.1"} })
	tok, _ := e.session()
	d := e.create(tok, "x")
	for i := 0; i < maxReportsOfDoc+5; i++ {
		e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d.ViewID+`","reason":"r"}`), "X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1)), 202)
	}
	if n := len(e.keys("report/")); n != maxReportsOfDoc {
		t.Fatalf("stored reports: %d, want %d", n, maxReportsOfDoc)
	}
	long := strings.Repeat("é", 3000)
	tok2, _ := e.session()
	d2 := e.create(tok2, "y")
	e.want(e.do("POST", "/public/report", "", []byte(`{"id":"`+d2.ID+`","reason":"`+long+`"}`)), 202)
	for _, k := range e.keys("report/") {
		doc, _ := e.inner.Load(context.Background(), k)
		var rep Report
		_ = json.Unmarshal(doc.Data, &rep)
		if rep.DocID == d2.ID && (len(rep.Reason) > maxReasonBytes || rep.ViewID != d2.ViewID) {
			t.Fatalf("report %+v", rep)
		}
	}
	e.want(e.do("POST", "/public/report", "", bytes.Repeat([]byte("a"), maxReportBody+1)), 413)
}

func TestFailedAdminAuthIsRateLimitedAndSuccessBypassesTheLimiter(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		n := limitN(2)
		o.Limiters.Admin = &n
	})
	id := strings.Repeat("A", 22)
	for i := 0; i < 2; i++ {
		e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, AdminHeader, "guess"), 403)
	}
	e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, AdminHeader, "guess"), 429)
	e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, AdminHeader, ""), 429)
	e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, AdminHeader, adminToken), 202)
	for i := 0; i < 5; i++ { // idempotent: 204 from then on
		e.want(e.do("DELETE", "/admin/takedown/"+id, "", nil, AdminHeader, adminToken), 204)
	}
}

type limitN int

func (l *limitN) Allow(context.Context, string) (bool, time.Duration) {
	if *l <= 0 {
		return false, time.Minute
	}
	*l--
	return true, 0
}

func TestCurrentUserIsAnonymousAuthUser(t *testing.T) {
	e := newEnv(t, nil)
	tok, sr := e.session()
	var s sessionResponse
	_ = json.Unmarshal(sr.body, &s)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	u, ok := e.svc.CurrentUser(req)
	if !ok || u.ID != s.UserID || !u.IsAnonymous() || u.Email != "" {
		t.Fatalf("CurrentUser = %+v, %v", u, ok)
	}
	req.Header.Set("Authorization", "Bearer nope")
	if _, ok := e.svc.CurrentUser(req); ok {
		t.Fatal("a wrong token resolved")
	}
}

func TestSessionTokenOpensNothingElse(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "x")
	if r := e.do("GET", "/docs/pubdoc/"+d.ID, tok, nil); r.StatusCode != 400 {
		t.Fatalf("status %d", r.StatusCode)
	}
}

func TestSweepKeepsRecordsOfLiveDocumentsAndFreshOrphans(t *testing.T) {
	e := newEnv(t, nil)
	tok, _ := e.session()
	d := e.create(tok, "x")
	if n, err := e.svc.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	_ = e.inner.Delete(context.Background(), docKey(d.ID))
	_ = e.inner.Delete(context.Background(), docstore.ExpiryPrefix+docKey(d.ID))
	if n, _ := e.svc.Sweep(context.Background()); n != 0 {
		t.Fatalf("a fresh orphan was swept (%d)", n)
	}
	e.clk.Add(2 * time.Minute)
	if n, _ := e.svc.Sweep(context.Background()); n != 2 {
		t.Fatalf("Sweep = %d, want the two records", n)
	}
}
