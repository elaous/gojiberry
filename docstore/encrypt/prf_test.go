package encrypt

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
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

// Known answers: a fixed PRF output gives these exact strings. They were computed once and the
// derivation step was checked by hand (HKDF-SHA256, no salt, the info string, with a second
// implementation), so a change to the derivation (the salt, the info, the encoding) fails here
// instead of silently locking people out of documents they encrypted.
var prfVectors = []struct {
	secretHex, seedHex, secretKey, recipient, fingerprint string
}{
	{
		secretHex:   "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
		seedHex:     "865f87b2e0f5d0527ffa7b25d1e5761cf90007e486e81636715e8bb70b878ac3",
		secretKey:   "AGE-SECRET-KEY-1SE0C0VHQ7HG9YLL60VJARETKRNUSQPLYSM5PVDN3T69MWZU83TPSGHWQU6",
		recipient:   "age18d0q438xdrvmxjfr8vxqyavsh854njcjl6ju6usmxm4fdwae89us56rplc",
		fingerprint: "SHA256:kI9A4DrgAoMa1+sB2XLNpXH1z90utLh091aFJwoem00",
	},
	{
		secretHex: "0707070707070707070707070707070707070707070707070707070707070707",
		secretKey: "AGE-SECRET-KEY-18TKMW5D8WQ25X00VHAFZ0XQ8W7ZNQ6R49DYPRXS6Y5K5XLV2PMKQQLGPXR",
		recipient: "age1xwe7gsg80nue6yvr6legx537254qq2uxm3z0pcy7hqdud56csewq8fgmjm",
	},
	{
		secretHex: "0000000000000000000000000000000000000000000000000000000000000000",
		secretKey: "AGE-SECRET-KEY-1SEJTRDRFH9RK2RZNPSW2HGMYWYW5HEKCCW62WLRTDTEK7LGKS89SAA28XX",
		recipient: "age1s6xt0tz0f9ft87cnjj9est73svrvf2tnyn5fnymkqykhql48uvns49783a",
	},
}

func TestPRFKnownAnswers(t *testing.T) {
	for _, v := range prfVectors {
		secret, _ := hex.DecodeString(v.secretHex)
		id, err := PRFIdentity(secret)
		if err != nil {
			t.Fatal(err)
		}
		if id.String() != v.secretKey || id.Recipient().String() != v.recipient {
			t.Errorf("%s:\n got %s\n     %s\nwant %s\n     %s", v.secretHex, id.String(), id.Recipient(), v.secretKey, v.recipient)
		}
		r, err := PRFRecipient(secret, "l", "c")
		if err != nil || r.Text != v.recipient || r.Type != TypePasskey {
			t.Errorf("%+v %v", r, err)
		}
		if v.fingerprint != "" && r.Fingerprint != v.fingerprint {
			t.Errorf("fingerprint %s, want %s", r.Fingerprint, v.fingerprint)
		}
		if v.seedHex != "" {
			if got := bech32Decode(t, "age-secret-key-", strings.ToLower(v.secretKey)); hex.EncodeToString(got) != v.seedHex {
				t.Errorf("seed %x, want %s", got, v.seedHex)
			}
		}
	}
	// The salt every passkey is asked with is part of the format too.
	if hex.EncodeToString(PRFSalt) != "c9b03ab7dd2e276e2c15c6da079e3420bc56eac3d62875a9e06210e8ba2bc3b8" {
		t.Error("PRFSalt")
	}
}

