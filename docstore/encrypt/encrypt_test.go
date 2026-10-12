package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"filippo.io/age"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
	"golang.org/x/crypto/ssh"
	"strings"
	"testing"
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
	// A document stored in the clear is refused by default (a store writer cannot replace a
	// ciphertext with plaintext), and read only with the explicit opt-out.
	_ = inner.Save(ctx, "plain", []byte("not secret"))
	a := New(inner, Static{Keys: []age.Identity{ana.id}})
	if _, err := a.Load(ctx, "plain"); !errors.Is(err, ErrNotEncrypted) {
		t.Errorf("%v", err)
	}
	a.AllowPlaintext = true
	if d, err := a.Load(ctx, "plain"); err != nil || string(d.Data) != "not secret" {
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
	kept := docstore.NewMemoryAdapter()
	_ = kept.Save(ctx, "d", oldCopy.Data)
	if d, err := New(kept, Static{Keys: []age.Identity{bo.id}}).Load(ctx, "d"); err != nil || string(d.Data) != "version 1" {
		t.Errorf("an old copy keeps working for who has it: %v", err)
	}
}

type mutable struct {
	to   []age.Recipient
	keys []age.Identity
}

func (m *mutable) Recipients(context.Context, string) ([]age.Recipient, error) { return m.to, nil }

func (m *mutable) Identities(context.Context, string) ([]age.Identity, error) { return m.keys, nil }

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

func TestDocIDFindsTheDocumentAFileBelongsTo(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	for stored, want := range map[string]string{
		id + "~0123456789ab.json": id,
		id + ".json":              id,
		"versions/" + id + "/00000000000000000042.json": id,
		"catalog:index":        "",
		"fonts/Serif/400.ttf":  "",
		"versions/nope/1.json": "",
		"short":                "",
	} {
		if got := DocID(stored); got != want {
			t.Errorf("%q: %q, want %q", stored, got, want)
		}
	}
}

