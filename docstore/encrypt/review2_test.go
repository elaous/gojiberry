package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

// Tests of the second review round of PR 83: the window between a re-key and the change of the
// record to Signed, the way out of the lockout it leaves, and the document id in the signature.

// otherClient is a client that has no Signer (an older build, or a browser that is not the
// owner's): it saves server-level documents with the unsigned v2 frame while the record does
// not say Signed.
func (w *world) otherClient() *Adapter { return New(w.inner, w.ring) }

func mustNotChange(t *testing.T, w *world, f string, before []byte) {
	t.Helper()
	d, err := w.inner.Load(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.Data, before) {
		t.Errorf("%s was rewritten", f)
	}
}

// A client without the Signer that saves between the re-key and the change of the record is
// noticed by the owner before the record says Signed: the file is sealed again, signed, and the
// document is not locked.
func TestUnsignedSaveBetweenRekeyAndSignedFlipIsResealed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if b := w.binding(t, w.mainFile(t, id)); b.Signed {
		t.Fatal("setup: a document that was never re-keyed is not marked signed")
	}
	f := id + "~cccccccccccc.open"
	other := w.otherClient()
	fired := 0
	w.sharing.beforeFlip = func() {
		if fired++; fired == 1 {
			if err := other.Save(ctx, f, []byte("latest")); err != nil {
				t.Error(err)
			}
			if v := w.frameVersion(t, f); v != 2 {
				t.Errorf("setup: the other client's frame is v%d, want the unsigned v2", v)
			}
		}
	}
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if !w.binding(t, f).Signed {
		t.Fatal("the record does not say the files are signed")
	}
	for _, g := range w.files(t, id) {
		if v := w.frameVersion(t, g); v != 3 {
			t.Errorf("%s is v%d: the flip left an unsigned file under a Signed record", g, v)
		}
	}
	if d, err := w.ad.Load(ctx, f); err != nil || string(d.Data) != "latest" {
		t.Fatalf("the save made in the window was lost or locked: %v", err)
	}
	if err := w.ad.Save(ctx, f, []byte("next")); err != nil {
		t.Errorf("Save after the flip: %v", err)
	}
}

// lockedDocument leaves the state the window of finishFiles leaves (a save by a client without
// the Signer after the owner's last look and before the record is written): an unsigned file
// under a record that says Signed.
func lockedDocument(t *testing.T, w *world, kind string) (id, f string) {
	t.Helper()
	ctx := context.Background()
	id = w.history(t)
	f = id + "~cccccccccccc.open"
	other := w.otherClient()
	w.sharing.afterRead = func() {
		w.sharing.afterRead = nil
		if kind == "v1" {
			t.Fatal("not used")
		}
		if err := other.Save(ctx, f, []byte("latest")); err != nil {
			t.Error(err)
		}
	}
	// The re-key's second look, after it flipped, finds the file saved in the window: the owner
	// is told (the record does say Signed).
	if _, err := w.sharing.Repair(ctx, id); !errors.Is(err, ErrUnsignedAfterFlip) || !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("setup: Repair = %v, want ErrUnsignedAfterFlip wrapping ErrForgedFrame", err)
	}
	if !w.binding(t, f).Signed {
		t.Fatal("setup: the record says Signed")
	}
	if v := w.frameVersion(t, f); v != 2 {
		t.Fatalf("setup: the file is v%d", v)
	}
	return id, f
}

func TestLockoutIsRefusedByDefaultAndRepairedWithAcceptUnsigned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, f := lockedDocument(t, w, "v2")
	raw, _ := w.inner.Load(ctx, f)

	// What the lockout is: every operation refuses the file.
	if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Load: %v", err)
	}
	if err := w.ad.Save(ctx, id+"~dddddddddddd.open", []byte("x")); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Save: %v", err)
	}
	// The default is to refuse, with an error that names the file and the option.
	_, err := w.sharing.Repair(ctx, id)
	if !errors.Is(err, ErrForgedFrame) || !strings.Contains(err.Error(), f) || !strings.Contains(err.Error(), "AcceptUnsigned") {
		t.Fatalf("Repair without the option: %v", err)
	}
	if _, err := w.sharing.Recover(ctx, id, "server", nil); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Recover without the option: %v", err)
	}
	mustNotChange(t, w, f, raw.Data)

	// The owner confirms: the file is sealed again, signed, with its content.
	if _, err := w.sharing.Repair(ctx, id, AcceptUnsigned()); err != nil {
		t.Fatalf("Repair with AcceptUnsigned: %v", err)
	}
	if v := w.frameVersion(t, f); v != 3 {
		t.Errorf("v%d after the repair", v)
	}
	if d, err := w.ad.Load(ctx, f); err != nil || string(d.Data) != "latest" {
		t.Fatalf("the latest save was lost: %v", err)
	}
	if err := w.ad.Save(ctx, id+"~dddddddddddd.open", []byte("x")); err != nil {
		t.Errorf("Save after the repair: %v", err)
	}
	if !w.binding(t, f).Signed {
		t.Error("the record lost its Signed claim")
	}
}

