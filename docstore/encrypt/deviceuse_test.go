package encrypt

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore"
)

func TestDeviceUseIsKeptEncryptedPerKey(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	a := New(inner, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	t0 := time.Date(2030, 5, 1, 10, 0, 0, 0, time.UTC)
	if err := a.RecordDeviceUse(ctx, "fp-laptop", "work laptop", t0); err != nil {
		t.Fatal(err)
	}
	if err := a.RecordDeviceUse(ctx, "fp-phone", "phone", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A later use of the same key replaces its record.
	if err := a.RecordDeviceUse(ctx, "fp-laptop", "work laptop", t0.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := a.DeviceUses(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("uses: %v, %v", got, err)
	}
	for _, u := range got {
		if u.Fingerprint == "fp-laptop" && !u.At.Equal(t0.Add(48*time.Hour)) {
			t.Errorf("laptop record: %v", u)
		}
	}
	// On the server the record is ciphertext: neither the label nor the time is readable.
	phoneID, _ := deviceUseID("fp-phone")
	raw, err := inner.Load(ctx, phoneID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw.Data, []byte("phone")) || bytes.Contains(raw.Data, []byte("2030")) || !IsEncrypted(raw.Data) {
		t.Errorf("the record is not encrypted: %q", raw.Data)
	}
	// It is not a document: it never expires and is not listed as one.
	if docstore.IsDocumentKey(phoneID) {
		t.Error("a device record counts as a document")
	}
}

func TestDeviceUseRefusesBadFingerprintsAndCleansLabels(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	a := New(docstore.NewMemoryAdapter(), Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	for _, fp := range []string{"", "tab\there", " pad ", strings.Repeat("x", 300)} {
		if err := a.RecordDeviceUse(ctx, fp, "d", time.Now()); err == nil {
			t.Errorf("fingerprint %q accepted", fp)
		}
	}
	if err := a.RecordDeviceUse(ctx, "SHA256:fp/odd", "  my\x00 laptop\n"+strings.Repeat("é", 100), time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := a.DeviceUses(ctx)
	if len(got) != 1 || strings.ContainsAny(got[0].Device, "\x00\n") || len([]rune(got[0].Device)) > MaxDeviceLabel {
		t.Errorf("label: %q", got)
	}
}

func TestDeviceUseOfARemovedKeyIsNotReadableByIt(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	ring := &mutable{to: []age.Recipient{ana.r.Key, bo.r.Key}, keys: []age.Identity{ana.id}}
	a := New(inner, ring)
	if err := a.RecordDeviceUse(ctx, "fp", "phone", time.Now()); err != nil {
		t.Fatal(err)
	}
	ring.to = []age.Recipient{ana.r.Key} // bo is removed
	if _, err := a.RekeyAll(ctx); err != nil {
		t.Fatal(err)
	}
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	if got, _ := asBo.DeviceUses(ctx); len(got) != 0 {
		t.Errorf("a removed key still reads the records: %v", got)
	}
	if got, _ := a.DeviceUses(ctx); len(got) != 1 {
		t.Errorf("the remaining key lost them: %v", got)
	}
}
