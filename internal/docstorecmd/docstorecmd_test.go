package docstorecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/public"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func statusTok(h http.Handler, method, path, token string) int {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestWithDirectoryIsOffWithoutAKeyAndMountsEveryRouteWithOne(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	build := func() (*docstore.Server, docstore.Adapter) {
		a := docstore.NewMemoryAdapter()
		return docstore.NewServer(a, "memory").WithAuth(mgr), a
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", "")
	srv, a := build()
	if err := withDirectory(ctx, srv, a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if c := statusTok(srv.Handler(), "GET", "/directory/log", ""); c != 404 {
		t.Errorf("no key: %d, want 404 (off)", c)
	}

	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV", strings.Repeat("22", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP", "domain")
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW", "48h")
	srv, a = build()
	if err := withDirectory(ctx, srv, a, docstore.StoreLimiters(a)); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	for _, r := range [][2]string{{"POST", "/directory/challenge"}, {"POST", "/directory/publish"}, {"DELETE", "/directory/keys/x"}, {"GET", "/directory/lookup?email=a@x.io"}, {"GET", "/directory/log"}} {
		if c := statusTok(h, r[0], r[1], ""); c != 401 {
			t.Errorf("%s %s = %d, want 401 (mounted, needs sign-in)", r[0], r[1], c)
		}
	}
	// The domain policy and the shared limiters are in effect: a user of x.io may not look up y.org.
	tok, _ := mgr.StartSession(auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true})
	if c := statusTok(h, "GET", "/directory/lookup?email=bo@y.org", tok); c != 403 {
		t.Errorf("another domain = %d", c)
	}
	if c := statusTok(h, "GET", "/directory/lookup?email=bo@x.io", tok); c != 200 {
		t.Errorf("same domain = %d", c)
	}
	if m, _ := docstore.ListPrefix(ctx, a, docstore.RateLimitPrefix); len(m) == 0 {
		t.Error("with the store limiter the budgets are records in the store")
	}
}

func expiring() docstore.Adapter {
	return docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
}

// Everything off keeps the current behaviour: no public routes, nothing mounted.
func TestPublicOffByDefault(t *testing.T) {
	a := expiring()
	srv := docstore.NewServer(a, "memory")
	svc, err := withPublic(srv, a, 0, docstore.MemoryLimiters())
	if err != nil || svc != nil {
		t.Fatalf("withPublic = %v, %v", svc, err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/public/session", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/public/session = %d, want 404 with the offer off", rec.Code)
	}
}

// The 24 hour promise: the public routes do not start without expiry.
func TestPublicRefusesToStartWithoutExpiry(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	a := docstore.NewMemoryAdapter()
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 0, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "EXPIRY_TTL") {
		t.Fatalf("err = %v", err)
	}
	// a TTL was given but the adapter does not expire (miswired): refused too
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, time.Hour, docstore.MemoryLimiters()); err == nil {
		t.Fatal("a store without expiry was accepted")
	}
}

func TestPublicEnabled(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL", "https://example.org/terms")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES", "10")
	a := expiring()
	srv := docstore.NewServer(a, "memory")
	svc, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters())
	if err != nil || svc == nil {
		t.Fatalf("withPublic = %v, %v", svc, err)
	}
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/public/session", nil))
	if rec.Code != http.StatusCreated || !strings.Contains(strings.Join(rec.Header().Values("Link"), ","), `rel="terms"`) {
		t.Fatalf("session = %d %v", rec.Code, rec.Header())
	}
	// no admin token configured: no takedown route
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("DELETE", "/admin/takedown/AAAAAAAAAAAAAAAAAAAAAA", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("takedown without a token = %d", rec.Code)
	}
	// a bad value is an error, not a default
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES", "ten")
	if _, err := withPublic(srv, a, time.Hour, docstore.MemoryLimiters()); err == nil {
		t.Fatal("a bad value was accepted")
	}
}

func status(h http.Handler, method, path string) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("x")))
	return rec.Code
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	return &buf
}

// Public mode closes the generic /docs API by default: anonymous visitors must not be able to read
// or write the documents of the store beside the public offer.
func TestPublicClosesDocsByDefault(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	a := expiring()
	_ = a.Save(context.Background(), "private-doc", []byte("secret"))
	srv := docstore.NewServer(a, "memory")
	if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	for _, c := range [][2]string{{"GET", "/docs"}, {"GET", "/docs/private-doc"}, {"PUT", "/docs/private-doc"}, {"DELETE", "/docs/private-doc"}, {"GET", "/docs/private-doc/acl"}, {"GET", "/quota"}} {
		if code := status(h, c[0], c[1]); code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c[0], c[1], code)
		}
	}
	if d, err := a.Load(context.Background(), "private-doc"); err != nil || string(d.Data) != "secret" {
		t.Fatalf("the document changed: %v %v", d, err)
	}
	if code := status(h, "POST", "/public/session"); code != http.StatusCreated {
		t.Fatalf("/public/session = %d", code)
	}
	if code := status(h, "GET", "/health"); code != http.StatusOK {
		t.Fatalf("/health = %d", code)
	}
}

