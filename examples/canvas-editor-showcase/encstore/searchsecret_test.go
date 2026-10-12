package encstore

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// The search secret is there only while a key is held, is the same on every visit for the same key
// and owner list, differs for another store's keys, and is not readable from what is stored.
func TestSearchSecretNeedsAnUnlockedKeyAndFollowsTheOwners(t *testing.T) {
	r := newRig(t)
	if _, ok := r.st.SearchSecret(); ok {
		t.Fatal("a store with no keys gave a secret")
	}
	rk, _ := recovery(t)
	if err := r.st.Enable(context.Background(), Options{}, passkey(t, r), rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	if _, ok := r.st.SearchSecret(); ok {
		t.Fatal("a locked store gave a secret")
	}
	r.unlockPasskey(t)
	a, ok := r.st.SearchSecret()
	if !ok || len(a) != 32 {
		t.Fatalf("an unlocked store gave no 32-byte secret: %d %v", len(a), ok)
	}
	r.open(t, r.saved)
	r.unlockPasskey(t)
	if b, _ := r.st.SearchSecret(); !bytes.Equal(a, b) {
		t.Fatal("the secret is not the same on the next visit")
	}
	cfg, err := json.Marshal(r.st.Config())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(cfg, a) {
		t.Fatal("the secret appears in the stored configuration")
	}
	r.st.Lock()
	if _, ok := r.st.SearchSecret(); ok {
		t.Fatal("a locked store gave a secret")
	}

	other := newRig(t)
	rk2, _ := recovery(t)
	seed := bytes.Repeat([]byte{9}, 32)
	if err := other.st.Enable(context.Background(), Options{}, passkeyFrom(t, seed, "Other"), rk2); err != nil {
		t.Fatal(err)
	}
	other.open(t, other.saved)
	if err := other.st.UnlockPasskey(bytes.Clone(seed)); err != nil {
		t.Fatal(err)
	}
	if c, _ := other.st.SearchSecret(); bytes.Equal(a, c) {
		t.Fatal("another store with other keys gave the same secret")
	}
}
