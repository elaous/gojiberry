package directoryclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

var (
	ana = auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}
	bo  = auth.User{ID: "google:2", Email: "bo@x.io", VerifiedEmail: true}
)

type env struct {
	t     *testing.T
	srv   *httptest.Server
	mgr   *auth.Manager
	store docstore.Adapter
	dir   *encrypt.AdapterDirectory
	keys  map[string]ed25519.PublicKey
}

func newEnv(t *testing.T) *env {
	t.Helper()
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	signer, err := encrypt.DirectorySignerFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	store := docstore.NewMemoryAdapter()
	dir := &encrypt.AdapterDirectory{Store: store, Signer: signer}
	handler := docstore.NewServer(store, "memory").WithAuth(mgr).WithDirectory(encrypt.DirectoryService(dir)).
		WithDirectoryLimits(func(_ string, perMinute float64, burst int) docstore.Limiter {
			return docstore.NewRateLimiter(perMinute*1000, burst*1000)
		}).Handler()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, mgr: mgr, store: store, dir: dir, keys: signer.(encrypt.PublicKeyer).PublicKeys()}
}

func (e *env) client(u auth.User) *Client {
	e.t.Helper()
	tok, err := e.mgr.StartSession(u)
	if err != nil {
		e.t.Fatal(err)
	}
	return &Client{BaseURL: e.srv.URL, Token: func() string { return tok }, LogKeys: e.keys, State: &WatchState{}, Pins: MemoryPins{}}
}

func ageKey(t *testing.T) (string, string, age.Identity) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id.Recipient().String(), id.String(), id
}

func sshKey(t *testing.T) (pem []byte, id age.Identity) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pem = marshalSSH(t, priv)
	id, err = encrypt.ParseSSHIdentity(pem)
	if err != nil {
		t.Fatal(err)
	}
	return pem, id
}

func TestPublishWithProofAttestationAndLookup(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	other := e.client(bo)

	pub, _, id := ageKey(t)
	k, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Label: "recovery", Identity: id})
	if err != nil || !k.Verified || !k.Held || k.Attested || k.Seq != 1 {
		t.Fatalf("%+v %v", k, err)
	}
	pem, sshID := sshKey(t)
	k2, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Label: "laptop", Identity: sshID, SSHPrivateKey: pem})
	if err != nil || !k2.Verified || !k2.Held || !k2.Attested || k2.Attestation == nil || k2.Type != "ssh-ed25519" {
		t.Fatalf("%+v %v", k2, err)
	}
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, SSHPrivateKey: pem}); err == nil {
		t.Error("an attestation by another key")
	}
	rows, err := other.Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	for _, r := range rows {
		if !r.Verified {
			t.Errorf("%+v", r)
		}
		if r.Type == "ssh-ed25519" && (!r.Attested || !r.AttestationVerified) {
			t.Errorf("the attestation is checked by the client: %+v", r)
		}
		if r.Type == "age-x25519" && (r.Attested || r.OwnerSigned) {
			t.Errorf("%+v", r)
		}
	}
	liar := &Client{BaseURL: e.srv.URL, Token: other.Token, HTTP: mutatingClient(func(body []byte) []byte {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		for _, r := range m["keys"].([]any) {
			row := r.(map[string]any)
			if att, ok := row["attestation"].(map[string]any); ok {
				att["sig"] = "AAAA"
			}
		}
		b, _ := json.Marshal(m)
		return b
	})}
	lrows, err := liar.Lookup(ctx, "ana@x.io")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range lrows {
		if r.Type == "ssh-ed25519" && (r.AttestationVerified || r.Attested) {
			t.Errorf("a forged attestation was accepted: %+v", r)
		}
	}
	pub3, _, _ := ageKey(t)
	_, _, wrong := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub3, Identity: wrong}); err == nil {
		t.Error("a challenge opened with another key")
	}
	// Revoke.
	if _, err := c.Revoke(ctx, "ana@x.io", k.Fingerprint, nil); err != nil {
		t.Fatal(err)
	}
	rows, _ = other.Lookup(ctx, "ana@x.io")
	if len(rows) != 1 || rows[0].Fingerprint != k2.Fingerprint {
		t.Errorf("%+v", rows)
	}
	if _, err := c.Revoke(ctx, "ana@x.io", k.Fingerprint, nil); !errors.Is(err, docstore.ErrDirectoryNotFound) {
		t.Errorf("%v", err)
	}
	if err := func() error {
		_, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id})
		return err
	}(); !errors.Is(err, docstore.ErrDirectoryRevoked) {
		t.Errorf("a revoked key cannot come back yet: %v", err)
	}
	if _, err := c.Lookup(ctx, "a@b@c"); err == nil {
		t.Error("malformed address")
	}
	pk := e.client(auth.User{ID: "passkey:z", Email: "z@x.io", VerifiedEmail: true})
	if _, err := pk.Lookup(ctx, "ana@x.io"); !errors.Is(err, docstore.ErrDirectoryRefused) {
		t.Errorf("passkey session: %v", err)
	}
}

