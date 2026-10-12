package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendSendsTheMessageWithTheKey(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"49a3999c"}`))
	}))
	defer srv.Close()
	r := Resend{APIKey: "re_testkey", From: "noreply@example.com", BaseURL: srv.URL}
	err := r.Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: "Verify your address", Text: "Open the link.", HTML: "<p>Open the link.</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/emails" || gotAuth != "Bearer re_testkey" {
		t.Fatalf("%s %s %q", gotMethod, gotPath, gotAuth)
	}
	if gotBody["from"] != "noreply@example.com" || gotBody["subject"] != "Verify your address" || gotBody["text"] != "Open the link." || gotBody["html"] != "<p>Open the link.</p>" {
		t.Fatalf("%v", gotBody)
	}
	if to, _ := gotBody["to"].([]any); len(to) != 1 || to[0] != "ana@example.com" {
		t.Fatalf("%v", gotBody["to"])
	}
}

func TestResendRefusalsNeverCarryTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"statusCode":422,"name":"validation_error","message":"The from address is not verified"}`))
	}))
	defer srv.Close()
	r := Resend{APIKey: "re_secretkey", From: "noreply@example.com", BaseURL: srv.URL}
	err := r.Send(context.Background(), Message{To: []string{"ana@example.com"}, Subject: "s", Text: "t"})
	if err == nil || strings.Contains(err.Error(), "re_secretkey") || !strings.Contains(err.Error(), "did not accept") {
		t.Fatalf("%v", err)
	}
	// nothing is sent for a message that is not valid, a missing key, a bad sender or a bad URL
	n := 0
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n++ }))
	defer counting.Close()
	good := Message{To: []string{"ana@example.com"}, Subject: "s", Text: "t"}
	for name, c := range map[string]struct {
		r Resend
		m Message
	}{
		"bad message": {Resend{APIKey: "k", From: "a@example.com", BaseURL: counting.URL}, Message{Subject: "s", Text: "t"}},
		"no key":      {Resend{From: "a@example.com", BaseURL: counting.URL}, good},
		"bad sender":  {Resend{APIKey: "k", From: "A <a@example.com>", BaseURL: counting.URL}, good},
		"bad url":     {Resend{APIKey: "k", From: "a@example.com", BaseURL: "http://[::1"}, good},
	} {
		if err := c.r.Send(context.Background(), c.m); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if n != 0 {
		t.Fatalf("%d requests were sent", n)
	}
	// a cancelled context stops the call
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Resend{APIKey: "k", From: "a@example.com", BaseURL: counting.URL}).Send(cctx, good); err == nil {
		t.Fatal("cancelled")
	}
	// a long first line of a provider error is cut
	if got := briefError(errString(strings.Repeat("x", 500) + "\nsecond line")); len(got) != 160 {
		t.Fatalf("%d", len(got))
	}
}

type errString string

func (e errString) Error() string { return string(e) }
