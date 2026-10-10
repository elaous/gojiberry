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

// The owner key: who the server says I am, enrolling, an address that then wants its signature,
// an entry signed by it, and a second enrollment refused.
func TestOwnerKeyEnrollmentAndSignedPublish(t *testing.T) {
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
	tok, err := srv.Login("Cy@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{Base: ts.URL, Token: func(context.Context) (string, error) { return tok, nil }}
	me, err := c.Me(ctx)
	if err != nil || me != "cy@example.com" {
		t.Fatalf("me = %q, %v", me, err)
	}
	if _, err := (&Client{Base: ts.URL}).Me(ctx); !errors.Is(err, ErrSignInRequired) {
		t.Fatalf("anonymous me: %v", err)
	}
	if e, err := c.Enrollment(ctx, me); err != nil || e.Enrolled {
		t.Fatalf("before: %+v %v", e, err)
	}
	owner, err := GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Enroll(ctx, me, owner); err != nil {
		t.Fatal(err)
	}
	e, err := c.Enrollment(ctx, me)
	if err != nil || !e.Enrolled || len(e.Fingerprints) != 1 || e.Fingerprints[0] != OwnerFingerprint(owner) {
		t.Fatalf("after: %+v %v", e, err)
	}
	// the backup text restores the same key
	back, err := ImportOwnerKey("  " + ExportOwnerKey(owner) + "\n")
	if err != nil || OwnerFingerprint(back) != OwnerFingerprint(owner) {
		t.Fatalf("backup: %v", err)
	}
	if _, err := ImportOwnerKey("not a key"); err == nil {
		t.Fatal("garbage read as an owner key")
	}
	// the address wants the signature now; with it the entry goes in
	pub, secret, _ := encrypt.GenerateRecoveryKey()
	id, _ := encrypt.ParseAgeIdentity(secret)
	if _, err := c.Publish(ctx, PublishRequest{Email: me, Key: pub, Identity: id}); !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("unsigned publish on an enrolled address: %v", err)
	}
	k, err := c.Publish(ctx, PublishRequest{Email: me, Key: pub, Label: "Cy", Identity: id, Owner: back})
	if err != nil || !k.Verified || !k.Held {
		t.Fatalf("signed publish: %+v %v", k, err)
	}
	// a second owner key cannot be enrolled over the first
	other, _ := GenerateOwnerKey()
	if err := c.Enroll(ctx, me, other); !errors.Is(err, ErrAlreadyEnrolled) {
		t.Fatalf("second enroll: %v", err)
	}
}

// Watching my address: the log keys pinned on first use, the position kept between runs (and
// restored from its text), what I did myself not reported, what somebody else did reported, and a
// log that is not checked without keys.
func TestWatchMyAddress(t *testing.T) {
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
	login := func(email string) *Client {
		tok, err := srv.Login(email)
		if err != nil {
			t.Fatal(err)
		}
		return &Client{Base: ts.URL, Token: func(context.Context) (string, error) { return tok, nil }}
	}
	me, other := login("di@example.com"), login("di@example.com") // two sessions of the same account
	if _, err := me.Watch(ctx, "di@example.com", nil); !errors.Is(err, ErrNoLogKeys) {
		t.Fatalf("without keys: %v", err)
	}
	keys, err := me.ServerLogKeys(ctx)
	if err != nil || len(keys) == 0 {
		t.Fatalf("server keys: %v %v", keys, err)
	}
	if err := me.SetLogKeys(map[string]string{"x": "not base64!"}); err == nil {
		t.Fatal("a bad key was accepted")
	}
	if err := me.SetLogKeys(keys); err != nil {
		t.Fatal(err)
	}
	var saved []string
	me.OnState = func(s string) { saved = append(saved, s) }
	pub, secret, _ := encrypt.GenerateRecoveryKey()
	id, _ := encrypt.ParseAgeIdentity(secret)
	if _, err := me.Publish(ctx, PublishRequest{Key: pub, Identity: id}); err != nil {
		t.Fatal(err)
	}
	if len(saved) == 0 {
		t.Fatal("the state was not handed to OnState after a publish")
	}
	rep, err := me.Watch(ctx, "di@example.com", nil)
	if err != nil || len(rep.Alerts) != 0 || rep.HeadSeq == 0 {
		t.Fatalf("my own entry: %+v %v", rep, err)
	}
	// somebody (another session of the account) publishes a key: reported
	pub2, secret2, _ := encrypt.GenerateRecoveryKey()
	id2, _ := encrypt.ParseAgeIdentity(secret2)
	if _, err := other.Publish(ctx, PublishRequest{Key: pub2, Identity: id2}); err != nil {
		t.Fatal(err)
	}
	rep, err = me.Watch(ctx, "di@example.com", nil)
	if err != nil || len(rep.Alerts) != 1 || rep.New != 1 {
		t.Fatalf("the other session's entry: %+v %v", rep, err)
	}
	// not reported twice, also after the state is stored and loaded again
	st := me.State()
	again := login("di@example.com")
	if err := again.SetLogKeys(keys); err != nil {
		t.Fatal(err)
	}
	if err := again.LoadState(st); err != nil {
		t.Fatal(err)
	}
	rep, err = again.Watch(ctx, "di@example.com", nil)
	if err != nil || len(rep.Alerts) != 0 {
		t.Fatalf("after reload: %+v %v", rep, err)
	}
	// damaged state is dropped, and said
	if err := again.LoadState("{not json"); !errors.Is(err, ErrBadState) {
		t.Fatalf("damaged state: %v", err)
	}
}

// Other people's rows: an owner-signed row is not trusted as owner-signed until somebody confirmed
// the owner key; confirming it (and keeping the pin) turns the mark on.
func TestOwnerMarksOfOtherPeoplesRows(t *testing.T) {
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
	login := func(email string) *Client {
		tok, err := srv.Login(email)
		if err != nil {
			t.Fatal(err)
		}
		return &Client{Base: ts.URL, Token: func(context.Context) (string, error) { return tok, nil }}
	}
	bo := login("bo@example.com")
	owner, _ := GenerateOwnerKey()
	if err := bo.Enroll(ctx, "bo@example.com", owner); err != nil {
		t.Fatal(err)
	}
	pub, secret, _ := encrypt.GenerateRecoveryKey()
	id, _ := encrypt.ParseAgeIdentity(secret)
	if _, err := bo.Publish(ctx, PublishRequest{Email: "bo@example.com", Key: pub, Identity: id, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	ana := login("ana@example.com")
	rows, err := ana.Lookup(ctx, "bo@example.com")
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	r := rows[0]
	if !r.OwnerEnrolled || r.OwnerSigned || !r.Owner.NeedsConfirming() || r.OwnerPub == "" {
		t.Fatalf("a row signed by a key nobody confirmed: %+v", r)
	}
	// the second look shows the same key: first seen, still not confirmed
	if rows, _ = ana.Lookup(ctx, "bo@example.com"); rows[0].Owner != OwnerFirstSeen || rows[0].OwnerSigned {
		t.Fatalf("second look: %+v", rows[0])
	}
	// confirmed (after comparing the fingerprint another way), kept, and restored in a new client
	ana.PinOwner("bo@example.com", r.OwnerPub)
	saved := ana.Pins()
	fresh := login("ana@example.com")
	fresh.LoadPins(saved)
	rows, err = fresh.Lookup(ctx, "bo@example.com")
	if err != nil || rows[0].Owner != OwnerPinned || !rows[0].OwnerSigned || rows[0].Owner.NeedsConfirming() {
		t.Fatalf("after confirming: %+v %v", rows, err)
	}
}
