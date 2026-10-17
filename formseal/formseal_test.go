package formseal

import (
	"crypto/ed25519"
	"testing"
)

func key(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestASealHoldsOnlyForTheFieldAsItWasLocked(t *testing.T) {
	k := key(t)
	p := Props{Kind: "text", Name: "order", Label: "Order", Pattern: `[A-Z]+`, Required: true, Options: []string{"a", "b"}}
	sl, err := Make(k, p)
	if err != nil || !Verify(sl, p) || !Holds(k, sl) || Holds(key(t), sl) || Holds(nil, sl) {
		t.Fatalf("seal %+v, %v", sl, err)
	}
	for name, q := range map[string]Props{
		"label":    {Kind: "text", Name: "order", Label: "Order2", Pattern: `[A-Z]+`, Required: true, Options: []string{"a", "b"}},
		"pattern":  {Kind: "text", Name: "order", Label: "Order", Pattern: `.*`, Required: true, Options: []string{"a", "b"}},
		"required": {Kind: "text", Name: "order", Label: "Order", Pattern: `[A-Z]+`, Options: []string{"a", "b"}},
		"options":  {Kind: "text", Name: "order", Label: "Order", Pattern: `[A-Z]+`, Required: true, Options: []string{"a"}},
		"moved":    {Kind: "text", Name: "orde", Label: "rOrder", Pattern: `[A-Z]+`, Required: true, Options: []string{"a", "b"}},
	} {
		if Verify(sl, q) {
			t.Errorf("a changed %s still verifies", name)
		}
	}
	other, _ := Make(key(t), p)
	forged := Seal{Key: other.Key, Sig: sl.Sig}
	if Verify(forged, p) || Verify(Seal{}, p) || Verify(Seal{Key: "!", Sig: "!"}, p) {
		t.Error("a signature from another key, or nothing, verifies")
	}
}

func TestLengthsKeepNeighbouringFieldsApart(t *testing.T) {
	k := key(t)
	a := Props{Name: "ab", Label: "c"}
	b := Props{Name: "a", Label: "bc"}
	sl, _ := Make(k, a)
	if Verify(sl, b) {
		t.Error("moving a character between two properties must change the message")
	}
}

func TestFingerprintIsStableAndShort(t *testing.T) {
	k := key(t)
	sl, _ := Make(k, Props{})
	f := Fingerprint(sl.Key)
	if len(f) != 19 || f != Fingerprint(sl.Key) || Fingerprint("nope") != "" {
		t.Errorf("fingerprint %q", f)
	}
}
