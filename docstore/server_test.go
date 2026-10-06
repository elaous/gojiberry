package docstore

import (
	"context"
	"github.com/elaous/gojiberry/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *Server {
	return NewServer(NewMemoryAdapter(), "memory")
}

func TestServerHealthReportsBackend(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"backend":"memory"`) {
		t.Fatalf("body = %q, want it to mention backend=memory", rec.Body.String())
	}
}

func TestServerPutGetDeleteRoundTrip(t *testing.T) {
	srv := newTestServer()
	handler := srv.Handler()

	put := httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", rec.Code)
	}

	get := httptest.NewRequest(http.MethodGet, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("GET body = %q, want hello", rec.Body.String())
	}

	del := httptest.NewRequest(http.MethodDelete, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", rec.Code)
	}

	get2 := httptest.NewRequest(http.MethodGet, "/docs/doc1", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get2)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want 404", rec.Code)
	}
}

func TestServerGetMissingReturns404(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/docs/nope", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestServerListReturnsSavedMetadata(t *testing.T) {
	srv := newTestServer()
	handler := srv.Handler()

	for _, id := range []string{"a", "b"} {
		req := httptest.NewRequest(http.MethodPut, "/docs/"+id, strings.NewReader("x"))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"a"`) || !strings.Contains(body, `"b"`) {
		t.Fatalf("list body = %q, want both a and b", body)
	}
}

func TestServerCorsPreflightShortCircuits(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodOptions, "/docs/doc1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("missing CORS header on preflight response")
	}
}

