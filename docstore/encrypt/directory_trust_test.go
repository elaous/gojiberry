package encrypt

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

var (
	ana = auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}
	bo  = auth.User{ID: "google:2", Email: "bo@x.io", VerifiedEmail: true}
)

// testClock is a clock the tests move.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)} }
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *testClock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// ageKey is a key pair of the kind a passkey or a recovery key makes.
func ageKey(t *testing.T) (Recipient, age.Identity) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseAgeRecipient(id.Recipient().String())
	if err != nil {
		t.Fatal(err)
	}
	return r, id
}

// sshKey is an ssh-ed25519 key pair: the private PEM, the public line, the parsed recipient and
// the age identity that opens what is encrypted to it.
func edSSHKey(t *testing.T) (pemBytes []byte, r Recipient, id age.Identity) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(priv, "test")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes = pem.EncodeToMemory(blk)
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	r, err = ParseSSHRecipient(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))))
	if err != nil {
		t.Fatal(err)
	}
	id, err = ParseSSHIdentity(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	return pemBytes, r, id
}

func newTestDirectory(t *testing.T) (*AdapterDirectory, docstore.Adapter, *testClock) {
	t.Helper()
	clk := newTestClock()
	store := docstore.NewMemoryAdapter()
	return &AdapterDirectory{Store: store, Signer: testSigner(t, 1), Now: clk.Now}, store, clk
}

// --- rotation -------------------------------------------------------------------------------

func TestDirectoryRotationKeepsRecordsValidAndResignMovesThem(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	oldSigner := testSigner(t, 1)
	d1 := &AdapterDirectory{Store: store, Signer: oldSigner}
	k1, k2 := newRecovery(t), newRecovery(t)
	for _, k := range []Recipient{k1, k2} {
		if err := d1.Publish(ctx, ana, k); err != nil {
			t.Fatal(err)
		}
	}
	// Rotation: a new current key, the old one for verification only.
	newSeed := []byte(strings.Repeat("\x02", 32))
	rotated, err := DirectorySignerFromSeed(newSeed, []byte(strings.Repeat("\x01", 32)))
	if err != nil {
		t.Fatal(err)
	}
	if rotated.KeyID() == oldSigner.KeyID() {
		t.Fatal("two keys, two IDs")
	}
	d2 := &AdapterDirectory{Store: store, Signer: rotated}
	got, err := d2.Lookup(ctx, "ana@x.io")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, k := range got {
		if !k.Verified || k.Kid != oldSigner.KeyID() {
			t.Errorf("a record signed before the rotation still verifies, under its own key ID: %+v", k)
		}
	}
	// A directory that has only the new key does not know the old records (named and signed by
	// the old key): the retired key must stay listed until Resign has run.
	if got, _ := (&AdapterDirectory{Store: store, Signer: testSigner(t, 2)}).Lookup(ctx, "ana@x.io"); len(got) != 0 {
		t.Errorf("without the retired key the old records are not found: %v", got)
	}

	// Publishing again re-signs one record with the current key and moves it to the new name.
	if err := d2.Publish(ctx, ana, k1); err != nil {
		t.Fatal(err)
	}
	got, _ = d2.Lookup(ctx, "ana@x.io")
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	kids := map[string]string{}
	for _, k := range got {
		kids[k.Fingerprint] = k.Kid
		if !k.Verified {
			t.Errorf("%+v", k)
		}
	}
	if kids[k1.Fingerprint] != rotated.KeyID() || kids[k2.Fingerprint] != oldSigner.KeyID() {
		t.Errorf("re-signed on republish: %v", kids)
	}
	if _, err := store.Load(ctx, (&AdapterDirectory{Signer: oldSigner}).directoryID("ana@x.io", k1.Fingerprint)); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the old copy of the republished record is gone: %v", err)
	}

	// Resign finishes the job; a second run has nothing to do.
	if rep, err := d2.Resign(ctx); err != nil || rep.Resigned != 1 {
		t.Fatalf("Resign = %+v %v, want 1", rep, err)
	}
	if rep, err := d2.Resign(ctx); err != nil || rep.Resigned != 0 {
		t.Fatalf("second Resign = %+v %v", rep, err)
	}
	// Now the retired key can go.
	d3 := &AdapterDirectory{Store: store, Signer: mustSigner(t, newSeed)}
	got, _ = d3.Lookup(ctx, "ana@x.io")
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	for _, k := range got {
		if !k.Verified || k.Kid != rotated.KeyID() {
			t.Errorf("%+v", k)
		}
	}
	// What the publisher proved survives: nothing was lost by re-signing (Seq stays).
	for _, k := range got {
		if k.Seq == 0 {
			t.Errorf("the log number is kept: %+v", k)
		}
	}
	// The log was not touched by Resign: two publishes and a republish.
	l, _ := d3.ReadLog(ctx, 0, 100)
	if len(l.Entries) != 3 {
		t.Errorf("log entries = %d, want 3", len(l.Entries))
	}
}

