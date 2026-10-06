package docstorecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/public"
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

// The admin route counts failed authentications per client address, so behind an unnamed proxy one
// caller's wrong guesses would lock the operator out: with an admin token the server does not start
// unless the proxies are named or "none" says there are none.
func TestPublicRefusesAdminBehindUnsetTrustedProxies(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN", "a-long-random-admin-token")
	a := expiring()
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") || !strings.Contains(err.Error(), "none") {
		t.Fatalf("err = %v, want the refusal naming TRUSTED_PROXIES and none", err)
	}
	for _, v := range []string{"none", "10.0.0.0/8"} {
		t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC_TRUSTED_PROXIES", v)
		a := expiring()
		srv := docstore.NewServer(a, "memory")
		svc, err := withPublic(srv, a, 24*time.Hour)
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
	if _, err := withPublic(docstore.NewServer(a, "memory"), a, 24*time.Hour); err != nil {
		t.Fatal(err)
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
		if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
		if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
		if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
	if _, err := withPublic(srv, a, 24*time.Hour); err != nil {
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
