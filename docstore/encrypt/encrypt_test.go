package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

// sshKey makes an ed25519 SSH key pair: the public line and the private PEM.
func sshKey(t *testing.T, comment string) (pub string, priv []byte) {
	t.Helper()
	p, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(p)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(k, comment)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment, pem.EncodeToMemory(blk)
}

type person struct {
	r  Recipient
	id age.Identity
}

func newPerson(t *testing.T, label string) person {
	t.Helper()
	pub, priv := sshKey(t, label)
	r, err := ParseSSHRecipient(pub)
	if err != nil {
		t.Fatal(err)
	}
	id, err := ParseSSHIdentity(priv)
	if err != nil {
		t.Fatal(err)
	}
	return person{r, id}
}

func TestRoundTripWithSSHKeysAndTheStoreHoldsCiphertext(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana@opendocs.io")
	if ana.r.Label != "ana@opendocs.io" || ana.r.Type != "ssh-ed25519" || !strings.HasPrefix(ana.r.Fingerprint, "SHA256:") {
		t.Fatalf("%+v", ana.r)
	}
	inner := docstore.NewMemoryAdapter()
	a := New(inner, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	secret := []byte("the quarterly numbers: 1,234,567")
	if err := a.Save(ctx, "doc1", secret); err != nil {
		t.Fatal(err)
	}
	raw, _ := inner.Load(ctx, "doc1")
	if !IsEncrypted(raw.Data) || bytes.Contains(raw.Data, []byte("quarterly")) {
		t.Fatal("the store holds ciphertext")
	}
	got, err := a.Load(ctx, "doc1")
	if err != nil || !bytes.Equal(got.Data, secret) {
		t.Fatalf("%q %v", got.Data, err)
	}
	// The same plaintext is a different ciphertext each time (a fresh data key).
	_ = a.Save(ctx, "doc2", secret)
	raw2, _ := inner.Load(ctx, "doc2")
	if bytes.Equal(raw.Data, raw2.Data) {
		t.Error("each save has its own data key")
	}
	// The catalog and ACL records stay readable.
	_ = a.Save(ctx, "catalog:index", []byte(`{"docs":{}}`))
	_ = a.Save(ctx, "acl:doc1", []byte(`{}`))
	for _, id := range []string{"catalog:index", "acl:doc1"} {
		if d, _ := inner.Load(ctx, id); IsEncrypted(d.Data) {
			t.Errorf("%s is readable by design", id)
		}
	}
}

func TestWrongKeyAndTamperedCiphertextAreRefused(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	_ = New(inner, Static{To: []age.Recipient{ana.r.Key}}).Save(ctx, "d", []byte("for ana only"))
	stranger := New(inner, Static{Keys: []age.Identity{bo.id}})
	if _, err := stranger.Load(ctx, "d"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("a key that is not a recipient: %v", err)
	}
	// Flip a byte in the payload.
	raw, _ := inner.Load(ctx, "d")
	bad := append([]byte(nil), raw.Data...)
	bad[len(bad)-3] ^= 1
	_ = inner.Save(ctx, "d", bad)
	if d, err := New(inner, Static{Keys: []age.Identity{ana.id}}).Load(ctx, "d"); err == nil {
		t.Fatalf("tampered ciphertext was read: %q", d.Data)
	}
	// A document stored in the clear is read unless encryption is required.
	_ = inner.Save(ctx, "plain", []byte("not secret"))
	a := New(inner, Static{Keys: []age.Identity{ana.id}})
	if d, err := a.Load(ctx, "plain"); err != nil || string(d.Data) != "not secret" {
		t.Errorf("%v", err)
	}
	a.RequireEncrypted = true
	if _, err := a.Load(ctx, "plain"); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("%v", err)
	}
	if err := New(inner, Static{}).Save(ctx, "x", []byte("y")); !errors.Is(err, ErrNoRecipients) {
		t.Errorf("a document no one can open is not saved: %v", err)
	}
}

