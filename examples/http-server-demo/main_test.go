package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestServesOverRealHTTP starts an actual TCP listener (httptest.Server,
// not just an in-memory handler call) and makes a real HTTP request
// against it, proving this renders over the network the way a browser
// would see it -- not just that xango.Render doesn't error.
func TestServesOverRealHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/?name=Gopher")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("got Content-Type %q, want text/html; charset=utf-8", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head><title>gojiberry</title></head><body><h1>Hello, Gopher!</h1></body></html>`
	if string(body) != want {
		t.Errorf("got:  %s\nwant: %s", body, want)
	}
}

func TestFallsBackToStrangerWithNoNameParam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><head><title>gojiberry</title></head><body><h1>Hello, stranger!</h1></body></html>`
	if string(body) != want {
		t.Errorf("got:  %s\nwant: %s", body, want)
	}
}