func TestOwnerEnrollmentFlowThroughTheClient(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	viewer := e.client(bo)
	owner, err := GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	// An export round-trips.
	back, err := ImportOwnerKey(ExportOwnerKey(owner))
	if err != nil || !owner.Equal(back) {
		t.Fatalf("export/import: %v", err)
	}
	if _, err := ImportOwnerKey("nonsense"); err == nil {
		t.Error("import")
	}

	enr, err := c.Enroll(ctx, "ana@x.io", owner)
	if err != nil || !enr.Enrolled || len(enr.Keys) != 1 {
		t.Fatalf("%+v %v", enr, err)
	}
	if got, err := viewer.Enrollment(ctx, "ana@x.io"); err != nil || !got.Enrolled {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := c.Enroll(ctx, "ana@x.io", owner); !errors.Is(err, docstore.ErrDirectoryEnrolled) {
		t.Errorf("enrolling twice: %v", err)
	}
	pub, _, id := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id}); !errors.Is(err, docstore.ErrDirectoryOwnerRequired) {
		t.Fatalf("no owner key: %v", err)
	}
	stranger, _ := GenerateOwnerKey()
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id, Owner: stranger}); !errors.Is(err, docstore.ErrDirectoryBadOwnerSig) {
		t.Fatalf("another owner key: %v", err)
	}
	k, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id, Owner: owner})
	if err != nil || !k.OwnerSigned || !k.Held {
		t.Fatalf("%+v %v", k, err)
	}
	if rows, err := viewer.Lookup(ctx, "ana@x.io"); err != nil || len(rows) != 1 || rows[0].OwnerSigned || !rows[0].OwnerUnpinned {
		t.Fatalf("a key seen for the first time is not confirmed: %+v %v", rows, err)
	}
	viewer.Pins = MemoryPins{}
	viewer.Pins.Set("ana@x.io", encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey)))
	rows, err := viewer.Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 1 || !rows[0].OwnerSigned || !rows[0].OwnerPinned || rows[0].OwnerChanged {
		t.Fatalf("%+v %v", rows, err)
	}
	liar := &Client{BaseURL: e.srv.URL, Token: viewer.Token, Pins: MemoryPins{}, HTTP: mutatingClient(func(body []byte) []byte {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		for _, r := range m["keys"].([]any) {
			row := r.(map[string]any)
			row["owner_signed"] = true
			row["owner"] = map[string]any{"pub": row["owner"].(map[string]any)["pub"], "time": 1, "prev": "0:" + strings.Repeat("0", 64), "sig": "AAAA"}
		}
		b, _ := json.Marshal(m)
		return b
	})}
	lr, err := liar.Lookup(ctx, "ana@x.io")
	if err != nil || len(lr) != 1 || lr[0].OwnerSigned || lr[0].DirectoryKey.OwnerSigned {
		t.Fatalf("forged evidence accepted: %+v %v", lr, err)
	}

	next, _ := GenerateOwnerKey()
	if _, err := c.Rotate(ctx, "ana@x.io", stranger, next); !errors.Is(err, docstore.ErrDirectoryBadOwnerSig) {
		t.Errorf("rotation by a key that is not the owner's: %v", err)
	}
	if enr, err := c.Rotate(ctx, "ana@x.io", owner, next); err != nil || len(enr.Keys) != 2 {
		t.Fatalf("%+v %v", enr, err)
	}
	pub2, _, id2 := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub2, Identity: id2, Owner: owner}); !errors.Is(err, docstore.ErrDirectoryBadOwnerSig) {
		t.Errorf("the retired owner key: %v", err)
	}
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub2, Identity: id2, Owner: next}); err != nil {
		t.Fatal(err)
	}
	rows, _ = viewer.Lookup(ctx, "ana@x.io")
	changed := 0
	for _, r := range rows {
		if r.OwnerChanged == r.OwnerSigned {
			t.Errorf("a row is either signed by the pinned key or flagged as changed: %+v", r)
		}
		if r.OwnerChanged {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("the viewer's pin is the first key seen: the row signed by the new key is flagged: %d", changed)
	}
	// Revoke with the owner key.
	if _, err := c.Revoke(ctx, "ana@x.io", k.Fingerprint, nil); !errors.Is(err, docstore.ErrDirectoryOwnerRequired) {
		t.Errorf("%v", err)
	}
	if _, err := c.Revoke(ctx, "ana@x.io", k.Fingerprint, next); err != nil {
		t.Errorf("%v", err)
	}

	if enr, err := c.RequestReset(ctx); err != nil || enr.ResetSince == 0 {
		t.Fatalf("%+v %v", enr, err)
	}
	third, _ := GenerateOwnerKey()
	if _, err := c.CompleteReset(ctx, "ana@x.io", third); !errors.Is(err, docstore.ErrDirectoryResetWait) {
		t.Errorf("a reset before the wait: %v", err)
	}
	if enr, err := c.CancelReset(ctx, "ana@x.io", next); err != nil || enr.ResetSince != 0 {
		t.Fatalf("%+v %v", enr, err)
	}
}