func mustSigner(t *testing.T, seed []byte, prev ...[]byte) DirectorySigner {
	t.Helper()
	s, err := DirectorySignerFromSeed(seed, prev...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDirectorySignerRotationFromTheEnvironment(t *testing.T) {
	a, b, c := strings.Repeat("11", 32), strings.Repeat("22", 32), strings.Repeat("33", 32)
	t.Setenv(DirectoryKeyEnv, a)
	t.Setenv(DirectoryKeyPrevEnv, b+", "+c+" ,")
	sg, err := DirectorySignerFromEnv()
	if err != nil || sg == nil {
		t.Fatalf("%v %v", sg, err)
	}
	if got := len(sg.Pseudonyms("x")); got != 3 {
		t.Errorf("current and two retired keys name records: %d", got)
	}
	if sg.Pseudonyms("x")[0] != sg.Pseudonym("x") {
		t.Error("the current key's name comes first")
	}
	msg := []byte("m")
	old := mustHexSigner(t, c)
	if sig := old.Sign(msg); !sg.Verify(old.KeyID(), msg, sig) || !sg.Verify("", msg, sig) || sg.Verify(sg.KeyID(), msg, sig) {
		t.Error("a retired key verifies under its own ID (or any, with none), not under another's")
	}
	if len(sg.(PublicKeyer).PublicKeys()) != 3 {
		t.Error("the public keys of all three")
	}
	// The current key listed as retired is not a second key; a bad retired key is an error, and
	// the retired keys alone do not turn the directory on.
	t.Setenv(DirectoryKeyPrevEnv, a)
	if sg, _ := DirectorySignerFromEnv(); len(sg.Pseudonyms("x")) != 1 {
		t.Error("a key listed twice counts once")
	}
	t.Setenv(DirectoryKeyPrevEnv, "zz")
	if _, err := DirectorySignerFromEnv(); err == nil {
		t.Error("a retired key that is not hex is an error")
	}
	t.Setenv(DirectoryKeyPrevEnv, "abcd")
	if _, err := DirectorySignerFromEnv(); err == nil {
		t.Error("a short retired key is an error")
	}
	t.Setenv(DirectoryKeyEnv, "")
	t.Setenv(DirectoryKeyPrevEnv, b)
	if sg, err := DirectorySignerFromEnv(); sg != nil || err != nil {
		t.Errorf("retired keys without a current one: %v %v", sg, err)
	}
}

func mustHexSigner(t *testing.T, hexSeed string) DirectorySigner {
	t.Helper()
	s, err := DirectorySignerFromHex(hexSeed)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// --- revocation -----------------------------------------------------------------------------

func TestDirectoryRevokeRemovesTheKeyAndRemembersIt(t *testing.T) {
	ctx := context.Background()
	d, store, clk := newTestDirectory(t)
	k1, k2 := newRecovery(t), newRecovery(t)
	for _, k := range []Recipient{k1, k2} {
		if err := d.Publish(ctx, ana, k); err != nil {
			t.Fatal(err)
		}
	}
	// Only the signed-in owner of the address can revoke; another address cannot reach it.
	if _, err := d.Revoke(ctx, bo, k1.Fingerprint, nil); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("another user's revoke: %v", err)
	}
	for name, u := range map[string]auth.User{"passkey": {ID: "passkey:z", Email: "ana@x.io", VerifiedEmail: true}, "unverified": {ID: "google:1", Email: "ana@x.io"}} {
		if _, err := d.Revoke(ctx, u, k1.Fingerprint, nil); !errors.Is(err, ErrNotPublishable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := d.Revoke(ctx, ana, "SHA256:nothing", nil); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("unknown key: %v", err)
	}
	if got, _ := d.Lookup(ctx, "ana@x.io"); len(got) != 2 {
		t.Fatalf("nothing was revoked yet: %v", got)
	}

	clk.Add(time.Minute)
	k, err := d.Revoke(ctx, ana, k1.Fingerprint, nil)
	if err != nil || !k.Revoked || k.Fingerprint != k1.Fingerprint || k.RevokedAt != clk.Now().Unix() {
		t.Fatalf("%+v %v", k, err)
	}
	// The key is gone from lookup at once; the other stays.
	got, _ := d.Lookup(ctx, "ana@x.io")
	if len(got) != 1 || got[0].Fingerprint != k2.Fingerprint || got[0].Revoked {
		t.Fatalf("%v", got)
	}
	// Asked for, it shows, flagged and verified (the tombstone is signed).
	got, _ = d.LookupWith(ctx, "ana@x.io", LookupOptions{IncludeRevoked: true})
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	for _, g := range got {
		if g.Fingerprint == k1.Fingerprint && (!g.Revoked || g.RevokedAt != clk.Now().Unix() || !g.Verified) {
			t.Errorf("%+v", g)
		}
		if g.Fingerprint == k2.Fingerprint && g.Revoked {
			t.Errorf("%+v", g)
		}
	}
	// Revoking again finds nothing to revoke. The fingerprint also works in its path form.
	if _, err := d.Revoke(ctx, ana, k1.Fingerprint, nil); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("twice: %v", err)
	}
	if _, err := d.Revoke(ctx, ana, fingerprintSegment(k2.Fingerprint), nil); err != nil {
		t.Errorf("path form: %v", err)
	}

	// A replayed publish of the revoked key is refused within the window...
	clk.Add(23 * time.Hour)
	if err := d.Publish(ctx, ana, k1); !errors.Is(err, ErrRevoked) {
		t.Fatalf("replay within the window: %v", err)
	}
	if got, _ := d.Lookup(ctx, "ana@x.io"); len(got) != 0 {
		t.Errorf("%v", got)
	}
	// ...and allowed after it; the old tombstone is replaced.
	clk.Add(2 * time.Hour)
	if got, _ := d.LookupWith(ctx, "ana@x.io", LookupOptions{IncludeRevoked: true}); len(got) != 0 {
		t.Errorf("the tombstones expired: %v", got)
	}
	if err := d.Publish(ctx, ana, k1); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	if got, _ := d.Lookup(ctx, "ana@x.io"); len(got) != 1 || got[0].Revoked || !got[0].Verified {
		t.Errorf("%v", got)
	}
	// The expired tombstone of k2 is pruned by the publish, or by Sweep.
	n, err := d.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	metas, _ := docstore.ListPrefix(ctx, store, "directory/")
	if len(metas) != 1 {
		t.Errorf("after the sweep (%d deleted): %v", n, metas)
	}
	// The log says all of it: 2 publishes, 2 revokes, 1 publish.
	l, _ := d.ReadLog(ctx, 0, 100)
	var ops []string
	for _, e := range l.Entries {
		ops = append(ops, e.Op)
	}
	if strings.Join(ops, ",") != "publish,publish,revoke,revoke,publish" {
		t.Errorf("log: %v", ops)
	}
}

func TestDirectoryTombstonesCountAgainstTheCapAndForgedOnesAreIgnored(t *testing.T) {
	ctx := context.Background()
	d, store, _ := newTestDirectory(t)
	d.MaxKeys = 2
	k1, k2, k3 := newRecovery(t), newRecovery(t), newRecovery(t)
	_ = d.Publish(ctx, ana, k1)
	_ = d.Publish(ctx, ana, k2)
	if _, err := d.Revoke(ctx, ana, k1.Fingerprint, nil); err != nil {
		t.Fatal(err)
	}
	// Cycling keys does not get past the cap within the window: the revoked one still counts.
	if err := d.Publish(ctx, ana, k3); !errors.Is(err, ErrTooManyKeys) {
		t.Errorf("%v", err)
	}
	// Someone with write access but no key writes a tombstone for k2: not believed.
	id := d.directoryID("ana@x.io", k2.Fingerprint)
	doc, _ := store.Load(ctx, id)
	var rec directoryRecord
	_ = json.Unmarshal(doc.Data, &rec)
	rec.Revoked = d.now().Unix()
	b, _ := json.Marshal(rec)
	_ = store.Save(ctx, id, b)
	got, _ := d.LookupWith(ctx, "ana@x.io", LookupOptions{IncludeRevoked: true})
	for _, g := range got {
		if g.Fingerprint == k2.Fingerprint {
			t.Errorf("a forged tombstone hides nothing and shows nothing: %+v", g)
		}
	}
	// An unsigned tombstone does not block publishing either.
	b, _ = json.Marshal(directoryRecord{V: 3, Key: k3.Text, Publisher: "google:1", Provider: "google", Email: "ana@x.io", Revoked: d.now().Unix()})
	_ = store.Save(ctx, d.directoryID("ana@x.io", k3.Fingerprint), b)
	_ = store.Delete(ctx, id)
	if err := d.Publish(ctx, ana, k3); err != nil {
		t.Errorf("a forged tombstone does not block: %v", err)
	}
}

// --- proof of possession --------------------------------------------------------------------

func TestDirectoryProofOfPossession(t *testing.T) {
	ctx := context.Background()
	d, _, clk := newTestDirectory(t)
	for name, mk := range map[string]func() (Recipient, age.Identity){
		"age": func() (Recipient, age.Identity) { return ageKey(t) },
		"ssh": func() (Recipient, age.Identity) { _, r, id := edSSHKey(t); return r, id },
	} {
		key, id := mk()
		c, err := d.Challenge(ctx, ana, key.Text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.Expires != clk.Now().Add(ChallengeTTL).Unix() || c.Token == "" || c.Encrypted == "" {
			t.Fatalf("%s: %+v", name, c)
		}
		// Whoever lacks the private key cannot answer.
		_, otherID := ageKey(t)
		if _, err := OpenChallenge(c, otherID); err == nil {
			t.Errorf("%s: another key opened the challenge", name)
		}
		proof, err := OpenChallenge(c, id)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		k, err := d.PublishWith(ctx, ana, key, PublishOptions{Challenge: proof.Token, Nonce: proof.Nonce})
		if err != nil || !k.Held || !k.Verified || k.Attested {
			t.Fatalf("%s: %+v %v", name, k, err)
		}
		got, _ := d.Lookup(ctx, "ana@x.io")
		found := false
		for _, g := range got {
			if g.Fingerprint == key.Fingerprint {
				found = g.Held && g.Verified
			}
		}
		if !found {
			t.Errorf("%s: lookup says held: %v", name, got)
		}
		// A challenge works once: the same answer again is a replay.
		if _, err := d.PublishWith(ctx, ana, key, PublishOptions{Challenge: proof.Token, Nonce: proof.Nonce}); !errors.Is(err, ErrBadProof) {
			t.Errorf("%s: replay: %v", name, err)
		}
	}
	// A key published without a proof is not Held, and RequireProof refuses it.
	plainKey := newRecovery(t)
	k, err := d.PublishWith(ctx, ana, plainKey, PublishOptions{})
	if err != nil || k.Held {
		t.Fatalf("%+v %v", k, err)
	}
	d.RequireProof = true
	if _, err := d.PublishWith(ctx, ana, newRecovery(t), PublishOptions{}); !errors.Is(err, ErrProofRequired) {
		t.Errorf("%v", err)
	}
	if err := d.Publish(ctx, ana, newRecovery(t)); !errors.Is(err, ErrProofRequired) {
		t.Errorf("Publish too: %v", err)
	}
	d.RequireProof = false
}

func TestDirectoryProofIsBoundToUserKeyAndTime(t *testing.T) {
	ctx := context.Background()
	d, store, clk := newTestDirectory(t)
	key, id := ageKey(t)
	other, otherID := ageKey(t)
	c, _ := d.Challenge(ctx, ana, key.Text)
	proof, _ := OpenChallenge(c, id)
	publish := func(u auth.User, k Recipient, p docstore.DirectoryProof) error {
		_, err := d.PublishWith(ctx, u, k, PublishOptions{Challenge: p.Token, Nonce: p.Nonce})
		return err
	}
	// Another user cannot use ana's answer, nor ana for another key, nor with a wrong nonce or
	// a token made up or altered.
	if err := publish(bo, key, proof); !errors.Is(err, ErrBadProof) {
		t.Errorf("another user: %v", err)
	}
	if err := publish(ana, other, proof); !errors.Is(err, ErrBadProof) {
		t.Errorf("another key: %v", err)
	}
	if err := publish(ana, key, docstore.DirectoryProof{Token: proof.Token, Nonce: strings.Repeat("0", 32)}); !errors.Is(err, ErrBadProof) {
		t.Errorf("wrong nonce: %v", err)
	}
	pl, _, _ := strings.Cut(proof.Token, ".")
	for name, tok := range map[string]string{"empty": "", "no dot": "abc", "bad base64": "!!.!!", "unsigned": pl + ".", "other signature": pl + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 64))} {
		if err := publish(ana, key, docstore.DirectoryProof{Token: tok, Nonce: proof.Nonce}); !errors.Is(err, ErrBadProof) && (tok != "" || err != nil) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A token another server (key) made does not count.
	d2 := &AdapterDirectory{Store: docstore.NewMemoryAdapter(), Signer: testSigner(t, 9), Now: clk.Now}
	c2, _ := d2.Challenge(ctx, ana, key.Text)
	p2, _ := OpenChallenge(c2, id)
	if err := publish(ana, key, p2); !errors.Is(err, ErrBadProof) {
		t.Errorf("another directory's token: %v", err)
	}
	// None of those used the challenge up: the right answer still works, within its time...
	clk.Add(ChallengeTTL - time.Second)
	if err := publish(ana, key, proof); err != nil {
		t.Fatalf("%v", err)
	}
	// ...and not after it.
	c3, _ := d.Challenge(ctx, ana, other.Text)
	p3, _ := OpenChallenge(c3, otherID)
	clk.Add(ChallengeTTL + time.Second)
	if err := publish(ana, other, p3); !errors.Is(err, ErrBadProof) || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired: %v", err)
	}
	// Used challenges are remembered until they could no longer be answered, then swept.
	if metas, _ := docstore.ListPrefix(ctx, store, popPrefix); len(metas) != 1 {
		t.Fatalf("used challenge records: %v", metas)
	}
	clk.Add(popGrace + time.Second)
	if n, err := d.Sweep(ctx); err != nil || n != 1 {
		t.Errorf("Sweep = %d %v", n, err)
	}
	// Refusals: who may not publish may not ask; a non-key is refused.
	if _, err := d.Challenge(ctx, auth.User{ID: "passkey:1", Email: "a@x.io", VerifiedEmail: true}, key.Text); !errors.Is(err, ErrNotPublishable) {
		t.Errorf("%v", err)
	}
	if _, err := d.Challenge(ctx, ana, "garbage"); !errors.Is(err, errBadPublicKey) {
		t.Errorf("%v", err)
	}
	if _, err := (&AdapterDirectory{Store: store}).Challenge(ctx, ana, key.Text); !errors.Is(err, ErrDirectoryOff) {
		t.Errorf("%v", err)
	}
}

func TestDirectoryProofIsSingleUseAcrossConcurrentPublishes(t *testing.T) {
	ctx := context.Background()
	d, _, _ := newTestDirectory(t)
	key, id := ageKey(t)
	c, _ := d.Challenge(ctx, ana, key.Text)
	proof, _ := OpenChallenge(c, id)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := d.PublishWith(ctx, ana, key, PublishOptions{Challenge: proof.Token, Nonce: proof.Nonce}); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Errorf("one challenge, %d publishes", ok)
	}
}

