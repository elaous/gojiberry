package docstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func usageGet(h http.Handler, admin bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/admin/usage", nil)
	if admin {
		req.Header.Set("X-Admin", "yes")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminUsageCountsAndNamesNothing(t *testing.T) {
	ctx := context.Background()
	mem := NewMemoryAdapter()
	_ = mem.Save(ctx, "ana-private-report", []byte("12345"))
	_ = mem.Save(ctx, "bo-notes", []byte("123"))
	_ = mem.Save(ctx, aclKey("ana-private-report"), []byte("{}"))
	srv := NewServer(mem, "memory").WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" })
	h := srv.Handler()

	rec := usageGet(h, true)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var u usageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.Documents != 2 || u.Records != 1 || u.Bytes != 10 || u.Largest != 5 {
		t.Fatalf("%+v", u)
	}
	for _, leak := range []string{"ana", "bo-notes", "report"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("the report names %q: %s", leak, rec.Body)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("not cacheable")
	}

	if rec := usageGet(h, false); rec.Code != http.StatusForbidden {
		t.Fatalf("without the guard: %d", rec.Code)
	}
	post := httptest.NewRecorder()
	h.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/admin/usage", nil))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", post.Code)
	}
	if rec := usageGet(NewServer(mem, "memory").Handler(), true); rec.Code != http.StatusNotFound {
		t.Fatalf("with no operator guard configured the route does not exist: %d", rec.Code)
	}
	if rec := usageGet(NewServer(brokenStore{mem}, "b").WithDirectoryAdmin(func(*http.Request) bool { return true }).Handler(), true); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failing store: %d", rec.Code)
	}
}
