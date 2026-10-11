package userconfig

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func svc() (*Service, *docstore.MemoryAdapter) {
	m := docstore.NewMemoryAdapter()
	return &Service{A: m, Who: func(r *http.Request) (string, bool) {
		u := r.Header.Get("X-User")
		return u, u != ""
	}}, m
}

func do(h http.Handler, method, path, user, ifMatch, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if user != "" {
		req.Header.Set("X-User", user)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPRoundTripWithRevisions(t *testing.T) {
	h, _ := svc()
	if rec := do(h, "GET", "/me/config", "", "", ""); rec.Code != 401 {
		t.Fatalf("not signed in: %d", rec.Code)
	}
	if rec := do(h, "GET", "/me/config", "ana", "", ""); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"items":[]}` {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	if rec := do(h, "PUT", "/me/config/prefs", "ana", "", "x"); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("PUT needs If-Match: %d", rec.Code)
	}
	rec := do(h, "PUT", "/me/config/prefs", "ana", `"0"`, `{"font":18}`)
	if rec.Code != 204 || rec.Header().Get("Etag") != `"1"` {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	get := do(h, "GET", "/me/config/prefs", "ana", "", "")
	if get.Code != 200 || get.Body.String() != `{"font":18}` || get.Header().Get("Etag") != `"1"` ||
		get.Header().Get("Cache-Control") != "no-store" || get.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%d %q %v", get.Code, get.Body, get.Header())
	}
	stale := do(h, "PUT", "/me/config/prefs", "ana", `"0"`, "other device")
	if stale.Code != http.StatusPreconditionFailed || stale.Header().Get("Etag") != `"1"` {
		t.Fatalf("a stale write shows the current revision: %d %v", stale.Code, stale.Header())
	}
	if rec := do(h, "PUT", "/me/config/prefs", "ana", "1", "second"); rec.Code != 204 || rec.Header().Get("Etag") != `"2"` {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if list := do(h, "GET", "/me/config/", "ana", "", ""); strings.TrimSpace(list.Body.String()) != `{"items":["prefs"]}` {
		t.Fatalf("%q", list.Body)
	}
	if rec := do(h, "DELETE", "/me/config/prefs", "ana", `"1"`, ""); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("delete of a stale revision: %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/me/config/prefs", "ana", "", ""); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("delete needs If-Match: %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/me/config/prefs", "ana", `"2"`, ""); rec.Code != 204 {
		t.Fatalf("%d", rec.Code)
	}
	if rec := do(h, "GET", "/me/config/prefs", "ana", "", ""); rec.Code != 404 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestHTTPPeopleAreSeparatedAndInputsChecked(t *testing.T) {
	h, _ := svc()
	do(h, "PUT", "/me/config/prefs", "ana", "0", "ana's secret")
	if rec := do(h, "GET", "/me/config/prefs", "bo", "", ""); rec.Code != 404 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		method, path, im string
		want             int
	}{
		{"PUT", "/me/config/Bad%20Name", "0", 400},
		{"PUT", "/me/config/a", "abc", 428},
		{"PUT", "/me/config/a", "-1", 428},
		{"POST", "/me/config/a", "0", 405},
		{"PUT", "/me/config", "0", 405},
		{"GET", "/me/config/..%2fx", "", 400},
	} {
		if rec := do(h, c.method, c.path, "ana", c.im, "x"); rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	h.MaxItem = 4
	if rec := do(h, "PUT", "/me/config/big", "ana", "0", "12345678"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body over the item limit: %d", rec.Code)
	}
	h.MaxItem = 0
	h.MaxTotal = 20
	if rec := do(h, "PUT", "/me/config/more", "ana", "0", strings.Repeat("x", 30)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the total: %d", rec.Code)
	}
	custom := &Service{A: docstore.NewMemoryAdapter(), Prefix: "/cfg/", Who: h.Who}
	if rec := do(custom, "PUT", "/cfg/a", "ana", "0", "x"); rec.Code != 204 {
		t.Fatalf("a custom prefix: %d", rec.Code)
	}
}

func TestHTTPStoreFailureLeaksNothing(t *testing.T) {
	m := docstore.NewMemoryAdapter()
	h := &Service{A: brokenLoad{m}, Who: func(*http.Request) (string, bool) { return "ana", true }}
	rec := do(h, "GET", "/me/config/a", "ana", "", "")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "disk") {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	lh := &Service{A: brokenList{m}, Who: h.Who}
	if rec := do(lh, "GET", "/me/config", "ana", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", rec.Code)
	}
}

type brokenList struct{ docstore.Adapter }

func (brokenList) List(context.Context) ([]docstore.Meta, error) { return nil, errors.New("disk") }