func TestSharingByKeyNeedsTheFingerprintAndRemovingIsAReKey(t *testing.T) {
	ctx := context.Background()
	ana, bo, cy := newPerson(t, "ana"), newPerson(t, "bo"), newPerson(t, "cy")
	inner := docstore.NewMemoryAdapter()
	ad, sh := sharingSetup(t, inner, ana)
	docID := "22222222-2222-4222-8222-222222222222"
	cur, prev, ver := docID+"~aaaaaaaaaaaa.json", docID+"~bbbbbbbbbbbb.json", "versions/"+docID+"/00000000000000000007.json"
	for _, id := range []string{cur, prev, ver} {
		if err := ad.Save(ctx, id, []byte("text of "+id)); err != nil {
			t.Fatal(err)
		}
	}
	other := "33333333-3333-4333-8333-333333333333~cccccccccccc.json"
	_ = ad.Save(ctx, other, []byte("someone else's"))
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	if _, err := asBo.Load(ctx, cur); !errors.Is(err, ErrNoKey) {
		t.Fatal("not shared yet")
	}

	// The fingerprint has to be confirmed, and be this key's.
	if err := sh.Add(ctx, docID, bo.r, ""); !errors.Is(err, ErrFingerprint) {
		t.Fatalf("no confirmation: %v", err)
	}
	if err := sh.Add(ctx, docID, bo.r, cy.r.Fingerprint); !errors.Is(err, ErrFingerprint) {
		t.Fatalf("someone else's fingerprint: %v", err)
	}
	if err := sh.Add(ctx, docID, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := sh.Add(ctx, docID, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal("adding twice changes nothing:", err)
	}
	list, _ := sh.List(ctx, docID)
	if len(list) != 1 || list[0].Label != "bo" || list[0].Fingerprint != bo.r.Fingerprint || list[0].Type != "ssh-ed25519" {
		t.Fatalf("%+v", list)
	}
	// Everything of the document opens for Bo: the copy, the previous copy and the version.
	for _, id := range []string{cur, prev, ver} {
		if d, err := asBo.Load(ctx, id); err != nil || string(d.Data) != "text of "+id {
			t.Errorf("%s: %v", id, err)
		}
	}
	if _, err := asBo.Load(ctx, other); !errors.Is(err, ErrNoKey) {
		t.Error("another document is not shared")
	}
	// The record is readable by anyone with the store (public keys only).
	raw, err := inner.Load(ctx, KeysPrefix+docID)
	var sr signedRecord
	if err != nil || IsEncrypted(raw.Data) || json.Unmarshal(raw.Data, &sr) != nil || !bytes.Contains(sr.Payload, []byte(bo.r.Fingerprint)) {
		t.Errorf("the recipient list is plain (and signed): %v", err)
	}
	// A save made later goes to Bo too.
	_ = ad.Save(ctx, cur, []byte("newer"))
	if d, err := asBo.Load(ctx, cur); err != nil || string(d.Data) != "newer" {
		t.Errorf("%v", err)
	}

	// Revoked: the files are encrypted again without Bo.
	n, err := sh.Remove(ctx, docID, bo.r.Fingerprint)
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	for _, id := range []string{cur, prev, ver} {
		if _, err := asBo.Load(ctx, id); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s: Bo is out: %v", id, err)
		}
		if _, err := ad.Load(ctx, id); err != nil {
			t.Errorf("%s: Ana still reads it: %v", id, err)
		}
	}
	if list, _ := sh.List(ctx, docID); len(list) != 0 {
		t.Errorf("%+v", list)
	}
	if _, err := sh.Remove(ctx, docID, bo.r.Fingerprint); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("removing a key twice, with nothing pending: %v", err)
	}
	if _, err := sh.Remove(ctx, docID, "SHA256:nobody"); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("%v", err)
	}
	// An age (recovery) key shares the same way.
	pub, secret, _ := GenerateRecoveryKey()
	rec, _ := ParseAgeRecipient(pub)
	if err := sh.Add(ctx, docID, rec, rec.Fingerprint); err != nil {
		t.Fatal(err)
	}
	recID, _ := ParseAgeIdentity(secret)
	if d, err := New(inner, Static{Keys: []age.Identity{recID}}).Load(ctx, cur); err != nil || string(d.Data) != "newer" {
		t.Errorf("%v", err)
	}
}