func TestServerMissingIDReturns400(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestServerQuotaNotAvailableWhenAdapterDoesNotImplementIt(t *testing.T) {
	srv := newTestServer() // MemoryAdapter -- no QuotaReporter
	req := httptest.NewRequest(http.MethodGet, "/quota", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when unavailable", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("body = %q, want available:false", rec.Body.String())
	}
}

// fakeQuotaAdapter wraps MemoryAdapter, adding a QuotaReporter with a
// fixed answer -- the smallest way to exercise handleQuota's own
// "available" branch without needing a real LocalFSAdapter/disk.
type fakeQuotaAdapter struct {
	*MemoryAdapter
	quota Quota
}

func (f fakeQuotaAdapter) Usage(_ context.Context) (Quota, error) { return f.quota, nil }

func TestServerQuotaReportsUsageWhenAvailable(t *testing.T) {
	srv := NewServer(fakeQuotaAdapter{MemoryAdapter: NewMemoryAdapter(), quota: Quota{Used: 42, Total: 100}}, "fake")
	req := httptest.NewRequest(http.MethodGet, "/quota", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"used":42`) || !strings.Contains(body, `"total":100`) {
		t.Fatalf("body = %q, want available:true, used:42, total:100", body)
	}
}

func TestServerQuotaMethodNotAllowed(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/quota", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestServerHidesPrefixesAndMountsExtraRoutes(t *testing.T) {
	mem := NewMemoryAdapter()
	ctx := context.Background()
	_ = mem.Save(ctx, "passkey/account/x", []byte("secret account"))
	_ = mem.Save(ctx, "visible", []byte("hello"))
	extra := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("extra")) })
	h := NewServer(mem, "memory").WithRoutes("/auth/passkey/", extra).Hide("passkey/").Handler()
	do := func(method, path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("x")))
		return rec.Code, rec.Body.String()
	}
	if code, body := do("GET", "/docs"); code != 200 || strings.Contains(body, "passkey") || !strings.Contains(body, "visible") {
		t.Errorf("listing: %d %s", code, body)
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if code, _ := do(method, "/docs/passkey/account/x"); code != http.StatusBadRequest {
			t.Errorf("%s of a hidden id: %d", method, code)
		}
	}
	if d, _ := mem.Load(ctx, "passkey/account/x"); string(d.Data) != "secret account" {
		t.Error("the hidden record is untouched")
	}
	if code, body := do("GET", "/auth/passkey/anything"); code != 200 || body != "extra" {
		t.Errorf("the extra route: %d %q", code, body)
	}
	if code, body := do("GET", "/docs/visible"); code != 200 || body != "hello" {
		t.Errorf("%d %q", code, body)
	}
}

func serve(h http.Handler, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// CloseDocs answers 404 for the whole generic API, whatever the id; /health stays.
func TestCloseDocsClosesTheGenericAPI(t *testing.T) {
	mem := NewMemoryAdapter()
	_ = mem.Save(context.Background(), "doc1", []byte("secret"))
	open := NewServer(mem, "memory")
	if open.DocsClosed() {
		t.Fatal("a plain server counts as closed")
	}
	if rec := serve(open.Handler(), "GET", "/docs/doc1", ""); rec.Code != 200 {
		t.Fatalf("open server GET = %d", rec.Code)
	}
	closed := NewServer(mem, "memory").CloseDocs()
	if !closed.DocsClosed() {
		t.Fatal("CloseDocs did not close")
	}
	h := closed.Handler()
	for _, c := range []struct{ method, path string }{
		{"GET", "/docs"}, {"GET", "/docs/doc1"}, {"PUT", "/docs/new"}, {"DELETE", "/docs/doc1"},
		{"GET", "/docs/doc1/acl"}, {"PUT", "/docs/doc1/acl"}, {"GET", "/quota"},
	} {
		if rec := serve(h, c.method, c.path, "x"); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, rec.Code)
		}
	}
	if d, err := mem.Load(context.Background(), "new"); err == nil {
		t.Fatalf("a closed server stored %q", d.Data)
	}
	if rec := serve(h, "GET", "/health", ""); rec.Code != 200 {
		t.Fatalf("/health = %d", rec.Code)
	}
	if !NewServer(mem, "memory").WithRequireACL(true).WithAuth(newTestAuthManager(t)).DocsClosed() {
		t.Fatal("required ACLs with sign-in do not count as closed")
	}
	if NewServer(mem, "memory").WithRequireACL(true).DocsClosed() {
		t.Fatal("required ACLs without sign-in count as closed")
	}
}

// Hide matches without regard to case, so a backend that folds case cannot alias a hidden prefix.
func TestHideIgnoresCase(t *testing.T) {
	mem := NewMemoryAdapter()
	_ = mem.Save(context.Background(), "pubdoc/a", []byte("x"))
	h := NewServer(mem, "memory").Hide("PubDoc/").Hide("passkey/").Handler()
	for _, id := range []string{"pubdoc/a", "PubDoc/a", "PUBDOC/a", "pubDoc/zzz", "Passkey/u", "PASSKEY/u"} {
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if rec := serve(h, method, "/docs/"+id, "y"); rec.Code != http.StatusBadRequest {
				t.Errorf("%s /docs/%s = %d, want 400", method, id, rec.Code)
			}
		}
		if rec := serve(h, "GET", "/docs/"+id+"/acl", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("acl of %s = %d", id, rec.Code)
		}
	}
	rec := serve(h, "GET", "/docs", "")
	if strings.Contains(rec.Body.String(), "pubdoc/a") {
		t.Fatalf("a hidden id is listed: %s", rec.Body.String())
	}
	if rec := serve(h, "PUT", "/docs/pubdocs-not-hidden", "y"); rec.Code != http.StatusNoContent {
		t.Fatalf("a similar id was hidden: %d", rec.Code)
	}
}

// The document routes name the headers a browser script may read.
func TestDocsRoutesExposeHeadersToCORS(t *testing.T) {
	h := newTestServer().Handler()
	for _, path := range []string{"/docs", "/docs/x", "/health"} {
		rec := serve(h, "GET", path, "", "Origin", "https://app.example")
		v := rec.Header().Get("Access-Control-Expose-Headers")
		for _, want := range []string{"X-Gojiberry-Expires", "Retry-After", "Link"} {
			if !strings.Contains(v, want) {
				t.Errorf("%s: Access-Control-Expose-Headers = %q lacks %s", path, v, want)
			}
		}
	}
}

func newTestAuthManager(t *testing.T) *auth.Manager {
	t.Helper()
	m, err := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
