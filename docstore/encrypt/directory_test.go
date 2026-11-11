package encrypt

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

func testSigner(t *testing.T, fill byte) DirectorySigner {
	t.Helper()
	sg, err := DirectorySignerFromSeed([]byte(strings.Repeat(string([]byte{fill}), 32)))
	if err != nil {
		t.Fatal(err)
	}
	return sg
}

func TestDirectoryPublishRefusesUsersWithoutAVerifiedProviderEmail(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: store, Signer: testSigner(t, 1)}
	key := newRecovery(t)
	for name, u := range map[string]auth.User{
		"no email":     {ID: "google:1"},
		"blank email":  {ID: "google:1", Email: "  "},
		"unverified":   {ID: "google:1", Email: "ana@x.io"},
		"passkey user": {ID: "passkey:abc", Email: "ana@x.io", VerifiedEmail: true}, // an address typed at registration is a label
		"no id":        {Email: "ana@x.io", VerifiedEmail: true},
		"no provider":  {ID: "1", Email: "ana@x.io", VerifiedEmail: true},
	} {
		if err := d.Publish(ctx, u, key); !errors.Is(err, ErrNotPublishable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if metas, _ := store.List(ctx); len(metas) != 0 {
		t.Errorf("nothing was written: %v", metas) // not even a log entry
	}
	if err := d.Publish(ctx, auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}, Recipient{Text: "garbage"}); err == nil {
		t.Error("a key that does not parse is refused")
	}
}

func TestDirectoryIsOffWithoutASigner(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: store}
	if err := d.Publish(ctx, auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}, newRecovery(t)); !errors.Is(err, ErrDirectoryOff) {
		t.Errorf("%v", err)
	}
	if got, err := d.Lookup(ctx, "ana@x.io"); err != nil || got != nil {
		t.Errorf("%v %v", got, err)
	}
	t.Setenv(DirectoryKeyEnv, "")
	if sg, err := DirectorySignerFromEnv(); sg != nil || err != nil {
		t.Errorf("an unset key is off, not an error: %v %v", sg, err)
	}
	t.Setenv(DirectoryKeyEnv, "zz")
	if _, err := DirectorySignerFromEnv(); err == nil {
		t.Error("a bad key is an error")
	}
	t.Setenv(DirectoryKeyEnv, "abcd")
	if _, err := DirectorySignerFromEnv(); err == nil {
		t.Error("a short key is an error")
	}
	t.Setenv(DirectoryKeyEnv, strings.Repeat("ab", 32))
	if sg, err := DirectorySignerFromEnv(); sg == nil || err != nil {
		t.Errorf("%v %v", sg, err)
	}
}

