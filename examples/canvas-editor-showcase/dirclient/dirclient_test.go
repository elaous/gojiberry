package dirclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/examples/people-keys-demo/demoserver"
)

// The client against the demo server's real routes: publishing as an account with a verified
// e-mail, looking up (the mark comes from the server), and the refusals a page must explain.
func TestDirectoryClientAgainstTheDemoServer(t *testing.T) {
	ctx := context.Background()
	var srv *demoserver.Server
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { srv.Mux.ServeHTTP(w, r) }))
	ts.Start()
	defer ts.Close()
	var err error
	srv, err = demoserver.New(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	tokenFor := func(email string) func(context.Context) (string, error) {
		tok, err := srv.Login(email)
		if err != nil {
			t.Fatal(err)
		}
		return func(context.Context) (string, error) { return tok, nil }
	}
	bo := &Client{Base: ts.URL, Token: tokenFor("bo@example.com")}
	pub, secret, err := encrypt.GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	id, err := encrypt.ParseAgeIdentity(secret)
	if err != nil {
		t.Fatal(err)
	}
	// published with the proof of possession: the row says the key was held
	k, err := bo.Publish(ctx, PublishRequest{Key: pub, Label: "Bo's key", Identity: id})
	if err != nil || !k.Verified || !k.Held || k.Key != pub || k.Fingerprint == "" {
		t.Fatalf("publish: %+v %v", k, err)
	}
	// a proof with the wrong key is not accepted
	other, osecret, _ := encrypt.GenerateRecoveryKey()
	wrong, _ := encrypt.ParseAgeIdentity(osecret)
	if _, err := bo.Publish(ctx, PublishRequest{Key: other, Identity: id}); err == nil {
		t.Fatal("a challenge opened with another key was accepted")
	}
	_ = wrong
	// no proof: this server takes it, and the row says it was not held
	if k, err := bo.Publish(ctx, PublishRequest{Key: other, Label: "no proof"}); err != nil || k.Held {
		t.Fatalf("publish without proof: %+v %v", k, err)
	}
	// Ana looks Bo up and gets the verified mark from the server.
	ana := &Client{Base: ts.URL, Token: tokenFor("ana@example.com")}
	keys, err := ana.Lookup(ctx, "bo@example.com")
	if err != nil || len(keys) != 2 || !keys[0].Verified || (keys[0].Key != pub && keys[1].Key != pub) {
		t.Fatalf("lookup: %+v %v", keys, err)
	}
	if keys, err := ana.Lookup(ctx, "nobody@example.com"); err != nil || len(keys) != 0 {
		t.Fatalf("an unknown address: %+v %v", keys, err)
	}
	// No session: sign in first. A passkey account may not look up or publish.
	if _, err := (&Client{Base: ts.URL}).Lookup(ctx, "bo@example.com"); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("anonymous: %v", err)
	}
	pk, _ := srv.Auth.StartSession(auth.User{ID: "passkey:x", Email: "bo@example.com", VerifiedEmail: true})
	pkc := &Client{Base: ts.URL, Token: func(context.Context) (string, error) { return pk, nil }}
	if _, err := pkc.Lookup(ctx, "bo@example.com"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("passkey lookup: %v", err)
	}
	if _, err := pkc.Publish(ctx, PublishRequest{Key: pub}); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("passkey publish: %v", err)
	}
	// A server with no directory routes says so, for a page to show a clear message.
	plain := httptest.NewServer(http.NewServeMux())
	defer plain.Close()
	nd := &Client{Base: plain.URL}
	if _, err := nd.Lookup(ctx, "x@example.com"); !errors.Is(err, ErrNoDirectory) {
		t.Fatalf("no routes (lookup): %v", err)
	}
	if _, err := nd.Publish(ctx, PublishRequest{Key: pub}); !errors.Is(err, ErrNoDirectory) {
		t.Fatalf("no routes (publish): %v", err)
	}
	var _ Directory = nd
}
