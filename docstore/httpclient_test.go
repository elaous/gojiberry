package docstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPAdapterConformance(t *testing.T) {
	testAdapterConformance(t, func() Adapter {
		ts := httptest.NewServer(NewServer(NewMemoryAdapter(), "memory").Handler())
		t.Cleanup(ts.Close)
		return NewHTTPAdapter(ts.URL+"/", nil)
	})
}

func TestHTTPAdapterHeadersAndErrors(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer ts.Close()
	a := NewHTTPAdapter(ts.URL, ts.Client())
	a.Header.Set("Authorization", "Bearer x")
	ctx := context.Background()
	if err := a.Save(ctx, "a b", []byte("x")); err == nil {
		t.Fatal("save: want the server's refusal")
	}
	if got != "Bearer x" {
		t.Fatalf("header %q", got)
	}
	if _, err := a.Load(ctx, "a"); err == nil {
		t.Fatal("load")
	}
	if _, err := a.List(ctx); err == nil {
		t.Fatal("list")
	}
	if err := a.Delete(ctx, "a"); err == nil {
		t.Fatal("delete")
	}
	ts.Close()
	if err := a.Save(ctx, "a", nil); err == nil {
		t.Fatal("unreachable server")
	}
	if _, err := a.Load(ctx, "a"); err == nil {
		t.Fatal("unreachable server")
	}
	if _, err := a.List(ctx); err == nil {
		t.Fatal("unreachable server")
	}
	if err := a.Delete(ctx, "a"); err == nil {
		t.Fatal("unreachable server")
	}
}

func TestHTTPAdapterBadList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer ts.Close()
	if _, err := NewHTTPAdapter(ts.URL, nil).List(context.Background()); err == nil {
		t.Fatal("bad JSON")
	}
}
