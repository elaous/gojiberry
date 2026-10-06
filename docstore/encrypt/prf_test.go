package encrypt

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore"
)

func TestPRFIdentityIsDeterministicAndASoundAgeKey(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	a, err := PRFIdentity(secret)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := PRFIdentity(secret)
	if a.String() != b.String() || a.Recipient().String() != b.Recipient().String() {
		t.Error("the same PRF output gives the same key")
	}
	other, _ := PRFIdentity(bytes.Repeat([]byte{8}, 32))
	if other.String() == a.String() {
		t.Error("a different passkey gives a different key")
	}
	if _, err := PRFIdentity(secret[:31]); err == nil {
		t.Error("a short output is refused")
	}
	r, err := PRFRecipient(secret, "ana's phone", "cred-1")
	if err != nil || !r.IsPasskey() || r.Hint != "cred-1" || r.Label != "ana's phone" || r.Text != a.Recipient().String() {
		t.Fatalf("%+v %v", r, err)
	}
	// Encrypt to the recipient, open with the derived identity.
	var buf bytes.Buffer
	w, _ := age.Encrypt(&buf, r.Key)
	_, _ = w.Write([]byte("hello"))
	_ = w.Close()
	rd, err := age.Decrypt(&buf, a)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	_, _ = got.ReadFrom(rd)
	if got.String() != "hello" {
		t.Error(got.String())
	}
}

func TestCheckRecoverableNeedsAKeyThatIsNotAPasskey(t *testing.T) {
	pk, _ := PRFRecipient(bytes.Repeat([]byte{1}, 32), "a", "c1")
	pk2, _ := PRFRecipient(bytes.Repeat([]byte{2}, 32), "b", "c2")
	rec, _ := ParseAgeRecipient(mustRecovery(t))
	if err := CheckRecoverable(pk); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("one passkey: %v", err)
	}
	if err := CheckRecoverable(pk, pk2); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("two passkeys are two ways to lose it together: %v", err)
	}
	if err := CheckRecoverable(pk, rec); err != nil {
		t.Errorf("a recovery key is the second way: %v", err)
	}
	if err := CheckRecoverable(); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("nothing: %v", err)
	}
}

func mustRecovery(t *testing.T) string {
	t.Helper()
	pub, _, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestPasskeyKeyIsSharedAndOpensTheDocument(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	ad, sh := sharingSetup(t, inner, ana)
	doc := "77777777-7777-4777-8777-777777777777"
	id := doc + "~aaaaaaaaaaaa.json"
	_ = ad.Save(ctx, id, []byte("secret"))
	secret := bytes.Repeat([]byte{9}, 32)
	r, _ := PRFRecipient(secret, "ana's phone", "cred-9")
	if err := sh.Add(ctx, doc, r, r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	list, _ := sh.List(ctx, doc)
	if len(list) != 1 || list[0].Type != TypePasskey || list[0].Hint != "cred-9" || list[0].Label != "ana's phone" {
		t.Fatalf("%+v", list)
	}
	// Later the passkey is asked again: same bytes, same identity, opens the document.
	idn, _ := PRFIdentity(secret)
	if d, err := New(inner, Static{Keys: []age.Identity{idn}}).Load(ctx, id); err != nil || string(d.Data) != "secret" {
		t.Errorf("%v", err)
	}
	// Opening later still works after the list is read back (the entry parses as a recipient).
	if _, err := ad.RekeyDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
}
