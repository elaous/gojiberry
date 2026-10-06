package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

func TestConfidentialCheckNeedsARecoverableOwnerKey(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	_, sh := sharingSetup(t, inner, ana)
	pk, _ := PRFRecipient(bytes.Repeat([]byte{4}, 32), "ana's phone", "cred-4")
	pk2, _ := PRFRecipient(bytes.Repeat([]byte{5}, 32), "ana's laptop", "cred-5")
	id := docfile.NewID()

	if err := ConfidentialCheck(sh, pk)(ctx, "t", id); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("a passkey alone: %v", err)
	}
	if err := ConfidentialCheck(sh, pk, pk2)(ctx, "t", id); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("two passkeys: %v", err)
	}
	if err := ConfidentialCheck(sh)(ctx, "t", id); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("no owner key: %v", err)
	}
	if err := ConfidentialCheck(sh, pk, ana.r)(ctx, "t", id); err != nil {
		t.Errorf("a passkey and an SSH key: %v", err)
	}
	if err := ConfidentialCheck(nil, pk, ana.r)(ctx, "t", id); err != nil {
		t.Errorf("no sharing: %v", err)
	}
	// Someone else's key the document is shared with is not the owner's way back in.
	bo := newPerson(t, "bo")
	if err := sh.Add(ctx, id, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err := ConfidentialCheck(sh, pk)(ctx, "t", id); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("a shared SSH key does not count: %v", err)
	}
	// A list that does not verify is not trusted: the check fails with that error.
	_ = inner.Save(ctx, KeysPrefix+id, []byte(`{"payload":"e30=","sig":"AAAA"}`))
	if err := ConfidentialCheck(sh, pk, ana.r)(ctx, "t", id); !errors.Is(err, ErrBadSignature) {
		t.Errorf("a forged list: %v", err)
	}
}

func TestConfidentialDocumentsOnAnEncryptedStore(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana") // her SSH key is the recovery key
	passkeySecret := bytes.Repeat([]byte{7}, 32)
	pk, _ := PRFRecipient(passkeySecret, "ana's phone", "cred-7")
	pkID, _ := PRFIdentity(passkeySecret)
	inner := docstore.NewMemoryAdapter()
	seed := bytes.Repeat([]byte{3}, 32)
	sg, _ := NewSigner(seed)
	ring := func(owners ...age.Recipient) *Adapter {
		return New(inner, StoreKeyring{Store: inner, Owners: owners, Keys: []age.Identity{pkID, ana.id}, OwnerSigners: []ed25519.PublicKey{sg.Public()}})
	}
	sharingFor := func(ad *Adapter) *Sharing { return &Sharing{Store: inner, Adapter: ad, Signer: sg} }

	// A store whose only owner key is a passkey: nothing can be made confidential.
	ad := ring(pk.Key)
	s := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: ad}}}
	s.ConfidentialCheck = ConfidentialCheck(sharingFor(ad), pk)
	d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "merger terms of xango"}}}}
	d.SetName("Merger xango.io")
	e, err := s.Save(ctx, "m", docfile.Open, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTags(ctx, "m", e.ID, []string{"deal"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProtection(ctx, "m", e.ID, saver.ProtectionE2EE); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Fatalf("%v", err)
	}
	if got, _ := s.Get(ctx, "m", e.ID); got.Name != "Merger xango.io" || got.Protection != saver.ProtectionServer {
		t.Errorf("%+v", got)
	}

	// With the SSH recovery key as a second owner key it is allowed.
	ad = ring(pk.Key, ana.r.Key)
	s = &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: ad}}}
	s.ConfidentialCheck = ConfidentialCheck(sharingFor(ad), pk, ana.r)
	if err := s.SetProtection(ctx, "m", e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "m", e.ID)
	if got.Name != saver.ConfidentialName || len(got.Tags) != 0 || got.Protection != saver.ProtectionE2EE {
		t.Fatalf("%+v", got)
	}
	// What the store shows to whoever can read it: a placeholder, ciphertext, no name or text.
	metas, _ := inner.List(ctx)
	for _, m := range metas {
		raw, _ := inner.Load(ctx, m.ID)
		for _, secret := range []string{"Merger", "xango", "deal"} {
			if bytes.Contains(raw.Data, []byte(secret)) {
				t.Errorf("%s shows %q", m.ID, secret)
			}
		}
	}
	// The owner opens it with the passkey alone, or with the recovery key alone.
	for name, id := range map[string]age.Identity{"passkey": pkID, "recovery key": ana.id} {
		only := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner, Static{Keys: []age.Identity{id}})}}}
		n, tags, err := only.Reveal(ctx, "m", e.ID)
		if err != nil || n != "Merger xango.io" || !slices.Equal(tags, []string{"deal"}) {
			t.Errorf("%s: %q %v %v", name, n, tags, err)
		}
	}

	// Someone with the store and a key that does not fit: the document is locked, a rebuild
	// lists it as confidential without reading it, and Verify says it could not check it.
	bo := newPerson(t, "bo")
	stranger := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner, Static{Keys: []age.Identity{bo.id}})}}}
	if _, _, err := stranger.Reveal(ctx, "m", e.ID); !errors.Is(err, ErrNoKey) || !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("%v", err)
	}
	_ = inner.Delete(ctx, saver.CatalogKey)
	if err := stranger.Rebuild(ctx, "m"); err != nil {
		t.Fatalf("a rebuild without the key: %v", err)
	}
	got, err = stranger.Get(ctx, "m", e.ID)
	if err != nil || got.Name != saver.ConfidentialName || got.Protection != saver.ProtectionE2EE || len(got.Tags) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if rep, err := stranger.Verify(ctx, "m"); err != nil || rep.Locked != 1 || len(rep.Lost) != 0 {
		t.Errorf("%+v %v", rep, err)
	}
	// The owner rebuilds from the documents: the level and the placeholder are what they were.
	_ = inner.Delete(ctx, saver.CatalogKey)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, "m", e.ID)
	if got.Name != saver.ConfidentialName || got.Protection != saver.ProtectionE2EE || len(got.Tags) != 0 {
		t.Errorf("%+v", got)
	}
	if raw, _ := inner.Load(ctx, saver.CatalogKey); strings.Contains(string(raw.Data), "xango") || strings.Contains(string(raw.Data), "deal") {
		t.Errorf("the rebuilt catalog says what is hidden: %s", raw.Data)
	}
}