func TestRecoverWithAcceptUnsignedGetsOutOfTheLockout(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, f := lockedDocument(t, w, "v2")
	if _, err := w.sharing.Recover(ctx, id, "server", nil, AcceptUnsigned()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if d, err := w.ad.Load(ctx, f); err != nil || string(d.Data) != "latest" {
		t.Fatalf("after Recover: %v", err)
	}
	if !w.binding(t, f).Signed {
		t.Error("Recover dropped the Signed claim")
	}
}

// An older build that wrote v1/v2 frames under a record that already says Signed: the same way out.
func TestLegacyV1FilesUnderASignedRecordAreRecoverable(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	w.legacyV1(t, id)
	w.rewriteRecord(t, id, func(r *sharedRecord) { r.Signed = true })
	f := w.mainFile(t, id)
	if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("setup: %v", err)
	}
	if _, err := w.sharing.Repair(ctx, id); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("default: %v", err)
	}
	if _, err := w.sharing.Repair(ctx, id, AcceptUnsigned()); err != nil {
		t.Fatalf("Repair: %v", err)
	}
	if _, err := w.ad.Load(ctx, f); err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, g := range w.files(t, id) {
		if v := w.frameVersion(t, g); v != 3 {
			t.Errorf("%s is v%d", g, v)
		}
	}
}

// AcceptUnsigned launders unsigned frames only: a v3 frame whose signature is bad is a forgery,
// and nothing is written over it by Repair or Recover, with or without the option.
func TestBadSignatureV3FileIsNeverResealed(t *testing.T) {
	ctx := context.Background()
	attacker, _ := NewSigner(bytes.Repeat([]byte{7}, 32))
	for name, tamper := range map[string]func(t *testing.T, w *world, f string, rb Binding) []byte{
		"signed by a key that is not an owner's": func(t *testing.T, w *world, f string, rb Binding) []byte {
			return sealRaw(t, w.publicRecipients(), frame(f, Binding{Level: ProtectionE2EE, Seq: rb.Seq, Hash: rb.Hash}, []byte("forged"), attacker))
		},
		"a genuine header on other content": func(t *testing.T, w *world, f string, rb Binding) []byte {
			d, _ := w.inner.Load(ctx, f)
			r, _, err := w.ad.decrypt(ctx, f, d.Data)
			if err != nil {
				t.Fatal(err)
			}
			framed := readAll(t, r)
			data, _, err := unframe(f, framed)
			if err != nil {
				t.Fatal(err)
			}
			return sealRaw(t, w.publicRecipients(), append(append([]byte(nil), framed[:len(framed)-len(data)]...), "forged"...))
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			id := w.e2eeDoc(t)
			f := w.mainFile(t, id)
			rb := w.binding(t, f)
			bad := tamper(t, w, f, rb)
			if err := w.inner.Save(ctx, f, bad); err != nil {
				t.Fatal(err)
			}
			for _, opts := range [][]RepairOption{nil, {AcceptUnsigned()}} {
				if _, err := w.sharing.Repair(ctx, id, opts...); !errors.Is(err, ErrForgedFrame) {
					t.Errorf("Repair (%d options): %v", len(opts), err)
				}
				if _, err := w.sharing.Recover(ctx, id, ProtectionE2EE, nil, opts...); !errors.Is(err, ErrForgedFrame) {
					t.Errorf("Recover (%d options): %v", len(opts), err)
				}
				mustNotChange(t, w, f, bad)
			}
		})
	}
}

// AcceptUnsigned does not excuse an unsigned file that contradicts the record: one claiming a
// newer record than there is is still a replay finding.
func TestAcceptUnsignedStillChecksSeqAndLevel(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	f := w.mainFile(t, id)
	rb := w.binding(t, f)
	ahead := sealRaw(t, w.publicRecipients(), frame(f, Binding{Level: ProtectionE2EE, Seq: rb.Seq + 5, Hash: rb.Hash}, []byte("x"), nil))
	if err := w.inner.Save(ctx, f, ahead); err != nil {
		t.Fatal(err)
	}
	if _, err := w.sharing.Repair(ctx, id, AcceptUnsigned()); !errors.Is(err, docstore.ErrDowngrade) {
		t.Fatalf("Repair: %v", err)
	}
	mustNotChange(t, w, f, ahead)
}