// With sign-in and required ACLs /docs is protected, so it is left to them.
func TestPublicLeavesDocsToRequiredACLs(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	m, err := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	a := expiring()
	srv := docstore.NewServer(a, "memory").WithRequireACL(true).WithAuth(m)
	if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	if code := status(h, "GET", "/docs"); code != http.StatusOK {
		t.Fatalf("/docs = %d: the ACL-protected API must stay reachable for signed-in people", code)
	}
	// an anonymous write is refused by the ACL rule, not stored
	if code := status(h, "PUT", "/docs/x"); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("anonymous PUT /docs/x = %d", code)
	}
	// RequireACL without a manager is not protection: closed by default again
	srv2 := docstore.NewServer(a, "memory").WithRequireACL(true)
	if _, err := withPublic(srv2, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if code := status(srv2.Handler(), "GET", "/docs"); code != http.StatusNotFound {
		t.Fatalf("/docs = %d with RequireACL but no sign-in", code)
	}
}

func TestPublicAllowOpenDocsIsAnExplicitOptInAndLoud(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS", "true")
	logs := captureLog(t)
	a := expiring()
	srv := docstore.NewServer(a, "memory")
	if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if code := status(srv.Handler(), "GET", "/docs"); code != http.StatusOK {
		t.Fatalf("/docs = %d, want it open", code)
	}
	if out := logs.String(); !strings.Contains(out, "WARNING") || !strings.Contains(out, "OPEN") {
		t.Fatalf("no loud warning: %q", out)
	}
	// anything but a boolean refuses to start
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS", "yes please")
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "ALLOW_OPEN_DOCS") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublicWarnsWhenTrustedProxiesIsEmpty(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	run := func() string {
		logs := captureLog(t)
		a := expiring()
		if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		return logs.String()
	}
	if out := run(); !strings.Contains(out, "WARNING") || !strings.Contains(out, "TRUSTED_PROXIES") {
		t.Fatalf("no warning: %q", out)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", "none")
	if out := run(); strings.Contains(out, "TRUSTED_PROXIES") {
		t.Fatalf("\"none\" did not silence the warning: %q", out)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", "10.0.0.0/8")
	if out := run(); strings.Contains(out, "TRUSTED_PROXIES") {
		t.Fatalf("a configured proxy list warned: %q", out)
	}
}

// The admin route counts failed authentications per client address, so behind an unnamed proxy one
// caller's wrong guesses would lock the operator out: with an admin token the server does not start
// unless the proxies are named or "none" says there are none.
func TestPublicRefusesAdminBehindUnsetTrustedProxies(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN", "a-long-random-admin-token")
	a := expiring()
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") || !strings.Contains(err.Error(), "none") {
		t.Fatalf("err = %v, want the refusal naming TRUSTED_PROXIES and none", err)
	}
	for _, v := range []string{"none", "10.0.0.0/8"} {
		t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", v)
		a := expiring()
		srv := docstore.NewServer(a, "memory")
		svc, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters())
		if err != nil || svc == nil {
			t.Fatalf("TRUSTED_PROXIES=%s: %v", v, err)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/admin/health", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("/admin/health without the token = %d, want 403", rec.Code)
		}
	}
	// no admin token, no admin route, no demand
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN", "")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", "")
	a = expiring()
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
}

// A tombstone shorter than the session lets a ban lapse: the server does not start.
func TestPublicRefusesAShortTombstoneTTL(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TOMBSTONE_TTL", "24h")
	a := expiring()
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "TombstoneTTL") {
		t.Fatalf("err = %v", err)
	}
}

// A client that is slow to send its request is cut off by the server's own timeouts.
func TestHTTPServerHasReadTimeouts(t *testing.T) {
	s := newHTTPServer(":0", http.NotFoundHandler())
	if s.ReadTimeout <= 0 || s.ReadHeaderTimeout <= 0 || s.IdleTimeout <= 0 {
		t.Fatalf("timeouts: read %v header %v idle %v", s.ReadTimeout, s.ReadHeaderTimeout, s.IdleTimeout)
	}
}

