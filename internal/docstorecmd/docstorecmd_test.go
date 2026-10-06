package docstorecmd

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

func expiring() docstore.Adapter {
	return docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*time.Hour))
}

// Everything off keeps the current behaviour: no public routes, nothing mounted.
func TestPublicOffByDefault(t *testing.T) {
	a := expiring()
	srv := docstore.NewServer(a, "memory")
	svc, err := withPublic(srv, a, 0)
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
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 0); err == nil || !strings.Contains(err.Error(), "EXPIRY_TTL") {
		t.Fatalf("err = %v", err)
	}
	// a TTL was given but the adapter does not expire (miswired): refused too
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, time.Hour); err == nil {
		t.Fatal("a store without expiry was accepted")
	}
}

func TestPublicEnabled(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TERMS_URL", "https://example.org/terms")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_MAX_DOC_BYTES", "10")
	a := expiring()
	srv := docstore.NewServer(a, "memory")
	svc, err := withPublic(srv, a, 24*time.Hour)
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
	if _, err := withPublic(srv, a, time.Hour); err == nil {
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
	if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
	if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
	if _, err := withPublic(srv2, a, 24*time.Hour); err != nil {
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
	if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour); err == nil || !strings.Contains(err.Error(), "ALLOW_OPEN_DOCS") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublicWarnsWhenTrustedProxiesIsEmpty(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	run := func() string {
		logs := captureLog(t)
		a := expiring()
		if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour); err != nil {
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
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour); err == nil || !strings.Contains(err.Error(), "TombstoneTTL") {
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