func TestDirectoryLookupMarksVerifiedOnlyForPublishedRecords(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	signer := testSigner(t, 1)
	d := &AdapterDirectory{Store: store, Signer: signer}
	key := newRecovery(t)
	key.Label = "Ana's laptop\n"
	if err := d.Publish(ctx, auth.User{ID: "google:1", Email: " Ana@X.io ", VerifiedEmail: true}, key); err != nil {
		t.Fatal(err)
	}
	got, err := d.Lookup(ctx, "ana@x.IO")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if !got[0].Verified || got[0].Fingerprint != key.Fingerprint || got[0].Text != key.Text || got[0].Type != "age-x25519" || got[0].Label != "Ana's laptop" {
		t.Errorf("%+v", got[0])
	}
	if other, _ := d.Lookup(ctx, "bo@x.io"); len(other) != 0 {
		t.Errorf("another address finds nothing: %v", other)
	}
	if none, err := d.Lookup(ctx, " "); err != nil || none != nil {
		t.Errorf("blank: %v %v", none, err)
	}
	if other, _ := (&AdapterDirectory{Store: store, Signer: testSigner(t, 2)}).Lookup(ctx, "ana@x.io"); len(other) != 0 {
		t.Errorf("another key: %v", other)
	}

	put := func(email string, rec directoryRecord, r Recipient, sign DirectorySigner) {
		t.Helper()
		id := d.directoryID(email, r.Fingerprint)
		if sign != nil {
			rec.Sig = base64.StdEncoding.EncodeToString(sign.Sign(rec.message(id)))
		}
		b, _ := json.Marshal(rec)
		if err := store.Save(ctx, id, b); err != nil {
			t.Fatal(err)
		}
	}
	mk := func() (Recipient, directoryRecord) {
		r := newRecovery(t)
		return r, directoryRecord{V: 2, Key: r.Text, Publisher: "google:9", Provider: "google", Email: "ana@x.io", At: 1}
	}
	k2, r2 := mk()
	put("ana@x.io", r2, k2, nil) // unsigned
	k3, r3 := mk()
	put("ana@x.io", r3, k3, testSigner(t, 7)) // another key
	k4, r4 := mk()
	put("ana@x.io", r4, k4, signer)
	b, _ := store.Load(ctx, d.directoryID("ana@x.io", k4.Fingerprint))
	tampered := strings.Replace(string(b.Data), "google:9", "google:1", 1)
	if err := store.Save(ctx, d.directoryID("ana@x.io", k4.Fingerprint), []byte(tampered)); err != nil { // publisher changed after signing
		t.Fatal(err)
	}
	k5, r5 := mk()
	r5.Publisher, r5.Provider = "passkey:9", "passkey"
	put("ana@x.io", r5, k5, signer) // a passkey session, even signed
	k6, r6 := mk()
	r6.Email = "mallory@x.io"
	put("ana@x.io", r6, k6, signer) // signed for another address
	k7, r7 := mk()
	r7.Email = "ana@x.io"
	other := d.directoryID("mallory@x.io", k7.Fingerprint)
	r7.Sig = base64.StdEncoding.EncodeToString(signer.Sign(r7.message(other)))
	b7, _ := json.Marshal(r7)
	if err := store.Save(ctx, d.directoryID("ana@x.io", k7.Fingerprint), b7); err != nil {
		t.Fatal(err)
	}
	k8, r8 := mk()
	b8, _ := json.Marshal(r8)
	if err := store.Save(ctx, d.directoryID("ana@x.io", "SHA256:other"), b8); err != nil {
		t.Fatal(err)
	}
	_ = k8
	if err := store.Save(ctx, d.directoryID("ana@x.io", "SHA256:junk"), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	got, err = d.Lookup(ctx, "ana@x.io")
	if err != nil || len(got) != 7 {
		t.Fatalf("%v %v", got, err)
	}
	verified := map[string]bool{}
	for _, k := range got {
		verified[k.Fingerprint] = k.Verified
	}
	if !verified[key.Fingerprint] {
		t.Error("the published key is verified")
	}
	for i, k := range []Recipient{k2, k3, k4, k5, k6, k7} {
		if verified[k.Fingerprint] {
			t.Errorf("forged record %d is verified", i+2)
		}
	}
}

func TestDirectoryRecordNamesDoNotRevealAddresses(t *testing.T) {
	d := &AdapterDirectory{Store: docstore.NewMemoryAdapter(), Signer: testSigner(t, 1)}
	id := d.directoryID("A@x.io", "SHA256:ab+/cd")
	if id != d.directoryID(" a@x.io", "SHA256:ab+/cd") {
		t.Error("case and spaces do not change the ID")
	}
	if want := directoryPrefix + d.emailHash("a@x.io") + "/SHA256-ab-_cd"; id != want {
		t.Errorf("%q, want %q", id, want)
	}
	plain := sha256.Sum256([]byte("a@x.io"))
	if strings.Contains(id, hex.EncodeToString(plain[:])) {
		t.Error("the name is a bare hash of the address")
	}
	if id == (&AdapterDirectory{Signer: testSigner(t, 2)}).directoryID("a@x.io", "SHA256:ab+/cd") {
		t.Error("the name does not depend on the key")
	}
}

func TestDirectoryKeepsAtMostMaxKeysPerAddress(t *testing.T) {
	ctx := context.Background()
	d := &AdapterDirectory{Store: docstore.NewMemoryAdapter(), Signer: testSigner(t, 1), MaxKeys: 2}
	u := auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}
	k1, k2 := newRecovery(t), newRecovery(t)
	for _, k := range []Recipient{k1, k2, k1} { // publishing k1 again replaces it
		if err := d.Publish(ctx, u, k); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Publish(ctx, u, newRecovery(t)); !errors.Is(err, ErrTooManyKeys) {
		t.Errorf("%v", err)
	}
	// Another address has its own room.
	if err := d.Publish(ctx, auth.User{ID: "google:2", Email: "bo@x.io", VerifiedEmail: true}, newRecovery(t)); err != nil {
		t.Error(err)
	}
}

// countingStore counts how it is listed.
type countingStore struct {
	docstore.Adapter
	lists, prefixLists int
}

func (c *countingStore) List(ctx context.Context) ([]docstore.Meta, error) {
	c.lists++
	return c.Adapter.List(ctx)
}

func (c *countingStore) ListPrefix(ctx context.Context, p string) ([]docstore.Meta, error) {
	c.prefixLists++
	return docstore.ListPrefix(ctx, c.Adapter, p)
}

func TestDirectoryLookupDoesNotListTheWholeStore(t *testing.T) {
	ctx := context.Background()
	cs := &countingStore{Adapter: docstore.NewMemoryAdapter()}
	d := &AdapterDirectory{Store: cs, Signer: testSigner(t, 1)}
	if err := d.Publish(ctx, auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Lookup(ctx, "ana@x.io"); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if cs.lists != 0 || cs.prefixLists != 2 {
		t.Errorf("lists=%d prefixLists=%d", cs.lists, cs.prefixLists)
	}
}

// slowListStore widens the list-then-save window.
type slowListStore struct{ docstore.Adapter }

func (s slowListStore) List(ctx context.Context) ([]docstore.Meta, error) {
	time.Sleep(20 * time.Millisecond)
	return s.Adapter.List(ctx)
}

func TestDirectoryConcurrentPublishesRespectTheCap(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	d := &AdapterDirectory{Store: slowListStore{store}, Signer: testSigner(t, 1), MaxKeys: 3}
	u := auth.User{ID: "google:1", Email: "ana@x.io", VerifiedEmail: true}
	keys := make([]Recipient, 8)
	for i := range keys {
		keys[i] = newRecovery(t)
	}
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		go func(k Recipient) {
			defer wg.Done()
			_ = d.Publish(ctx, u, k)
		}(k)
	}
	wg.Wait()
	metas, _ := docstore.ListPrefix(ctx, store, "directory/")
	if len(metas) != 3 {
		t.Fatalf("%d keys published for one address with a cap of 3", len(metas))
	}
}

func TestCleanLabelDropsInvisibleFormatting(t *testing.T) {
	for in, want := range map[string]string{
		"laptop":                       "laptop",
		"a\u202Eb\u202Cc":              "abc", // bidi override and pop
		"x\u2066y\u2069z":              "xyz", // isolates
		"zero\u200Bwidth\u200D\uFEFFj": "zerowidthj",
		"l\u200Frtl":                   "lrtl",
		"line\u2028sep\u2029para":      "lineseppara",
		"tab\there\x7f":                "tabhere",
		"\u00adsoft":                   "soft", // soft hyphen is Cf
		"\u00fcn\u00ef\U0001F600":      "\u00fcn\u00ef\U0001F600",
	} {
		if got := cleanLabel(in); got != want {
			t.Errorf("cleanLabel(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 200)
	if got := cleanLabel(long); len([]rune(got)) != 80 {
		t.Errorf("%d runes, want 80", len([]rune(got)))
	}
	if got := cleanLabel(strings.Repeat("\u202E", 100) + strings.Repeat("a", 100)); len(got) != 80 {
		t.Errorf("invisible characters must not use up the limit: %d", len(got))
	}
}