func TestPasskeyPRFKeyIsPublishedWithItsIdentityAndAttestedByTheOwner(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	r, err := encrypt.PRFRecipient(secret, "My passkey", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := encrypt.PRFIdentity(secret)
	if err != nil {
		t.Fatal(err)
	}
	k, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: r.Text, Label: "My passkey", Identity: id, Owner: owner})
	if err != nil || !k.Held || !k.OwnerSigned || k.Attested {
		t.Fatalf("a passkey key cannot attest itself; the owner signature attests it: %+v %v", k, err)
	}
	bob := e.client(bo)
	bob.Pins.Set("ana@x.io", encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey)))
	rows, _ := bob.Lookup(ctx, "ana@x.io")
	if len(rows) != 1 || !rows[0].OwnerSigned || rows[0].Attested {
		t.Errorf("%+v", rows)
	}
}

func TestWatchLogReportsEntriesTheWatcherDidNotMake(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	st := c.State
	pub, _, id := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client(bo).Publish(ctx, PublishRequest{Email: "bo@x.io", Key: func() string { p, _, _ := ageKey(t); return p }()}); err != nil {
		t.Fatal(err)
	}
	rep, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{})
	if err != nil || len(rep.Foreign) != 0 || len(rep.Entries) != 1 || rep.Head.Seq != 2 || st.Seq != 2 || len(st.Made) != 0 {
		t.Fatalf("%+v %v state %+v", rep, err, st)
	}
	if rep, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); err != nil || len(rep.Entries) != 0 || len(rep.Foreign) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	evilPub, _, _ := ageKey(t)
	if _, err := e.client(ana).Publish(ctx, PublishRequest{Email: "ana@x.io", Key: evilPub}); err != nil {
		t.Fatal(err)
	}
	rep, err = c.WatchLog(ctx, "ana@x.io", st, WatchOptions{})
	if err != nil || len(rep.Foreign) != 1 || rep.Foreign[0].Message != "someone published a key for your address" || rep.Foreign[0].Entry.Seq != 3 || rep.Foreign[0].Entry.Op != "publish" {
		t.Fatalf("%+v %v", rep, err)
	}
	owner, _ := GenerateOwnerKey()
	if _, err := e.client(ana).Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	rep, err = c.WatchLog(ctx, "ana@x.io", st, WatchOptions{})
	if err != nil || len(rep.Foreign) != 1 || !strings.Contains(rep.Foreign[0].Message, "enrolled an owner key") || !rep.Enrolled {
		t.Fatalf("trust on first use: whoever enrolls first is shown to the watcher: %+v %v", rep, err)
	}
}