func TestAcceptUnsignedNeedsTheAdapterSigner(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, _ := lockedDocument(t, w, "v2")
	s := &Sharing{Store: w.inner, Adapter: w.otherClient(), Signer: w.signer}
	if _, err := s.Repair(ctx, id, AcceptUnsigned()); !errors.Is(err, ErrNoFrameSigner) {
		t.Fatalf("Repair: %v", err)
	}
}

// ---- the document id is part of what a frame's signature covers ------------------------------

// A genuine signed frame of one document put into another (with the id in its header changed,
// as an attacker who knows both recipient lists can) is refused because the signature does not
// cover that id: not merely because the header id differs, which unframe checks and which would
// hide an id left out of the signed message.
func TestFrameSignatureCoversTheDocumentID(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	idA, idB := w.e2eeDoc(t), w.e2eeDoc(t)
	fa, fb := w.mainFile(t, idA), w.mainFile(t, idB)
	ba, bb := w.binding(t, fa), w.binding(t, fb)
	if ba.Level != bb.Level || ba.Seq != bb.Seq || ba.Hash != bb.Hash {
		t.Fatalf("setup: the two documents must have the same level, seq and recipient list: %+v %+v", ba, bb)
	}
	d, _ := w.inner.Load(ctx, fa)
	r, _, err := w.ad.decrypt(ctx, fa, d.Data)
	if err != nil {
		t.Fatal(err)
	}
	framed := readAll(t, r)
	data, fi, err := unframe(fa, framed)
	if err != nil {
		t.Fatal(err)
	}

	// frameSigMessage directly: the signature verifies for A's id and for no other.
	pub := w.signer.Public()
	msgA := frameSigMessage(fa, levelByte(fi.b.Level), fi.b.Seq, fi.b.Hash, fi.content)
	msgB := frameSigMessage(fb, levelByte(fi.b.Level), fi.b.Seq, fi.b.Hash, fi.content)
	if !ed25519.Verify(pub, msgA, fi.sig) {
		t.Fatal("setup: the genuine signature does not verify for its own id")
	}
	if bytes.Equal(msgA, msgB) || ed25519.Verify(pub, msgB, fi.sig) {
		t.Fatal("the signed message does not depend on the document id")
	}

	// The replay: A's frame with B's id in the header, which unframe accepts for B.
	i := bytes.Index(framed, []byte(fa))
	if i < len(frameMagic) {
		t.Fatal("setup: cannot find the id in the header")
	}
	replay := append([]byte(nil), framed[:len(frameMagic)]...)
	replay = binary.AppendUvarint(replay, uint64(len(fb)))
	replay = append(replay, fb...)
	replay = append(replay, framed[i+len(fa):]...)
	_, rfi, err := unframe(fb, replay)
	if err != nil {
		t.Fatalf("setup: the header id is B's, unframe must accept it: %v", err)
	}
	err = w.ad.verifyFrame(fb, rfi, data, true, false)
	if !errors.Is(err, ErrForgedFrame) || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("verifyFrame of A's signature over B's id: %v", err)
	}
	// And end to end, through Load of B's file.
	if err := w.inner.Save(ctx, fb, sealRaw(t, w.publicRecipients(), replay)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ad.Load(ctx, fb); !errors.Is(err, ErrForgedFrame) || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("Load of the replayed frame: %v", err)
	}
}

// ---- Verify keeps what the pending check found beside the record it could not verify ----------

func TestVerifyKeepsPendingResultsWhenOneRecordDoesNotVerify(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.saver.PendingCheck = PendingHook(w.sharing)
	good := w.history(t)
	w.inner.failOn.Store(func() string {
		for _, f := range w.files(t, good) {
			if strings.HasPrefix(f, "versions/") {
				return f
			}
		}
		return ""
	}())
	_ = w.saver.SetProtection(ctx, "m", good, "e2ee") // cut short: the record is pending
	w.inner.failOn.Store("")
	if p, _ := w.sharing.Pending(ctx, good); !p {
		t.Fatal("setup: the record is pending")
	}
	bad := "11111111-2222-4333-8444-555555555555"
	if err := w.inner.Save(ctx, KeysPrefix+bad, []byte(`{"payload":"x","sig":"y"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.sharing.PendingDocs(ctx); err == nil {
		t.Fatal("setup: the bad record must fail to verify")
	}
	rep, err := w.saver.Verify(ctx, "m")
	if err != nil {
		t.Fatalf("one bad record aborted Verify: %v", err)
	}
	if len(rep.Pending) != 1 || rep.Pending[0] != good {
		t.Errorf("the pending documents it could read were dropped: %+v", rep.Pending)
	}
	if len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], bad) {
		t.Errorf("the failure is not a finding naming the record: %v", rep.Warnings)
	}
}