func TestRemoveEverywhere(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	ad, sh := sharingSetup(t, inner, ana)
	a, b, c := "44444444-4444-4444-8444-444444444444~aaaaaaaaaaaa.json", "55555555-5555-4555-8555-555555555555~bbbbbbbbbbbb.json", "66666666-6666-4666-8666-666666666666~cccccccccccc.json"
	for _, id := range []string{a, b, c} {
		if err := ad.Save(ctx, id, []byte("text of "+id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, doc := range []string{DocID(a), DocID(b)} {
		if err := sh.Add(ctx, doc, bo.r, bo.r.Fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	used, err := sh.Usage(ctx, bo.r.Fingerprint)
	if err != nil || len(used) != 2 || used[0] != DocID(a) || used[1] != DocID(b) {
		t.Fatalf("%v %v", used, err)
	}
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	if _, err := asBo.Load(ctx, a); err != nil {
		t.Fatal(err)
	}
	docs, files, err := sh.RemoveEverywhere(ctx, bo.r.Fingerprint)
	if err != nil || len(docs) != 2 || files != 2 {
		t.Fatalf("%v %d %v", docs, files, err)
	}
	for _, id := range []string{a, b} {
		if _, err := asBo.Load(ctx, id); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s: %v", id, err)
		}
		if d, err := ad.Load(ctx, id); err != nil || string(d.Data) != "text of "+id {
			t.Errorf("%s: Ana still reads it: %v", id, err)
		}
	}
	if used, _ := sh.Usage(ctx, bo.r.Fingerprint); len(used) != 0 {
		t.Errorf("%v", used)
	}
	if docs, files, err := sh.RemoveEverywhere(ctx, bo.r.Fingerprint); err != nil || len(docs) != 0 || files != 0 {
		t.Errorf("nothing left to do: %v %d %v", docs, files, err)
	}
}

func setup(t *testing.T) (*Adapter, *docstore.MemoryAdapter, person) {
	t.Helper()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	return New(inner, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}}), inner, ana
}

func TestACiphertextIsBoundToItsID(t *testing.T) {
	ctx := context.Background()
	a, inner, _ := setup(t)
	_ = a.Save(ctx, "A", []byte("secret of A"))
	_ = a.Save(ctx, "B", []byte("secret of B"))
	rawA, _ := inner.Load(ctx, "A")
	_ = inner.Save(ctx, "B", rawA.Data)
	if d, err := a.Load(ctx, "B"); !errors.Is(err, ErrWrongDocument) {
		t.Fatalf("A's ciphertext swapped into B: %q %v", d.Data, err)
	}
	if d, err := a.Load(ctx, "A"); err != nil || string(d.Data) != "secret of A" {
		t.Fatal(err)
	}
}

func TestEmptyPlaintextRoundTrips(t *testing.T) {
	ctx := context.Background()
	a, _, _ := setup(t)
	if err := a.Save(ctx, "e", nil); err != nil {
		t.Fatal(err)
	}
	if d, err := a.Load(ctx, "e"); err != nil || len(d.Data) != 0 {
		t.Fatalf("%q %v", d.Data, err)
	}
}

func TestTruncatedTrailingAndFlippedCiphertextAreRefused(t *testing.T) {
	ctx := context.Background()
	a, inner, _ := setup(t)
	_ = a.Save(ctx, "d", bytes.Repeat([]byte("x"), 70000)) // more than one chunk
	raw, _ := inner.Load(ctx, "d")
	good := raw.Data
	try := func(name string, b []byte) {
		t.Helper()
		_ = inner.Save(ctx, "d", b)
		if d, err := a.Load(ctx, "d"); err == nil {
			t.Errorf("%s was read (%d bytes)", name, len(d.Data))
		}
	}
	try("empty", nil)
	try("3 bytes", good[:3])
	try("header only", good[:len(header)])
	try("one chunk cut", good[:len(good)-65536-16])
	try("last byte cut", good[:len(good)-1])
	try("trailing bytes", append(append([]byte(nil), good...), 'x'))
	// A flip in the header (the stanza or its MAC).
	for _, off := range []int{len(header) + 2, len(header) + 40, bytes.Index(good, []byte("\n--- ")) + 8} {
		b := append([]byte(nil), good...)
		b[off] ^= 1
		try("header flip", b)
	}
	_ = inner.Save(ctx, "d", good)
	if _, err := a.Load(ctx, "d"); err != nil {
		t.Fatal(err)
	}
}

func TestCustomSkip(t *testing.T) {
	ctx := context.Background()
	a, inner, _ := setup(t)
	a.Skip = func(id string) bool { return strings.HasPrefix(id, "open/") }
	_ = a.Save(ctx, "open/x", []byte("readable"))
	_ = a.Save(ctx, "closed", []byte("hidden"))
	if r, _ := inner.Load(ctx, "open/x"); string(r.Data) != "readable" {
		t.Error("skipped id is stored as is")
	}
	if r, _ := inner.Load(ctx, "closed"); !IsEncrypted(r.Data) {
		t.Error("the rest is encrypted")
	}
	// With a custom Skip, the catalog is not skipped, and writing it encrypted is refused.
	if err := a.Save(ctx, "catalog:index", []byte("{}")); !errors.Is(err, ErrReservedID) {
		t.Errorf("%v", err)
	}
}

func TestReservedIDsAreNotEncrypted(t *testing.T) {
	ctx := context.Background()
	a, _, _ := setup(t)
	a.Skip = func(string) bool { return false }
	for _, id := range []string{"catalog:x", "acl:x", "fonts/x"} {
		if err := a.Save(ctx, id, nil); !errors.Is(err, ErrReservedID) {
			t.Errorf("%s: %v", id, err)
		}
		if err := a.SaveIf(ctx, id, nil, ""); !errors.Is(err, ErrReservedID) {
			t.Errorf("%s: %v", id, err)
		}
	}
}

func TestSaveIfOnAnEncryptedID(t *testing.T) {
	ctx := context.Background()
	a, inner, _ := setup(t)
	if err := a.SaveIf(ctx, "d", []byte("one"), ""); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.SaveIf(ctx, "d", []byte("again"), ""); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("create over an existing one: %v", err)
	}
	if err := a.SaveIf(ctx, "d", []byte("two"), docstore.Digest([]byte("one"))); err != nil {
		t.Fatalf("digest of the plaintext read: %v", err)
	}
	if err := a.SaveIf(ctx, "d", []byte("three"), docstore.Digest([]byte("one"))); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("stale digest: %v", err)
	}
	if d, _ := a.Load(ctx, "d"); string(d.Data) != "two" {
		t.Fatalf("%q", d.Data)
	}
	if err := a.SaveIf(ctx, "gone", []byte("x"), docstore.Digest([]byte("x"))); !errors.Is(err, docstore.ErrConflict) {
		t.Fatalf("%v", err)
	}
	// Not conditional inside: it says so.
	plainStore := New(unconditional{inner}, Static{})
	if err := plainStore.SaveIf(ctx, "d", nil, ""); !errors.Is(err, docstore.ErrConditionalUnsupported) {
		t.Fatalf("%v", err)
	}
}

