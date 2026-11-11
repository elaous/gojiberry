package pack

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, k, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func pubOf(k ed25519.PrivateKey) []byte { return k.Public().(ed25519.PublicKey) }

func TestCosignAddsASignerAndKeepsTheOthersValid(t *testing.T) {
	a, b, c := newKey(t), newKey(t), newKey(t)
	f := &memFile{b: build(t, a)}
	if err := Cosign(f, int64(len(f.b)), b); err != nil {
		t.Fatal(err)
	}
	if err := Cosign(f, int64(len(f.b)), c); err != nil {
		t.Fatal(err)
	}
	r, err := Open(f, int64(len(f.b)))
	if err != nil {
		t.Fatal(err)
	}
	signers := r.Signers()
	if len(signers) != 3 || !bytes.Equal(signers[0].PublicKey, pubOf(a)) || !bytes.Equal(signers[1].PublicKey, pubOf(b)) || !bytes.Equal(signers[2].PublicKey, pubOf(c)) {
		t.Fatalf("signers %+v", signers)
	}
	for _, s := range signers {
		if !s.Valid {
			t.Errorf("signer %x is not valid", s.PublicKey[:4])
		}
	}
	if rep := r.Verify(); !rep.OK() || len(rep.Signers) != 3 || !bytes.Equal(rep.Signer, pubOf(a)) {
		t.Errorf("report %+v", rep)
	}
	if sig, valid := r.Signature(); sig == nil || !valid || !bytes.Equal(sig.PublicKey, pubOf(a)) {
		t.Error("the first signature is the pack's signature as ever")
	}
}

func TestCosignRefusals(t *testing.T) {
	a, b := newKey(t), newKey(t)
	unsigned := &memFile{b: build(t, nil)}
	if err := Cosign(unsigned, int64(len(unsigned.b)), a); !errors.Is(err, ErrNotSigned) {
		t.Errorf("unsigned: %v", err)
	}
	f := &memFile{b: build(t, a)}
	if err := Cosign(f, int64(len(f.b)), a); !errors.Is(err, ErrAlreadySigned) {
		t.Errorf("the same key twice: %v", err)
	}
	g := &memFile{b: build(t, a)}
	g.b[len(g.b)-footerSize+30] ^= 1
	if _, err := Open(g, int64(len(g.b))); err == nil {
		if err := Cosign(g, int64(len(g.b)), b); err == nil {
			t.Error("a pack whose signature does not hold was co-signed")
		}
	}
}

func TestOneBadCosignatureFailsThePackAndNamesTheSigner(t *testing.T) {
	a, b := newKey(t), newKey(t)
	f := &memFile{b: build(t, a)}
	if err := Cosign(f, int64(len(f.b)), b); err != nil {
		t.Fatal(err)
	}
	r, err := Open(f, int64(len(f.b)))
	if err != nil {
		t.Fatal(err)
	}
	r.sig.More[0].Sig[0] ^= 0xff
	s := r.Signers()
	if len(s) != 2 || !s[0].Valid || s[1].Valid {
		t.Errorf("signers %+v", s)
	}
	if rep := r.Verify(); rep.OK() || rep.SignatureOK {
		t.Error("a pack with a signature that does not hold passes")
	}
}

func TestWriterCloseSignedWithSeveralKeys(t *testing.T) {
	a, b := newKey(t), newKey(t)
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Meta{ID: "p", Name: "n", Created: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add(doc(id1, "one", "first")); err != nil {
		t.Fatal(err)
	}
	if err := w.CloseSigned(a, b); err != nil {
		t.Fatal(err)
	}
	r, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Signers(); len(s) != 2 || !s[0].Valid || !s[1].Valid {
		t.Errorf("signers %+v", s)
	}
	many := make([]ed25519.PrivateKey, MaxSigners+1)
	for i := range many {
		many[i] = newKey(t)
	}
	var b2 bytes.Buffer
	w2, _ := NewWriter(&b2, Meta{ID: "p"})
	if err := w2.CloseSigned(many...); err == nil {
		t.Error("more signers than the limit were accepted")
	}
}

func TestAppendingDropsTheOtherSignersSoTheyMustSignAgain(t *testing.T) {
	a, b := newKey(t), newKey(t)
	f := &memFile{b: build(t, a)}
	if err := Cosign(f, int64(len(f.b)), b); err != nil {
		t.Fatal(err)
	}
	ap, err := OpenAppend(f, int64(len(f.b)), a)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Add(doc(id3, "three", "third")); err != nil {
		t.Fatal(err)
	}
	if err := ap.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(f, int64(len(f.b)))
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Signers(); len(s) != 1 || !s[0].Valid || !bytes.Equal(s[0].PublicKey, pubOf(a)) {
		t.Errorf("after an append only the key that signed again is left: %+v", r.Signers())
	}
}

func TestSignatureRecordWithNestedSignersIsRefused(t *testing.T) {
	inner := Signature{Alg: "ed25519", PublicKey: make([]byte, 32), Sig: make([]byte, 64), More: []Signature{{Alg: "ed25519"}}}
	outer := Signature{Alg: "ed25519", PublicKey: make([]byte, 32), Sig: make([]byte, 64), More: []Signature{inner}}
	if _, err := unmarshalSignature(outer.marshal()); err == nil {
		t.Error("a signer inside a signer was read")
	}
}