// bech32Decode is a small independent decoder for the tests: it checks the checksum and returns
// the bytes.
func bech32Decode(t *testing.T, hrp, s string) []byte {
	t.Helper()
	const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
	rest, ok := strings.CutPrefix(s, hrp+"1")
	if !ok {
		t.Fatalf("no %s prefix: %s", hrp, s)
	}
	var data []byte
	for _, c := range rest {
		i := strings.IndexRune(charset, c)
		if i < 0 {
			t.Fatalf("bad character %q", c)
		}
		data = append(data, byte(i))
	}
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	vals := []byte{}
	for _, c := range []byte(hrp) {
		vals = append(vals, c>>5)
	}
	vals = append(vals, 0)
	for _, c := range []byte(hrp) {
		vals = append(vals, c&31)
	}
	for _, v := range append(vals, data...) {
		top := chk >> 25
		chk = chk&0x1ffffff<<5 ^ uint32(v)
		for i := range gen {
			if top>>i&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	if chk != 1 {
		t.Fatalf("bad checksum in %s", s)
	}
	data = data[:len(data)-6]
	var out []byte
	acc, bits := 0, 0
	for _, v := range data {
		acc, bits = acc<<5|int(v), bits+5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= 1<<bits - 1
		}
	}
	return out
}

// ageSecretKey must agree with the age library: for keys it generates, our encoding of the
// bytes is the text it wrote; and for our encoding of chosen bytes, it derives the public key
// crypto/ecdh does.
func TestAgeSecretKeyAgreesWithAgeAndEcdh(t *testing.T) {
	for range 20 {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		raw := bech32Decode(t, "age-secret-key-", strings.ToLower(id.String()))
		if len(raw) != 32 {
			t.Fatalf("%d bytes", len(raw))
		}
		if got := ageSecretKey(raw); got != id.String() {
			t.Errorf("ageSecretKey = %s, age wrote %s", got, id.String())
		}
	}
	seeds := [][]byte{make([]byte, 32), bytes.Repeat([]byte{0xff}, 32)}
	for i := range 10 {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		b[0] = byte(i)
		seeds = append(seeds, b)
	}
	for _, seed := range seeds {
		id, err := age.ParseX25519Identity(ageSecretKey(seed))
		if err != nil {
			t.Fatalf("%x: age does not parse it: %v", seed, err)
		}
		priv, err := ecdh.X25519().NewPrivateKey(seed)
		if err != nil {
			t.Fatal(err)
		}
		want := bech32Encode("age", priv.PublicKey().Bytes())
		if id.Recipient().String() != want {
			t.Errorf("%x: public key %s, ecdh says %s", seed, id.Recipient(), want)
		}
	}
}

func TestParseSharedRejectsABadPasskeyEntry(t *testing.T) {
	ana := newPerson(t, "ana")
	// A passkey entry whose key is an SSH key, junk, or empty is not read as anything.
	for _, key := range []string{ana.r.Text, "age1junk", "", "AGE-SECRET-KEY-1SE0C0VHQ7HG9YLL60VJARETKRNUSQPLYSM5PVDN3T69MWZU83TPSGHWQU6"} {
		if r, err := parseShared(sharedEntry{Type: TypePasskey, Key: key, Label: "x"}); err == nil || r.Key != nil {
			t.Errorf("%q: %+v %v", key, r, err)
		}
	}
	good, _ := PRFRecipient(bytes.Repeat([]byte{3}, 32), "phone", "cred")
	r, err := parseShared(sharedEntry{Type: TypePasskey, Key: good.Text, Label: "phone", Hint: "cred"})
	if err != nil || !r.IsPasskey() || r.Hint != "cred" || r.Fingerprint != good.Fingerprint {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestPRFIdentityRefusesBadLengths(t *testing.T) {
	for _, n := range []int{0, 1, 31, 33, 64} {
		if _, err := PRFIdentity(make([]byte, n)); err == nil {
			t.Errorf("%d bytes", n)
		}
		if _, err := PRFRecipient(make([]byte, n), "", ""); err == nil {
			t.Errorf("%d bytes", n)
		}
	}
}

func TestCheckRecoverableOwnerCountsOnlyTheOwnersKeys(t *testing.T) {
	pk, _ := PRFRecipient(bytes.Repeat([]byte{1}, 32), "a", "c1")
	rec, _ := ParseAgeRecipient(mustRecovery(t))
	bo := newPerson(t, "bo")
	if err := CheckRecoverableOwner([]Recipient{pk}, []Recipient{bo.r}); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("someone else's key is not the owner's recovery: %v", err)
	}
	if err := CheckRecoverable(pk, bo.r); err != nil {
		t.Errorf("CheckRecoverable counts whatever it is given: %v", err)
	}
	if err := CheckRecoverableOwner([]Recipient{pk, rec}, nil); err != nil {
		t.Errorf("%v", err)
	}
	if err := CheckRecoverableOwner(nil, []Recipient{bo.r}); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Errorf("no owner keys: %v", err)
	}
}