// unconditional hides the inner adapter's SaveIf.
type unconditional struct{ docstore.Adapter }

func sharingSetup(t *testing.T, inner docstore.Adapter, owner person) (*Adapter, *Sharing) {
	t.Helper()
	seed := make([]byte, 32)
	seed[0] = 1
	sg, err := NewSigner(seed)
	if err != nil {
		t.Fatal(err)
	}
	ad := New(inner, StoreKeyring{Store: inner, Owners: []age.Recipient{owner.r.Key}, Keys: []age.Identity{owner.id}, OwnerSigners: []ed25519.PublicKey{sg.Public()}})
	return ad, &Sharing{Store: inner, Adapter: ad, Signer: sg}
}

const docA = "44444444-4444-4444-8444-444444444444"

const docB = "55555555-5555-4555-8555-555555555555"

func TestAStoreWriterCannotAddARecipientOrChangeTheList(t *testing.T) {
	ctx := context.Background()
	ana, bo, mallory := newPerson(t, "ana"), newPerson(t, "bo"), newPerson(t, "mallory")
	inner := docstore.NewMemoryAdapter()
	ad, sh := sharingSetup(t, inner, ana)
	f := docA + "~aaaaaaaaaaaa.json"
	_ = ad.Save(ctx, f, []byte("v1"))
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	good, _ := inner.Load(ctx, KeysPrefix+docA)

	// The old unsigned format, with Mallory in it.
	legacy := `{"recipients":[{"type":"` + mallory.r.Type + `","key":"` + mallory.r.Text + `"}]}`
	_ = inner.Save(ctx, KeysPrefix+docA, []byte(legacy))
	if err := ad.Save(ctx, f, []byte("v2")); !errors.Is(err, ErrUnsignedRecord) {
		t.Fatalf("unsigned: %v", err)
	}
	// Signed by Mallory's own signing key.
	evil, _ := NewSigner(bytes.Repeat([]byte{9}, 32))
	forged := &Sharing{Store: inner, Adapter: ad, Signer: evil}
	_ = inner.Delete(ctx, KeysPrefix+docA)
	// Mallory writes a record under her own key (recover mode: no check against the files'
	// frames, which would refuse the deleted record; the point here is the signature).
	if _, err := forged.mutateWith(ctx, docA, mutateOpts{recover: true}, func(rec *sharedRecord) (bool, error) {
		rec.Recipients = append(rec.Recipients, sharedEntry{Type: mallory.r.Type, Key: mallory.r.Text})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ad.Save(ctx, f, []byte("v2")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a record signed by a stranger: %v", err)
	}
	// A valid record of another document does not do for this one.
	_ = inner.Save(ctx, KeysPrefix+docA, good.Data)
	_ = sh.Add(ctx, docB, mallory.r, mallory.r.Fingerprint)
	other, _ := inner.Load(ctx, KeysPrefix+docB)
	_ = inner.Save(ctx, KeysPrefix+docA, other.Data)
	if _, err := sh.List(ctx, docA); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a record moved between documents: %v", err)
	}
	// A changed payload.
	var sr signedRecord
	_ = json.Unmarshal(good.Data, &sr)
	sr.Payload = bytes.Replace(sr.Payload, []byte(`"v":1`), []byte(`"v":2`), 1)
	tampered, _ := json.Marshal(sr)
	_ = inner.Save(ctx, KeysPrefix+docA, tampered)
	if _, err := sh.List(ctx, docA); err == nil {
		t.Fatal("a changed record verified")
	}
	// The real one works, and a keyring with no signers refuses a document that has a record.
	_ = inner.Save(ctx, KeysPrefix+docA, good.Data)
	if err := ad.Save(ctx, f, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	noSigners := New(inner, StoreKeyring{Store: inner, Owners: []age.Recipient{ana.r.Key}})
	if err := noSigners.Save(ctx, f, []byte("v3")); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("%v", err)
	}
	// No signer, no change.
	if err := (&Sharing{Store: inner, Adapter: ad}).Add(ctx, docA, mallory.r, mallory.r.Fingerprint); !errors.Is(err, ErrNoSigner) {
		t.Fatalf("%v", err)
	}
}

func TestTheFingerprintIsComputedFromTheStoredKey(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	_, sh := sharingSetup(t, inner, ana)
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	// Rewrite the stored fingerprint (re-signing as the owner would be needed to do this
	// for real; here the list is read through the same path with the field changed).
	var lied sharedEntry
	rec, _, _ := loadRecord(ctx, inner, docA, sh.verifiers(), nil)
	lied = rec.Recipients[0]
	lied.Fingerprint = "SHA256:somebodyelse"
	p, err := parseShared(lied)
	if err != nil || p.Fingerprint != bo.r.Fingerprint {
		t.Fatalf("%+v %v", p, err)
	}
	// An entry that claims to be age but holds an SSH key is not read as either.
	if _, err := parseShared(sharedEntry{Type: "age-x25519", Key: bo.r.Text}); err == nil {
		t.Error("type and key must agree")
	}
}

func TestMultiLineKeysAreRefused(t *testing.T) {
	a, b := newPerson(t, "a"), newPerson(t, "b")
	if _, err := ParseSSHRecipient(a.r.Text + "\n" + b.r.Text); err == nil {
		t.Error("two lines")
	}
	if _, err := ParseSSHRecipient(a.r.Text + "\r\n# more"); err == nil {
		t.Error("a trailing line")
	}
	if _, err := ParseSSHRecipient("  " + a.r.Text + "  \n"); err != nil {
		t.Errorf("surrounding space is fine: %v", err)
	}
}

func TestSignersFromAgeAndSSHKeys(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	a, b := SignerFromAge(id), SignerFromAge(id)
	if !a.Public().Equal(b.Public()) {
		t.Error("deterministic")
	}
	other, _ := age.GenerateX25519Identity()
	if a.Public().Equal(SignerFromAge(other).Public()) {
		t.Error("distinct")
	}
	_, priv := sshKey(t, "k")
	if _, err := SignerFromSSH(priv); err != nil {
		t.Error(err)
	}
	if _, err := SignerFromSSH([]byte("junk")); err == nil {
		t.Error("junk")
	}
	if _, err := NewSigner([]byte("short")); err == nil {
		t.Error("short seed")
	}
}