// --- attestation ----------------------------------------------------------------------------

func TestDirectoryAttestationOfAnSSHKey(t *testing.T) {
	ctx := context.Background()
	d, _, clk := newTestDirectory(t)
	pemBytes, key, id := edSSHKey(t)
	pub, att, err := AttestSSH(pemBytes, " Ana@X.io ", clk.Now())
	if err != nil || pub != key.Text {
		t.Fatalf("%q %v", pub, err)
	}
	// Anyone can check it, with the key text alone.
	if !VerifyAttestation(key.Text, "ana@x.io", att) {
		t.Fatal("the attestation verifies")
	}
	if VerifyAttestation(key.Text, "bo@x.io", att) || VerifyAttestation(key.Text, "ana@x.io", docstore.DirectoryAttestation{At: att.At + 1, Sig: att.Sig}) {
		t.Error("another address or time does not")
	}
	other, _ := ageKey(t)
	if VerifyAttestation(other.Text, "ana@x.io", att) || VerifyAttestation("garbage", "ana@x.io", att) || VerifyAttestation(key.Text, "ana@x.io", docstore.DirectoryAttestation{At: att.At, Sig: "!!"}) {
		t.Error("another key, a non-key and a non-signature do not")
	}

	c, _ := d.Challenge(ctx, ana, key.Text)
	proof, _ := OpenChallenge(c, id)
	k, err := d.PublishWith(ctx, ana, key, PublishOptions{Challenge: proof.Token, Nonce: proof.Nonce, Attestation: &att})
	if err != nil || !k.Attested || !k.Held || !k.Verified || k.Attestation == nil || *k.Attestation != att {
		t.Fatalf("%+v %v", k, err)
	}
	got, _ := d.Lookup(ctx, "ana@x.io")
	if len(got) != 1 || !got[0].Attested || got[0].Attestation == nil || !VerifyAttestation(got[0].Text, "ana@x.io", *got[0].Attestation) {
		t.Fatalf("%+v", got)
	}

	// Refusals: an attestation for another address, a stale one, one from another key, and a key
	// that cannot attest.
	_, otherAtt, _ := AttestSSH(pemBytes, "bo@x.io", clk.Now())
	stale, _ := mustAttest(t, pemBytes, "ana@x.io", clk.Now().Add(-11*time.Minute))
	future, _ := mustAttest(t, pemBytes, "ana@x.io", clk.Now().Add(11*time.Minute))
	pem2, key2, _ := edSSHKey(t)
	_, att2, _ := AttestSSH(pem2, "ana@x.io", clk.Now())
	for name, c := range map[string]struct {
		key Recipient
		att docstore.DirectoryAttestation
	}{
		"another address": {key, otherAtt}, "stale": {key, stale}, "from the future": {key, future},
		"another key's": {key, att2}, "age key": {other, att}, "not base64": {key, docstore.DirectoryAttestation{At: att.At, Sig: "!!"}},
	} {
		a := c.att
		if _, err := d.PublishWith(ctx, ana, c.key, PublishOptions{Attestation: &a}); !errors.Is(err, ErrBadAttestation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	_ = key2
	// Without an attestation an ssh key is published, just not Attested.
	k, err = d.PublishWith(ctx, ana, key2, PublishOptions{})
	if err != nil || k.Attested || k.Attestation != nil {
		t.Errorf("%+v %v", k, err)
	}
}

func mustAttest(t *testing.T, pemBytes []byte, email string, at time.Time) (docstore.DirectoryAttestation, error) {
	t.Helper()
	_, att, err := AttestSSH(pemBytes, email, at)
	if err != nil {
		t.Fatal(err)
	}
	return att, nil
}

func TestAttestSSHRefusesKeysThatCannotAttest(t *testing.T) {
	if _, _, err := AttestSSH([]byte("junk"), "a@x.io", time.Now()); err == nil {
		t.Error("junk")
	}
	if _, err := Attest(ed25519.PrivateKey(make([]byte, 64)), "a@x.io", time.Now()); err != nil {
		t.Errorf("a well-formed private key signs: %v", err)
	}
}

// The directory key alone cannot make a key it does not hold Held or Attested, and an attestation
// cannot be moved to another record: the marks are recomputed on every lookup.
func TestDirectoryAttestationCannotBeForgedWithTheServerKey(t *testing.T) {
	ctx := context.Background()
	d, store, clk := newTestDirectory(t)
	pemBytes, victimKey, _ := edSSHKey(t)
	_, attackerKey, _ := edSSHKey(t)
	_, victimAtt, _ := AttestSSH(pemBytes, "ana@x.io", clk.Now())

	// A forger with the server's signing key writes a perfectly signed record for the victim's
	// key under bo's address, with ana's attestation attached: signed, but not attested (the
	// attestation binds the key to ana's address, not bo's), and never Held.
	forge := func(email string, key Recipient, att *docstore.DirectoryAttestation) {
		rec := directoryRecord{Key: key.Text, Publisher: "google:2", Provider: "google", Email: email, At: clk.Now().Unix(), Held: false}
		if att != nil {
			rec.Attest, rec.AttestAt = att.Sig, att.At
		}
		id := d.directoryID(email, key.Fingerprint)
		b, _ := json.Marshal(d.sign(rec, id))
		if err := store.Save(ctx, id, b); err != nil {
			t.Fatal(err)
		}
	}
	forge("bo@x.io", victimKey, &victimAtt)
	got, _ := d.Lookup(ctx, "bo@x.io")
	if len(got) != 1 || !got[0].Verified || got[0].Attested || got[0].Held {
		t.Fatalf("a signed record with someone else's attestation: %+v", got)
	}
	// The same for the attacker's own key under ana's address with no attestation at all: the
	// server's signature gives Verified and nothing more.
	forge("ana@x.io", attackerKey, nil)
	got, _ = d.Lookup(ctx, "ana@x.io")
	if len(got) != 1 || !got[0].Verified || got[0].Attested || got[0].Held {
		t.Fatalf("%+v", got)
	}
	// Setting Held in a record the publisher did not prove is the one thing the signature does
	// cover: that is the server's word, which is why a client that must not trust the server
	// checks Attestation itself (VerifyAttestation) and the log (Seq).
	rec := directoryRecord{Key: victimKey.Text, Publisher: "google:1", Provider: "google", Email: "ana@x.io", At: clk.Now().Unix(), Held: true}
	id := d.directoryID("ana@x.io", victimKey.Fingerprint)
	b, _ := json.Marshal(d.sign(rec, id))
	_ = store.Save(ctx, id, b)
	got, _ = d.Lookup(ctx, "ana@x.io")
	for _, g := range got {
		if g.Fingerprint == victimKey.Fingerprint && (g.Attestation != nil || g.Attested || g.Seq != 0) {
			t.Errorf("no attestation and no log entry to show: %+v", g)
		}
	}
}

// --- transparency log -----------------------------------------------------------------------

func logKeys(t *testing.T, d *AdapterDirectory) map[string]ed25519.PublicKey {
	t.Helper()
	return d.Signer.(PublicKeyer).PublicKeys()
}

func TestDirectoryLogChainsAndDetectsRewrites(t *testing.T) {
	ctx := context.Background()
	d, store, clk := newTestDirectory(t)
	var fps []string
	for range 4 {
		k := newRecovery(t)
		fps = append(fps, k.Fingerprint)
		clk.Add(time.Second)
		if err := d.Publish(ctx, ana, k); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = d.Revoke(ctx, ana, fps[0], nil)
	l, err := d.ReadLog(ctx, 0, 100)
	if err != nil || len(l.Entries) != 5 {
		t.Fatalf("%v %v", l, err)
	}
	keys := logKeys(t, d)
	if len(l.Keys) != 1 {
		t.Errorf("keys: %v", l.Keys)
	}
	last, err := VerifyLog(l.Entries, "", 0, keys)
	if err != nil || last != l.Entries[4].Hash {
		t.Fatalf("%v", err)
	}
	if l.Entries[0].Prev != zeroHash || l.Entries[0].Address != d.LogAddressFor(" ANA@x.io") || l.Entries[4].Op != "revoke" || l.Entries[4].Fingerprint != fps[0] {
		t.Errorf("%+v", l.Entries)
	}
	if strings.Contains(l.Entries[0].Address, "ana") {
		t.Error("the address is hashed")
	}
	// An auditor that kept entry 3's hash checks only what came after, and a window of size 1.
	part, _ := d.ReadLog(ctx, 3, 100)
	if len(part.Entries) != 2 || part.Entries[0].Seq != 4 {
		t.Fatalf("since: %v", part.Entries)
	}
	if h, err := VerifyLog(part.Entries, l.Entries[2].Hash, 3, keys); err != nil || h != last {
		t.Errorf("continuation: %v", err)
	}
	if one, _ := d.ReadLog(ctx, 0, 1); len(one.Entries) != 1 {
		t.Errorf("limit: %d", len(one.Entries))
	}
	if none, _ := d.ReadLog(ctx, 5, 10); len(none.Entries) != 0 {
		t.Errorf("past the end: %v", none.Entries)
	}
	// ...but a continuation checked against the wrong earlier hash fails.
	if _, err := VerifyLog(part.Entries, l.Entries[1].Hash, 3, keys); !errors.Is(err, ErrLogBroken) {
		t.Errorf("wrong anchor: %v", err)
	}

	mutate := func(f func(es []LogEntry) []LogEntry) error {
		es := append([]LogEntry(nil), l.Entries...)
		_, err := VerifyLog(f(es), "", 0, keys)
		return err
	}
	for name, f := range map[string]func([]LogEntry) []LogEntry{
		"an entry changed":    func(es []LogEntry) []LogEntry { es[2].Fingerprint = "SHA256:other"; return es },
		"an entry removed":    func(es []LogEntry) []LogEntry { return append(es[:2:2], es[3:]...) },
		"entries reordered":   func(es []LogEntry) []LogEntry { es[1], es[2] = es[2], es[1]; return es },
		"the first one cut":   func(es []LogEntry) []LogEntry { return es[1:] },
		"hash fixed up":       func(es []LogEntry) []LogEntry { es[2].Fingerprint = "x"; es[2].Hash = logHash(es[2]); return es },
		"a signature swapped": func(es []LogEntry) []LogEntry { es[2].Sig = es[1].Sig; return es },
		"an unknown key":      func(es []LogEntry) []LogEntry { es[2].Kid = "nope"; return es },
		"bad signature text":  func(es []LogEntry) []LogEntry { es[2].Sig = "!!"; return es },
		"a forged tail": func(es []LogEntry) []LogEntry {
			e := LogEntry{Seq: 6, Prev: es[4].Hash, Op: "publish", Kid: es[4].Kid}
			e.Hash = logHash(e)
			return append(es, e)
		},
	} {
		if err := mutate(f); !errors.Is(err, ErrLogBroken) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Another key's signatures do not verify.
	if _, err := VerifyLog(l.Entries, "", 0, map[string]ed25519.PublicKey{l.Entries[0].Kid: logKeys(t, &AdapterDirectory{Signer: testSigner(t, 5)})[testSigner(t, 5).KeyID()]}); !errors.Is(err, ErrLogBroken) {
		t.Errorf("wrong key: %v", err)
	}
	// A rewritten store entry is what ReadLog then serves, and the auditor's check catches it.
	doc, _ := store.Load(ctx, logEntryID(2))
	var e LogEntry
	_ = json.Unmarshal(doc.Data, &e)
	e.Op = "revoke"
	b, _ := json.Marshal(e)
	_ = store.Save(ctx, logEntryID(2), b)
	l2, _ := d.ReadLog(ctx, 0, 100)
	if _, err := VerifyLog(l2.Entries, "", 0, keys); !errors.Is(err, ErrLogBroken) {
		t.Errorf("rewritten in the store: %v", err)
	}
}

func TestDirectoryLogAppendsStayOrderedAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	signer := testSigner(t, 1)
	// Three replicas of the directory on one (conditional) store, publishing for different
	// addresses at once: every log number is used once and the chain verifies.
	dirs := []*AdapterDirectory{{Store: store, Signer: signer}, {Store: store, Signer: signer}, {Store: store, Signer: signer}}
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u := auth.User{ID: "google:" + string(rune('a'+i)), Email: string(rune('a'+i)) + "@x.io", VerifiedEmail: true}
			if err := dirs[i%3].Publish(ctx, u, newRecovery(t)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	l, err := dirs[0].ReadLog(ctx, 0, 1000)
	if err != nil || len(l.Entries) != 30 {
		t.Fatalf("%d %v", len(l.Entries), err)
	}
	if _, err := VerifyLog(l.Entries, "", 0, logKeys(t, dirs[0])); err != nil {
		t.Fatal(err)
	}
	// A crash between an entry and the head leaves the head behind: the next append catches up.
	_ = store.Save(ctx, logHeadID, []byte(`{"seq":10,"hash":"`+l.Entries[9].Hash+`"}`))
	if err := dirs[1].Publish(ctx, ana, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	l, _ = dirs[0].ReadLog(ctx, 0, 1000)
	if len(l.Entries) != 31 {
		t.Fatalf("%d entries", len(l.Entries))
	}
	if _, err := VerifyLog(l.Entries, "", 0, logKeys(t, dirs[0])); err != nil {
		t.Fatalf("after catching up: %v", err)
	}
	// An unreadable head is repaired the same way (found again by walking).
	_ = store.Save(ctx, logHeadID, []byte("garbage"))
	if err := dirs[2].Publish(ctx, bo, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	if l, _ = dirs[0].ReadLog(ctx, 0, 1000); len(l.Entries) != 32 {
		t.Fatalf("%d entries", len(l.Entries))
	}
	if _, err := VerifyLog(l.Entries, "", 0, logKeys(t, dirs[0])); err != nil {
		t.Fatalf("after an unreadable head: %v", err)
	}
}

// The log works over a store with no conditional write too (one process).
func TestDirectoryLogOverANonConditionalStore(t *testing.T) {
	ctx := context.Background()
	inner := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: struct{ docstore.Adapter }{inner}, Signer: testSigner(t, 1)}
	for range 3 {
		if err := d.Publish(ctx, ana, newRecovery(t)); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := d.ReadLog(ctx, 0, 10)
	if len(l.Entries) != 3 {
		t.Fatalf("%d", len(l.Entries))
	}
	if _, err := VerifyLog(l.Entries, "", 0, logKeys(t, d)); err != nil {
		t.Error(err)
	}
	if _, err := (&AdapterDirectory{Store: inner}).ReadLog(ctx, 0, 1); !errors.Is(err, ErrDirectoryOff) {
		t.Errorf("%v", err)
	}
}

// A log entry is written for a publication that is later refused or fails: none for a refusal
// before the write.
func TestDirectoryRefusedPublishesAreNotLogged(t *testing.T) {
	ctx := context.Background()
	d, _, _ := newTestDirectory(t)
	d.MaxKeys = 1
	_ = d.Publish(ctx, ana, newRecovery(t))
	_ = d.Publish(ctx, ana, newRecovery(t)) // too many
	_ = d.Publish(ctx, auth.User{ID: "passkey:1", Email: "a@x.io", VerifiedEmail: true}, newRecovery(t))
	if l, _ := d.ReadLog(ctx, 0, 10); len(l.Entries) != 1 {
		t.Errorf("%d entries", len(l.Entries))
	}
}