func TestWatchLogOnAnEnrolledAddressFlagsWhatTheOwnerDidNotSign(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	other := e.client(ana) // another device that holds the owner key
	pub, _, id := ageKey(t)
	if _, err := other.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	st := c.State
	rep, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{Owner: owner})
	if err != nil || len(rep.Foreign) != 0 || len(rep.Violations) != 0 || len(rep.Entries) != 2 || !rep.Enrolled || len(rep.OwnerKeys) != 1 {
		t.Fatalf("an owner-signed entry from another device is the owner's: %+v %v", rep, err)
	}
	st2 := &WatchState{}
	rep, err = c.WatchLog(ctx, "ana@x.io", st2, WatchOptions{})
	if err != nil || len(rep.Foreign) != 2 {
		t.Errorf("%+v %v", rep, err)
	}

	forgeEntryWithoutOwner(t, e, "ana@x.io")
	rep, err = c.WatchLog(ctx, "ana@x.io", st, WatchOptions{Owner: owner})
	if err != nil || len(rep.Violations) != 1 || len(rep.Foreign) != 1 || !strings.Contains(rep.Violations[0].Why, "no owner signature") {
		t.Fatalf("%+v %v", rep, err)
	}
	if len(st.Pending) == 0 || st.Reported != st.Seq {
		t.Errorf("%+v", st)
	}
	data, _ := json.Marshal(st)
	var restored WatchState
	if json.Unmarshal(data, &restored) != nil || restored.Seq != st.Seq || restored.Reported != st.Reported || len(restored.Pending) != len(st.Pending) {
		t.Errorf("the state does not round-trip: %s", data)
	}
	rep, err = c.WatchLog(ctx, "ana@x.io", &restored, WatchOptions{Owner: owner})
	if err != nil || !rep.Enrolled || len(rep.Foreign) != 0 || len(rep.Violations) != 0 {
		t.Errorf("%+v %v", rep, err)
	}
}

