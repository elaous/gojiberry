package docstorecmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

func status(h http.Handler, method, path, token string) int {
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
	withDirectory(ctx, srv, a, docstore.MemoryLimiters())
	if c := status(srv.Handler(), "GET", "/directory/log", ""); c != 404 {
		t.Errorf("no key: %d, want 404 (off)", c)
	}

	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY_PREV", strings.Repeat("22", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP", "domain")
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REQUIRE_POP", "true")
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW", "48h")
	srv, a = build()
	withDirectory(ctx, srv, a, docstore.StoreLimiters(a))
	h := srv.Handler()
	for _, r := range [][2]string{{"POST", "/directory/challenge"}, {"POST", "/directory/publish"}, {"DELETE", "/directory/keys/x"}, {"GET", "/directory/lookup?email=a@x.io"}, {"GET", "/directory/log"}} {
		if c := status(h, r[0], r[1], ""); c != 401 {
			t.Errorf("%s %s = %d, want 401 (mounted, needs sign-in)", r[0], r[1], c)
		}
	}
	// The domain policy and the shared limiters are in effect: a user of x.io may not look up y.org.
	tok, _ := mgr.StartSession(auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true})
	if c := status(h, "GET", "/directory/lookup?email=bo@y.org", tok); c != 403 {
		t.Errorf("another domain = %d", c)
	}
	if c := status(h, "GET", "/directory/lookup?email=bo@x.io", tok); c != 200 {
		t.Errorf("same domain = %d", c)
	}
	if m, _ := docstore.ListPrefix(ctx, a, docstore.RateLimitPrefix); len(m) == 0 {
		t.Error("with the store limiter the budgets are records in the store")
	}
	time.Sleep(10 * time.Millisecond) // let the start-up Resign goroutine finish before the context ends
}
