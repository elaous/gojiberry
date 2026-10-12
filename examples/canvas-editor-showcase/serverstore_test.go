package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerTransportSendsTheTokenAndLeavesTheRequestAlone(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("Authorization") }))
	defer srv.Close()
	token := "tok-1"
	c := &http.Client{Transport: bearerTransport{token: func(context.Context) (string, error) { return token, nil }}}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	if _, err := c.Do(req); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer tok-1" {
		t.Errorf("Authorization = %q", got)
	}
	if req.Header.Get("Authorization") != "" {
		t.Error("the caller's request was changed")
	}
	token = "" // signed out: no header, the server answers
	if _, err := c.Do(req); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("Authorization = %q without a token", got)
	}
}

func TestBearerTransportFailsWhenTheTokenCannotBeGot(t *testing.T) {
	boom := errors.New("no token")
	c := &http.Client{Transport: bearerTransport{token: func(context.Context) (string, error) { return "", boom }}}
	if _, err := c.Get("http://127.0.0.1:1/"); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