func TestWatchLogDetectsACutOrRewrittenLog(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	for range 4 {
		p, _, id := ageKey(t)
		if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id}); err != nil {
			t.Fatal(err)
		}
	}
	st := c.State
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); err != nil {
		t.Fatal(err)
	}
	seen := *st
	if seen.Seq != 4 {
		t.Fatalf("%+v", seen)
	}
	for _, seq := range []int64{4, 3} {
		_ = e.store.Delete(ctx, fmt.Sprintf("directory-log/e/%012d", seq))
	}
	_ = e.store.Delete(ctx, "directory-log/head")
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); !errors.Is(err, ErrLogCut) {
		t.Fatalf("a truncated log: %v", err)
	}
	if st.Seq != seen.Seq || st.Hash != seen.Hash {
		t.Errorf("the state moved on an error: %+v", st)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err != nil {
		t.Errorf("a first run has nothing to compare with: %v", err)
	}

	e2 := newEnv(t)
	c2 := e2.client(ana)
	for range 3 {
		p, _, id := ageKey(t)
		if _, err := c2.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c2.WatchLog(ctx, "ana@x.io", c2.State, WatchOptions{}); err != nil {
		t.Fatal(err)
	}
	other := newEnv(t) // a different server, a different history, the same URL as far as the client knows
	oc := other.client(ana)
	for range 3 {
		p, _, id := ageKey(t)
		if _, err := oc.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id}); err != nil {
			t.Fatal(err)
		}
	}
	c2.BaseURL, c2.Token, c2.LogKeys = other.srv.URL, oc.Token, other.keys
	if _, err := c2.WatchLog(ctx, "ana@x.io", c2.State, WatchOptions{}); err == nil {
		t.Fatal("a rewritten history was accepted")
	}

	nokeys := e.client(ana)
	nokeys.LogKeys = nil
	if _, err := nokeys.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Errorf("%v", err)
	}
	nokeys.TrustServerKeys = true
	if _, err := nokeys.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err != nil {
		t.Errorf("%v", err)
	}
	wrong := e.client(ana)
	_, rogue, _ := ed25519.GenerateKey(rand.Reader)
	for kid := range wrong.LogKeys {
		wrong.LogKeys = map[string]ed25519.PublicKey{kid: rogue.Public().(ed25519.PublicKey)}
	}
	if _, err := wrong.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err == nil {
		t.Error("log keys that are not the server's")
	}
}

func TestWatchLogReadsAllPages(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	for i := range 230 { // more than a page of 200
		u := auth.User{ID: fmt.Sprintf("google:u%d", i), Email: fmt.Sprintf("u%d@x.io", i), VerifiedEmail: true}
		p, _, _ := ageKey(t)
		r, err := encrypt.ParseAgeRecipient(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.dir.Publish(ctx, u, r); err != nil {
			t.Fatal(err)
		}
	}
	p, _, id := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id}); err != nil {
		t.Fatal(err)
	}
	rep, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{})
	if err != nil || rep.Head.Seq != 231 || c.State.Seq != 231 || len(rep.Entries) != 1 {
		t.Fatalf("%+v %v %+v", rep, err, c.State)
	}
	p2, _, id2 := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p2, Identity: id2}); err != nil {
		t.Fatal(err)
	}
	if rep, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{}); err != nil || len(rep.Entries) != 1 || len(rep.Foreign) != 0 || c.State.Seq != 232 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func marshalSSH(t *testing.T, priv ed25519.PrivateKey) []byte {
	t.Helper()
	blk, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(blk)
}

func mutatingClient(f func([]byte) []byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err != nil || !strings.HasPrefix(r.URL.Path, "/directory/lookup") {
			return res, err
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(f(body)))
		res.ContentLength = -1
		return res, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func forgeEntryWithoutOwner(t *testing.T, e *env, email string) {
	t.Helper()
	ctx := context.Background()
	h, err := e.dir.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry := encrypt.SignLogEntry(e.dir.Signer, encrypt.LogEntry{Seq: h.Seq + 1, Prev: h.Hash, At: time.Now().Unix(), Op: encrypt.OpPublish,
		Address: e.dir.LogAddressFor(email), Fingerprint: "SHA256:rogue"})
	data, _ := json.Marshal(entry)
	if err := e.store.Save(ctx, fmt.Sprintf("directory-log/e/%012d", entry.Seq), data); err != nil {
		t.Fatal(err)
	}
}

func TestWatchLogFlagsAnEnrollmentTheServerDeletedOrChanged(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	rep, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{Owner: owner})
	if err != nil || len(rep.Violations) != 0 || !rep.Enrolled {
		t.Fatalf("%+v %v", rep, err)
	}
	metas, _ := docstore.ListPrefix(ctx, e.store, docstore.DirectoryEnrollPrefix)
	if len(metas) != 1 {
		t.Fatal(metas)
	}
	if err := e.store.Delete(ctx, metas[0].ID); err != nil {
		t.Fatal(err)
	}
	rep, err = c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{Owner: owner})
	if err != nil || len(rep.Violations) != 1 || !strings.Contains(rep.Violations[0].Why, "directory says it has none") {
		t.Fatalf("%+v %v", rep, err)
	}
	if got, err := e.client(bo).Enrollment(ctx, "ana@x.io"); err != nil || got.Enrolled {
		t.Errorf("%+v %v", got, err)
	}
}