// plainAdapter hides every optional capability (conditional writes among them) of the store it wraps.
type plainAdapter struct{ docstore.Adapter }

func TestWithDirectoryOnAStoreWithoutConditionalWrites(t *testing.T) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	plain := plainAdapter{docstore.NewMemoryAdapter()}
	build := func() *docstore.Server { return docstore.NewServer(plain, "plain").WithAuth(mgr) }

	// More than one process on such a store: the directory refuses to start, and is not mounted.
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "2")
	srv := build()
	if err := withDirectory(ctx, srv, plain, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "conditional") {
		t.Fatalf("two replicas on a store without conditional writes: %v", err)
	}
	if c := statusTok(srv.Handler(), "GET", "/directory/log", ""); c != 404 {
		t.Errorf("not mounted: %d", c)
	}
	// One process: a warning, and it runs.
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")
	logs := captureLog(t)
	if err := withDirectory(ctx, build(), plain, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "WARNING") || !strings.Contains(logs.String(), "conditional") {
		t.Errorf("no warning: %q", logs)
	}
	// A conditional store takes any number.
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "4")
	a := docstore.NewMemoryAdapter()
	if err := withDirectory(ctx, docstore.NewServer(a, "memory").WithAuth(mgr), a, docstore.MemoryLimiters()); err != nil {
		t.Errorf("a store with conditional writes: %v", err)
	}
	for _, bad := range []string{"0", "-1", "two"} {
		t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", bad)
		if err := withDirectory(ctx, build(), plain, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "REPLICAS") {
			t.Errorf("replicas %q: %v", bad, err)
		}
	}
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "")
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET", "everything")
	if err := withDirectory(ctx, build(), plain, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "GLOBAL_BUDGET") {
		t.Errorf("a bad budget mode: %v", err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_GLOBAL_BUDGET", "domain")
	if err := withDirectory(ctx, build(), plain, docstore.MemoryLimiters()); err != nil {
		t.Errorf("per domain: %v", err)
	}
}

// The start-up check runs before the server serves, logs what it found, and shows it in /health.
func TestWithDirectoryStartupCensusIsLoggedAndInHealth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	a := docstore.NewMemoryAdapter()
	// Records published under one key ...
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	first := docstore.NewServer(a, "memory").WithAuth(mgr)
	if err := withDirectory(ctx, first, a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	tok, _ := mgr.StartSession(auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true})
	for i := range 2 {
		req := httptest.NewRequest("POST", "/directory/publish", strings.NewReader(fmt.Sprintf(`{"key":"%s"}`, ageKey(t, i))))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		first.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("publish: %d %s", rec.Code, rec.Body)
		}
	}
	// ... and the operator starts the server with another key and no PREV.
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("22", 32))
	logs := captureLog(t)
	second := docstore.NewServer(a, "memory").WithAuth(mgr)
	if err := withDirectory(ctx, second, a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "2 directory records are named under unknown keys") {
		t.Errorf("log: %q", logs)
	}
	rec := httptest.NewRecorder()
	second.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	var h struct {
		Status    string
		Directory struct {
			Records     int `json:"records"`
			UnknownKeys int `json:"unknown_keys"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil || h.Status != "ok" || h.Directory.UnknownKeys != 2 || h.Directory.Records != 2 {
		t.Errorf("/health: %s", rec.Body)
	}
	// Listing the old key as retired makes them known again, and re-signs them before serving.
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV", strings.Repeat("11", 32))
	logs = captureLog(t)
	third := docstore.NewServer(a, "memory").WithAuth(mgr)
	if err := withDirectory(ctx, third, a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "unknown keys") || !strings.Contains(logs.String(), "2 re-signed") {
		t.Errorf("log: %q", logs)
	}
}

func ageKey(t *testing.T, i int) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id.Recipient().String()
}

// The public offer's budgets can live in the store like the directory's, so replicas share them.
func TestPublicTakesTheStoreLimiters(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	a := expiring()
	srv := docstore.NewServer(a, "memory")
	if _, err := withPublic(srv, a, 24*time.Hour, docstore.StoreLimiters(a)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/public/session", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("session = %d", rec.Code)
	}
	metas, _ := docstore.ListPrefix(context.Background(), a, docstore.RateLimitPrefix+"public-session/")
	if len(metas) != 1 {
		t.Errorf("the session budget is not a record in the store: %v", metas)
	}
	// A second server process on the same store shares the budget.
	srv2 := docstore.NewServer(a, "memory")
	if _, err := withPublic(srv2, a, 24*time.Hour, docstore.StoreLimiters(a)); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/public/session", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("session = %d", rec.Code)
	}
	if metas, _ := docstore.ListPrefix(context.Background(), a, docstore.RateLimitPrefix+"public-session/"); len(metas) != 1 {
		t.Errorf("two replicas, two budgets: %v", metas)
	}
	// And the default is still a budget per process.
	b := expiring()
	srv3 := docstore.NewServer(b, "memory")
	if _, err := withPublic(srv3, b, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv3.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/public/session", nil))
	if metas, _ := docstore.ListPrefix(context.Background(), b, docstore.RateLimitPrefix); len(metas) != 0 {
		t.Errorf("memory limiters wrote records: %v", metas)
	}
}

// publicSession opens an anonymous session on h and creates its one document; it returns the
// bearer token and the key the document is stored under.
func publicSession(t *testing.T, h http.Handler, a docstore.Adapter) (token, key string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/public/session", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("session = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("session body %q: %v", rec.Body, err)
	}
	req := httptest.NewRequest("POST", "/public/doc", strings.NewReader("hello"))
	req.Header.Set("Authorization", "Bearer "+out.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	metas, err := docstore.ListPrefix(context.Background(), a, "pubdoc/")
	if err != nil || len(metas) != 1 {
		t.Fatalf("public documents = %v %v", metas, err)
	}
	return out.Token, metas[0].ID
}

func renewWith(h http.Handler, token, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/docs/"+url.PathEscape(id)+"/renew", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A public (anonymous) session token cannot renew anything through POST /docs/{id}/renew, in
// each way the generic API can be configured beside the public offer, and never moves the
// deadline of a public document.
func TestPublicSessionTokenCannotUseTheRenewRoute(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", "none")
	ctx := context.Background()
	deadline := func(a *docstore.ExpiringAdapter, key string) time.Time {
		at, ok, err := a.Expiry(ctx, key)
		if err != nil || !ok {
			t.Fatalf("expiry of %s: %v %v", key, ok, err)
		}
		return at
	}

	t.Run("generic API closed (the default in public mode)", func(t *testing.T) {
		a := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
		_ = a.Save(ctx, "other", []byte("x"))
		srv := docstore.NewServer(a, "memory")
		if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		h := srv.Handler()
		tok, key := publicSession(t, h, a)
		before := deadline(a, key)
		for _, id := range []string{key, "other"} {
			rec := renewWith(h, tok, id)
			if rec.Code != http.StatusNotFound || rec.Header().Get(docstore.GoneHeader) != "" {
				t.Errorf("renew %s = %d %v, want a plain 404 (no document-gone marker)", id, rec.Code, rec.Header())
			}
		}
		if got := deadline(a, key); !got.Equal(before) {
			t.Fatalf("the deadline moved: %v -> %v", before, got)
		}
	})

	t.Run("generic API behind sign-in and required ACLs", func(t *testing.T) {
		m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://app.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		a := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
		_ = a.Save(ctx, "shared", []byte("x"))
		_ = a.Save(ctx, "acl:shared", []byte(`{"owner":"ana@example.com"}`))
		srv := docstore.NewServer(a, "memory").WithRequireACL(true).WithAuth(m)
		if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		h := srv.Handler()
		tok, key := publicSession(t, h, a)
		before := deadline(a, key)
		// The public document's own key is hidden from /docs: 400 whoever asks.
		if rec := renewWith(h, tok, key); rec.Code != http.StatusBadRequest {
			t.Errorf("renew of the public document = %d, want 400", rec.Code)
		}
		// A document with an access list: the token is no sign-in, so it is nobody.
		if rec := renewWith(h, tok, "shared"); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("renew of a shared document = %d, want 401/403", rec.Code)
		}
		// Required ACLs: a document nobody owns is closed to it too.
		_ = a.Save(ctx, "unowned", []byte("x"))
		if rec := renewWith(h, tok, "unowned"); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("renew of an unowned document = %d, want 401/403", rec.Code)
		}
		if got := deadline(a, key); !got.Equal(before) {
			t.Fatalf("the deadline moved: %v -> %v", before, got)
		}
	})

	t.Run("generic API left open on purpose", func(t *testing.T) {
		t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS", "true")
		a := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
		srv := docstore.NewServer(a, "memory")
		if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		h := srv.Handler()
		tok, key := publicSession(t, h, a)
		before := deadline(a, key)
		if rec := renewWith(h, tok, key); rec.Code != http.StatusBadRequest {
			t.Errorf("renew of the public document = %d, want 400 (hidden prefix)", rec.Code)
		}
		if rec := renewWith(h, "", key); rec.Code != http.StatusBadRequest {
			t.Errorf("anonymous renew of the public document = %d, want 400", rec.Code)
		}
		if got := deadline(a, key); !got.Equal(before) {
			t.Fatalf("the deadline moved: %v -> %v", before, got)
		}
	})
}

// The hub gives a public session token no role on a document with an access list (it is not a
// sign-in), so it cannot edit, and so cannot make the hub renew anything.
func TestPublicSessionTokenHasNoRoleInTheHub(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", "none")
	ctx := context.Background()
	m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	a := docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
	_ = a.Save(ctx, "acl:shared", []byte(`{"owner":"ana@example.com"}`))
	srv := docstore.NewServer(a, "memory").WithAuth(m)
	if _, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	tok, key := publicSession(t, srv.Handler(), a)
	before, _, _ := a.Expiry(ctx, key)

	var renewals int
	hh := hub.New(hub.Config{Store: a, Renewer: countingRenewer{a, &renewals}, RenewInterval: -1,
		User: hub.AuthUser(m), Roles: hub.ACLRolesWith(a, hub.AuthUser(m), hub.Owner)})
	hs := httptest.NewServer(hh.Handler())
	defer hs.Close()
	for _, c := range []struct{ method, path string }{
		{"POST", "/docs/shared/ops?replica=r"}, {"GET", "/docs/shared/snapshot"},
		{"POST", "/docs/" + url.PathEscape(key) + "/ops?replica=r"}, // a slash id: refused outright
	} {
		req, _ := http.NewRequest(c.method, hs.URL+c.path, strings.NewReader(`[]`))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			t.Errorf("%s %s with a public token = %d, want a refusal", c.method, c.path, resp.StatusCode)
		}
	}
	_ = hh.Flush()
	if renewals != 0 {
		t.Fatalf("the hub renewed %d times for a public token", renewals)
	}
	if after, _, _ := a.Expiry(ctx, key); !after.Equal(before) {
		t.Fatalf("the public document's deadline moved: %v -> %v", before, after)
	}
}

type countingRenewer struct {
	docstore.Renewer
	n *int
}

func (c countingRenewer) Renew(ctx context.Context, id string) error {
	*c.n++
	return c.Renewer.Renew(ctx, id)
}

// docstore.ConfigFromEnv turns the orphan sweep on by the same reading of GOJIBERRY_DOCSTORE_PUBLIC
// that public.FromEnv uses to mount the offer, so the two cannot disagree.
func TestOrphanSweepDefaultMatchesThePublicSwitch(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_EXPIRY_TTL", "24h")
	for _, v := range []string{"", "true", "TRUE", "1", "on", "false", "0", "off"} {
		t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", v)
		pc, err := public.FromEnv(os.Getenv)
		if err != nil {
			t.Fatal(err)
		}
		if got := docstore.ConfigFromEnv().ExpiryOrphans; got != pc.Enabled {
			t.Errorf("GOJIBERRY_DOCSTORE_PUBLIC=%q: orphan sweep %v, public offer %v", v, got, pc.Enabled)
		}
	}
}

// A short admin token is refused at start; the explicit override starts the server and warns loudly.
func TestPublicRefusesAShortAdminTokenUnlessAllowed(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", "none")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN", "short")
	a := expiring()
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "ADMIN_TOKEN") || !strings.Contains(err.Error(), "ALLOW_SHORT_ADMIN_TOKEN") {
		t.Fatalf("err = %v, want the refusal naming the token and the override", err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN", "true")
	a = expiring()
	if svc, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour, docstore.MemoryLimiters()); err != nil || svc == nil {
		t.Fatalf("with the override: %v", err)
	}
}

func TestAdminToken(t *testing.T) {
	check := adminToken("op-secret")
	req := func(h string) *http.Request {
		r := httptest.NewRequest("POST", "/admin/directory/unmapped", nil)
		if h != "" {
			r.Header.Set("X-Gojiberry-Admin-Token", h)
		}
		return r
	}
	if !check(req("op-secret")) {
		t.Error("the right token")
	}
	for _, bad := range []string{"", "op-secret ", "OP-SECRET", "op-secre", "other"} {
		if check(req(bad)) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseWitnesses(t *testing.T) {
	k := strings.Repeat("ab", 32)
	got, err := parseWitnesses("one:" + k + ", two:" + k)
	if err != nil || len(got) != 2 || len(got["one"]) != 32 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"one", "one:zz", "one:" + k[:10], ":" + k, "a:" + k + ",a:" + k} {
		if _, err := parseWitnesses(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
