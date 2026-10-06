package encrypt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

func newRecovery(t *testing.T) Recipient {
	t.Helper()
	pub, _, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseAgeRecipient(pub)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDirectoryPublishRefusesUsersWithoutAVerifiedProviderEmail(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: store}
	key := newRecovery(t)
	for name, u := range map[string]auth.User{
		"no email":     {ID: "google:1"},
		"blank email":  {ID: "google:1", Email: "  "},
		"passkey user": {ID: "passkey:abc", Email: "ana@x.io"}, // an address typed at registration is a label
		"no id":        {Email: "ana@x.io"},
	} {
		if err := d.Publish(ctx, u, key); !errors.Is(err, ErrNotPublishable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if metas, _ := store.List(ctx); len(metas) != 0 {
		t.Errorf("nothing was written: %v", metas)
	}
	if err := d.Publish(ctx, auth.User{ID: "google:1", Email: "ana@x.io"}, Recipient{Text: "garbage"}); err == nil {
		t.Error("a key that does not parse is refused")
	}
}

func TestDirectoryLookupMarksVerifiedOnlyForPublishedRecords(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: store}
	key := newRecovery(t)
	if err := d.Publish(ctx, auth.User{ID: "google:1", Email: " Ana@X.io "}, key); err != nil {
		t.Fatal(err)
	}
	got, err := d.Lookup(ctx, "ana@x.IO")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if !got[0].Verified || got[0].Fingerprint != key.Fingerprint || got[0].Text != key.Text || got[0].Type != "age-x25519" {
		t.Errorf("%+v", got[0])
	}
	if other, _ := d.Lookup(ctx, "bo@x.io"); len(other) != 0 {
		t.Errorf("another address finds nothing: %v", other)
	}
	if none, err := d.Lookup(ctx, " "); err != nil || none != nil {
		t.Errorf("blank: %v %v", none, err)
	}

	// Records someone wrote by hand do not get the mark.
	put := func(email string, rec directoryRecord, r Recipient) {
		t.Helper()
		b, _ := json.Marshal(rec)
		if err := store.Save(ctx, directoryID(email, r.Fingerprint), b); err != nil {
			t.Fatal(err)
		}
	}
	k2, k3, k4, k5 := newRecovery(t), newRecovery(t), newRecovery(t), newRecovery(t)
	put("ana@x.io", directoryRecord{V: 1, Key: k2.Text, Publisher: "google:9", Email: "ana@x.io"}, k2)                     // does not claim it
	put("ana@x.io", directoryRecord{V: 1, Key: k3.Text, Publisher: "passkey:9", Email: "ana@x.io", Verified: true}, k3)    // a passkey session
	put("ana@x.io", directoryRecord{V: 1, Key: k4.Text, Publisher: "google:9", Email: "mallory@x.io", Verified: true}, k4) // another address
	// A record whose key is not the one its ID names is dropped, not trusted.
	b, _ := json.Marshal(directoryRecord{V: 1, Key: k5.Text, Publisher: "google:9", Email: "ana@x.io", Verified: true})
	if err := store.Save(ctx, directoryID("ana@x.io", "SHA256:other"), b); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, directoryID("ana@x.io", "SHA256:junk"), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	got, err = d.Lookup(ctx, "ana@x.io")
	if err != nil || len(got) != 4 {
		t.Fatalf("%v %v", got, err)
	}
	verified := map[string]bool{}
	for _, k := range got {
		verified[k.Fingerprint] = k.Verified
	}
	if !verified[key.Fingerprint] || verified[k2.Fingerprint] || verified[k3.Fingerprint] || verified[k4.Fingerprint] {
		t.Errorf("only the published key is verified: %v", verified)
	}
}

func TestDirectoryIDIsPathSafe(t *testing.T) {
	id := directoryID("A@x.io", "SHA256:ab+/cd")
	if id != directoryID(" a@x.io", "SHA256:ab+/cd") {
		t.Error("case and spaces do not change the ID")
	}
	if want := directoryPrefix + emailHash("a@x.io") + "/SHA256-ab-_cd"; id != want {
		t.Errorf("%q, want %q", id, want)
	}
}