func TestRequireOwnerRefusesToRelyOnAnUnenrolledAddress(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	viewer := e.client(bo)
	viewer.RequireOwner = true

	pub, _, id := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id}); err != nil {
		t.Fatal(err)
	}
	rows, err := viewer.Lookup(ctx, "ana@x.io")
	if !errors.Is(err, ErrUnenrolled) || len(rows) != 1 {
		t.Fatalf("an unenrolled address: %d rows, err %v", len(rows), err)
	}
	if rows, err := viewer.Lookup(ctx, "nobody@x.io"); err != nil || len(rows) != 0 {
		t.Errorf("no keys: %v %v", rows, err)
	}
	viewer.RequireOwner = false
	if _, err := viewer.Lookup(ctx, "ana@x.io"); err != nil {
		t.Errorf("without the option: %v", err)
	}
	owner, err := GenerateOwnerKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	pub2, _, id2 := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub2, Identity: id2, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	viewer.RequireOwner = true
	rows, err = viewer.Lookup(ctx, "ana@x.io")
	for _, r := range rows {
		if r.Fingerprint != "" && !r.OwnerEnrolled {
			t.Errorf("a row of an enrolled address is not marked enrolled: %+v", r)
		}
	}
	if err != nil {
		t.Errorf("an enrolled address: %v", err)
	}
	e.dir.RequireEnrollment = true
	other := e.client(auth.User{ID: "google:9", Email: "cy@x.io", VerifiedEmail: true})
	pub3, _, id3 := ageKey(t)
	if _, err := other.Publish(ctx, PublishRequest{Email: "cy@x.io", Key: pub3, Identity: id3}); !errors.Is(err, docstore.ErrDirectoryEnrollmentRequired) {
		t.Errorf("server RequireEnrollment: %v", err)
	}
}

func TestLookupFlagsAChangedLogNumberOnAnUnenrolledAddress(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	viewer := e.client(bo)
	pub, _, id := ageKey(t)
	k, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub, Identity: id})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := viewer.Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 1 || rows[0].SeqChanged {
		t.Fatalf("first look: %+v %v", rows, err)
	}
	if again, _ := viewer.Lookup(ctx, "ana@x.io"); again[0].SeqChanged {
		t.Error("the same number again was flagged")
	}
	liar := &Client{BaseURL: e.srv.URL, Token: viewer.Token, State: viewer.State, Pins: viewer.Pins, HTTP: mutatingClient(func(body []byte) []byte {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		for _, r := range m["keys"].([]any) {
			r.(map[string]any)["seq"] = float64(k.Seq + 40)
		}
		b, _ := json.Marshal(m)
		return b
	})}
	rows, err = liar.Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 1 || !rows[0].SeqChanged {
		t.Fatalf("another number must be flagged: %+v %v", rows, err)
	}
	if back, _ := viewer.Lookup(ctx, "ana@x.io"); back[0].SeqChanged {
		t.Error("the first number is the reference")
	}
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	pub2, _, id2 := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: pub2, Identity: id2, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	rows, _ = liar.Lookup(ctx, "ana@x.io")
	for _, r := range rows {
		if r.OwnerSigned && r.SeqChanged {
			t.Errorf("a signed row was flagged: %+v", r)
		}
	}
}