// A pack of a confidential document, sealed to the owner's keys with the library's own
// encryption injected as Seal, and opened again with the matching Open.
func TestPackSealedWithTheStoresKeys(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	ad := New(inner, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	s := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: ad}}}
	d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "payroll gojiberry.io"}}}}
	d.SetName("Payroll")
	e, _ := s.Save(ctx, "m", docfile.Open, d)
	if err := s.SetProtection(ctx, "m", e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	// Seal and Open are Adapters of their own over an empty store: the pack carries what they write.
	scratch := docstore.NewMemoryAdapter()
	sealer := New(scratch, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	seal := func(ctx context.Context, id string, plain []byte) ([]byte, error) {
		if err := sealer.Save(ctx, id, plain); err != nil {
			return nil, err
		}
		raw, err := scratch.Load(ctx, id)
		return raw.Data, err
	}
	open := func(ctx context.Context, id string, env []byte) ([]byte, error) {
		if err := scratch.Save(ctx, id, env); err != nil {
			return nil, err
		}
		got, err := sealer.Load(ctx, id)
		return got.Data, err
	}
	var buf bytes.Buffer
	if rep, err := s.ExportPack(ctx, "m", &buf, saver.ExportOptions{Seal: seal}); err != nil || rep.Exported != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if bytes.Contains(buf.Bytes(), []byte("payroll")) || bytes.Contains(buf.Bytes(), []byte("Payroll")) {
		t.Fatal("the pack shows the document")
	}
	inner2 := docstore.NewMemoryAdapter()
	dst := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner2, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})}}}
	rep, err := dst.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), saver.ImportOptions{Open: open})
	if err != nil || len(rep.Imported) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	n, _, err := dst.Reveal(ctx, "m", e.ID)
	if err != nil || n != "Payroll" {
		t.Errorf("%q %v", n, err)
	}
}
