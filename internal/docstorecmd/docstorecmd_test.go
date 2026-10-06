package docstorecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
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
