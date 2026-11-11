package docstore_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/userconfig"
)

func TestMeConfigWithSignedInSessions(t *testing.T) {
	ctx := context.Background()
	m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://app.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	mem := docstore.NewMemoryAdapter()
	srv := docstore.NewServer(mem, "memory").WithAuth(m)
	svc := &userconfig.Service{A: mem, Who: srv.UserKey}
	h := srv.WithRoutes("/me/config", svc).WithRoutes("/me/config/", svc).Handler()

	tokenOf := func(email string) string {
		tok, err := m.StartSession(auth.User{Email: email})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	call := func(method, path, token, ifMatch, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	ana, bo := tokenOf("Ana@Example.com"), tokenOf("bo@example.com")
	if rec := call("PUT", "/me/config/prefs", ana, "0", "ana's settings"); rec.Code != 204 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := call("GET", "/me/config/prefs", ana, "", ""); rec.Body.String() != "ana's settings" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if rec := call("GET", "/me/config/prefs", tokenOf("ANA@example.COM"), "", ""); rec.Code != 200 {
		t.Fatalf("the same address in another case, from another device, is the same person: %d", rec.Code)
	}
	if rec := call("GET", "/me/config/prefs", bo, "", ""); rec.Code != 404 {
		t.Fatalf("bo must not see ana's item: %d %q", rec.Code, rec.Body)
	}
	if rec := call("GET", "/me/config/prefs", "", "", ""); rec.Code != 401 {
		t.Fatalf("no session: %d", rec.Code)
	}
	if rec := call("GET", "/me/config/prefs", "not-a-real-token", "", ""); rec.Code != 401 {
		t.Fatalf("an unknown token: %d", rec.Code)
	}
	metas, _ := mem.List(ctx)
	for _, meta := range metas {
		if strings.HasPrefix(meta.ID, docstore.ConfigPrefix) {
			if rec := call("GET", "/docs/"+meta.ID, ana, "", ""); rec.Code != http.StatusBadRequest {
				t.Fatalf("GET /docs/%s: %d", meta.ID, rec.Code)
			}
		}
	}
}
