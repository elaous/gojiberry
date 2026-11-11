package encrypt

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

func fillSigner(t *testing.T, b byte) *Signer {
	t.Helper()
	seed := make([]byte, 32)
	seed[0] = b
	sg, err := NewSigner(seed)
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

func TestPeekFrameReportsTheHeaderAndWhetherItIsSigned(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	sg := fillSigner(t, 1)
	ring := StoreKeyring{Store: inner, Owners: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}, OwnerSigners: []ed25519.PublicKey{sg.Public()}}
	ad := New(inner, ring)
	ad.Signer = sg
	f := docA + "~aaaaaaaaaaaa.json"
	if err := ad.Save(ctx, f, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	stored, _ := inner.Load(ctx, f)
	h, err := ad.PeekFrame(ctx, f, stored.Data)
	if err != nil || h.Version != 3 || !h.Signed || h.Level != "" || h.Seq != 0 {
		t.Fatalf("%+v %v", h, err)
	}
	stranger := New(inner, StoreKeyring{Store: inner, Owners: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}, OwnerSigners: []ed25519.PublicKey{fillSigner(t, 2).Public()}})
	if h, err := stranger.PeekFrame(ctx, f, stored.Data); err != nil || h.Version != 3 || h.Signed {
		t.Fatalf("%+v %v", h, err)
	}
	un := New(inner, ring)
	if err := un.Save(ctx, f, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	stored, _ = inner.Load(ctx, f)
	if h, err := un.PeekFrame(ctx, f, stored.Data); err != nil || h.Version != 2 || h.Signed {
		t.Fatalf("%+v %v", h, err)
	}
	other := newPerson(t, "bo")
	if _, err := New(inner, Static{Keys: []age.Identity{other.id}}).PeekFrame(ctx, f, stored.Data); !errors.Is(err, ErrNoKey) {
		t.Fatalf("%v", err)
	}
	if _, err := ad.PeekFrame(ctx, f, []byte("plain")); !errors.Is(err, ErrNotEncrypted) {
		t.Fatalf("%v", err)
	}
}

func TestTrustFramesReadsAFrameSignedByAnUnknownKey(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	sg := fillSigner(t, 1)
	ad := New(inner, StoreKeyring{Store: inner, Owners: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}, OwnerSigners: []ed25519.PublicKey{sg.Public()}})
	ad.Signer = sg
	f := docA + "~aaaaaaaaaaaa.json"
	if err := ad.Save(ctx, f, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	lost := New(inner, StoreKeyring{Store: inner, Owners: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}, OwnerSigners: []ed25519.PublicKey{fillSigner(t, 2).Public()}})
	if _, err := lost.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("without TrustFrames: %v", err)
	}
	lost.TrustFrames = true
	d, err := lost.Load(ctx, f)
	if err != nil || string(d.Data) != "v1" {
		t.Fatalf("%v %q", err, d.Data)
	}
}

func TestResignIndexMovesTheIndexToAnotherSigner(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	_, sh := sharingSetup(t, inner, ana)
	if err := sh.ResignIndex(ctx); err != nil { // no index: nothing is written
		t.Fatal(err)
	}
	if _, err := inner.Load(ctx, IndexKey); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("an index was made from nothing: %v", err)
	}
	if err := sh.setIndex(ctx, docA, ProtectionE2EE, 1); err != nil {
		t.Fatal(err)
	}
	oldSigner := sh.Signer
	newSigner := fillSigner(t, 2)
	moving := &Sharing{Store: inner, Adapter: sh.Adapter, Signer: newSigner, OwnerSigners: []ed25519.PublicKey{oldSigner.Public()}}
	if err := moving.ResignIndex(ctx); err != nil {
		t.Fatal(err)
	}
	only := &Sharing{Store: inner, Adapter: sh.Adapter, OwnerSigners: []ed25519.PublicKey{newSigner.Public()}}
	idx, err := only.Index(ctx)
	if err != nil || idx.Docs[docA].Level != ProtectionE2EE || idx.Seq < 2 {
		t.Fatalf("with the new signer alone: %+v %v", idx, err)
	}
	if _, err := (&Sharing{Store: inner, Adapter: sh.Adapter, OwnerSigners: []ed25519.PublicKey{oldSigner.Public()}}).Index(ctx); err == nil {
		t.Fatal("the old signer still verifies the index")
	}
}
