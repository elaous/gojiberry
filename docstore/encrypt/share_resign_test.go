package encrypt

import (
	"context"
	"crypto/ed25519"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

// Resign signs a list again with another owner's key, so a list survives the removal of the
// owner key that signed it: after the old signer is out of OwnerSigners, only the re-signed list
// verifies.
func TestResignMovesAListToAnotherSigner(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	_, sh := sharingSetup(t, inner, ana)
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	oldSigner := sh.Signer
	seed := make([]byte, 32)
	seed[0] = 2
	newSigner, _ := NewSigner(seed)
	// A Sharing that trusts both signers, signing with the new one, moves the list over.
	moving := &Sharing{Store: inner, Adapter: sh.Adapter, Signer: newSigner, OwnerSigners: []ed25519.PublicKey{oldSigner.Public()}}
	if err := moving.Resign(ctx, docA); err != nil {
		t.Fatal(err)
	}
	only := &Sharing{Store: inner, Adapter: sh.Adapter, OwnerSigners: []ed25519.PublicKey{newSigner.Public()}}
	list, err := only.List(ctx, docA)
	if err != nil || len(list) != 1 || list[0].Fingerprint != bo.r.Fingerprint {
		t.Fatalf("with the new signer alone: %v %+v", err, list)
	}
	oldOnly := &Sharing{Store: inner, Adapter: sh.Adapter, OwnerSigners: []ed25519.PublicKey{oldSigner.Public()}}
	if _, err := oldOnly.List(ctx, docA); err == nil {
		t.Fatal("the old signer still verifies the list")
	}
}

// Sign is a signature of the host's own statements, kept apart from the recipient lists: one is
// never accepted as the other.
func TestSignedMessagesAreSeparateFromRecipientLists(t *testing.T) {
	seed := make([]byte, 32)
	seed[0] = 9
	sg, _ := NewSigner(seed)
	sig := sg.Sign([]byte("hello"))
	if !VerifySigned(sg.Public(), []byte("hello"), sig) || VerifySigned(sg.Public(), []byte("hullo"), sig) {
		t.Fatal("verify")
	}
	other, _ := NewSigner(append([]byte{1}, seed[1:]...))
	if VerifySigned(other.Public(), []byte("hello"), sig) {
		t.Fatal("another key verified it")
	}
	if ed25519.Verify(sg.Public(), recordMessage(docA, []byte("hello")), sig) {
		t.Fatal("a signed message verifies as a record")
	}
}