func TestSharingIsAnotherRecipientAndRevokingIsAReKey(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	ring := &mutable{to: []age.Recipient{ana.r.Key}}
	a := New(inner, ring)
	_ = a.Save(ctx, "d", []byte("version 1"))
	// Shared with Bo: a save to both.
	ring.to = []age.Recipient{ana.r.Key, bo.r.Key}
	if err := a.Rekey(ctx, "d"); err == nil {
		t.Fatal("Ana's own key is needed to open it first") // ring has no identities yet
	}
	ring.keys = []age.Identity{ana.id}
	if err := a.Rekey(ctx, "d"); err != nil {
		t.Fatal(err)
	}
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	if d, err := asBo.Load(ctx, "d"); err != nil || string(d.Data) != "version 1" {
		t.Fatalf("Bo can open it now: %v", err)
	}
	oldCopy, _ := inner.Load(ctx, "d") // what Bo could have kept
	// Revoked: Ana's re-key to her alone, with a new data key.
	ring.to = []age.Recipient{ana.r.Key}
	n, err := a.RekeyAll(ctx)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := asBo.Load(ctx, "d"); !errors.Is(err, ErrNoKey) {
		t.Fatalf("Bo is out: %v", err)
	}
	if d, err := a.Load(ctx, "d"); err != nil || string(d.Data) != "version 1" {
		t.Fatalf("Ana still reads it: %v", err)
	}
	// What Bo already had still opens for him: nothing can take that back.
	_ = inner.Save(ctx, "kept", oldCopy.Data)
	if d, err := asBo.Load(ctx, "kept"); err != nil || string(d.Data) != "version 1" {
		t.Errorf("an old copy keeps working for who has it: %v", err)
	}
}

type mutable struct {
	to   []age.Recipient
	keys []age.Identity
}

func (m *mutable) Recipients(string) ([]age.Recipient, error) { return m.to, nil }
func (m *mutable) Identities(string) ([]age.Identity, error)  { return m.keys, nil }

func TestRecoveryKeyOpensWhatTheOwnerKeyCannot(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	pub, secret, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ParseAgeRecipient(pub)
	if err != nil || rec.Type != "age-x25519" || rec.Fingerprint == "" {
		t.Fatalf("%+v %v", rec, err)
	}
	inner := docstore.NewMemoryAdapter()
	_ = New(inner, Static{To: []age.Recipient{ana.r.Key, rec.Key}}).Save(ctx, "d", []byte("kept safe"))
	recID, err := ParseAgeIdentity(secret)
	if err != nil {
		t.Fatal(err)
	}
	if d, err := New(inner, Static{Keys: []age.Identity{recID}}).Load(ctx, "d"); err != nil || string(d.Data) != "kept safe" {
		t.Fatalf("the recovery key opens it: %v", err)
	}
}

func TestParsingBadKeys(t *testing.T) {
	for _, bad := range []string{"", "ssh-ed25519", "ssh-ed25519 AAAA", "hello world"} {
		if _, err := ParseSSHRecipient(bad); err == nil {
			t.Errorf("%q", bad)
		}
	}
	if _, err := ParseAgeRecipient("age1nope"); err == nil {
		t.Error("an age key")
	}
	if _, err := ParseSSHIdentity([]byte("not a pem")); err == nil {
		t.Error("an identity")
	}
	if _, err := ParseAgeIdentity("nope"); err == nil {
		t.Error("an age secret")
	}
}

// The saver, folders, versions and the conditional catalog work through the encrypting
// adapter, and the store holds no document text.
func TestTheSaverWorksOnAnEncryptedStore(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	ad := New(inner, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	ad.RequireEncrypted = true
	s := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: ad}}}
	f, err := s.CreateFolder(ctx, "m", "", "Private")
	if err != nil {
		t.Fatal(err)
	}
	d := docfile.Document{Title: "Diary", Content: []docfile.Item{{Chars: &docfile.Chars{Text: "dear diary, the password is hunter2"}}}}
	d.SetName("Diary")
	d.SetFolder(f.ID)
	e, err := s.Save(ctx, "m", docfile.JSON, d)
	if err != nil {
		t.Fatal(err)
	}
	d.SetID(e.ID)
	if _, err := s.SaveVersion(ctx, "m", docfile.JSON, d); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(ctx, "m", e.ID)
	if err != nil || got.Content[0].Chars.Text != "dear diary, the password is hunter2" {
		t.Fatalf("%v", err)
	}
	if vs, _ := s.Versions(ctx, "m", e.ID); len(vs) != 1 {
		t.Fatalf("%d", len(vs))
	}
	if rep, err := s.Verify(ctx, "m"); err != nil || len(rep.Lost) != 0 || rep.Checked != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	// Nothing in the store has the text; the catalog does list the name.
	metas, _ := inner.List(ctx)
	sawName := false
	for _, m := range metas {
		raw, _ := inner.Load(ctx, m.ID)
		if bytes.Contains(raw.Data, []byte("hunter2")) {
			t.Errorf("%s holds the text", m.ID)
		}
		if strings.HasPrefix(m.ID, "catalog:") && bytes.Contains(raw.Data, []byte("Diary")) {
			sawName = true
		}
	}
	if !sawName {
		t.Error("the catalog stays readable (names, tags, folders)")
	}
	// Someone with only the store sees ciphertext and cannot open a document.
	stranger := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner, Static{})}}}
	if _, err := stranger.Load(ctx, "m", e.ID); err == nil {
		t.Error("a store with no key opens nothing")
	}
}
