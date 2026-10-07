package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"testing"

)

// Tests of RepairAllOptions.Resign and the BySigner report (docs/roadmap.md, backlog item 5): moving
// a store from a retired owner signer to a new one.

// signerRotation is the world after the owner got a new signer: the clients that sign hold it and list
// both keys, one that has not rotated yet holds only the old.
type signerRotation struct {
	w       *world
	newKey  *Signer
	sharing *Sharing // new Signer, lists both
	ad      *Adapter
}

func (w *world) rotate(t *testing.T) signerRotation {
	t.Helper()
	nk, err := NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ring := w.ring
	ring.People.OwnerSigners = []ed25519.PublicKey{w.signer.Public(), nk.Public()}
	ad := New(w.inner, ring)
	ad.Signer = nk
	return signerRotation{w: w, newKey: nk, ad: ad, sharing: &Sharing{Store: w.inner, Adapter: ad, Signer: nk, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}}}
}

// onlyNew is an adapter that has dropped the old key from every list (procedure step 3).
func (r signerRotation) onlyNew() *Adapter {
	ring := r.w.ring
	ring.People.OwnerSigners = []ed25519.PublicKey{r.newKey.Public()}
	ad := New(r.w.inner, ring)
	ad.Signer = r.newKey
	return ad
}

// signersOf says, per file of the document, which key signed its frame.
func (r signerRotation) signersOf(t *testing.T, id string) map[string]string {
	t.Helper()
	out := map[string]string{}
	files := r.w.files(t, id)
	if len(files) == 0 {
		files = []string{id} // an item that is not a document's
	}
	for _, f := range files {
		d, err := r.w.inner.Load(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := r.ad.peekFrame(context.Background(), f, d.Data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out[f] = SignerFingerprint(r.ad.frameSigner(f, fi))
	}
	return out
}

func (r signerRotation) recordBy(id string) string {
	k := r.sharing.recordSigner(context.Background(), id)
	if k == nil {
		return ""
	}
	return SignerFingerprint(k)
}

func (r signerRotation) allBy(t *testing.T, id, fp string) {
	t.Helper()
	for f, got := range r.signersOf(t, id) {
		if got != fp {
			t.Errorf("%s is signed by %s, want %s", f, got, fp)
		}
	}
	if got := r.recordBy(id); got != fp {
		t.Errorf("record of %s is signed by %s, want %s", id, got, fp)
	}
}

func TestRepairAllReportsFramesBySignerAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	a, b := w.e2eeDoc(t), w.e2eeDoc(t)
	r := w.rotate(t)
	oldFP, newFP := SignerFingerprint(w.signer.Public()), SignerFingerprint(r.newKey.Public())
	// One document already moved: saved by the new signer (a Save writes one file, not the record).
	if err := r.ad.Save(ctx, w.mainFile(t, b), []byte("saved after the signerRotation")); err != nil {
		t.Fatal(err)
	}
	before := w.snapshot(t)

	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || rep.Repaired != 0 || rep.Resigned != 0 || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	sameStore(t, "a report without Resign", before, w.snapshot(t))
	if len(rep.OtherSigner) != 2 || rep.OtherSigner[0] != minStr(a, b) {
		t.Errorf("OtherSigner = %v", rep.OtherSigner)
	}
	by := map[string]SignerFrames{}
	for _, s := range rep.BySigner {
		by[s.Fingerprint] = s
	}
	if len(by) != 2 || by[oldFP].Current || !by[newFP].Current {
		t.Fatalf("%+v", rep.BySigner)
	}
	if by[newFP].Files != 1 || by[newFP].Docs != 1 || by[newFP].Records != 0 {
		t.Errorf("new signer: %+v", by[newFP])
	}
	if by[oldFP].Docs != 2 || by[oldFP].Records != 2 || by[oldFP].Files != len(w.files(t, a))+len(w.files(t, b))-1 {
		t.Errorf("old signer: %+v (files %d)", by[oldFP], len(w.files(t, a))+len(w.files(t, b)))
	}
	// Dry run with Resign: Todo, nothing written.
	rep, err = r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true, DryRun: true})
	if err != nil || len(rep.Todo) != 2 || rep.Resigned != 0 || len(rep.OtherSigner) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	sameStore(t, "a dry run", before, w.snapshot(t))
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func TestRepairAllResignMovesEverythingToTheNewSignerAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	docs := []string{w.e2eeDoc(t), w.e2eeDoc(t), w.history(t)}
	// A document with a v2 frame as well: Repaired, not Resigned.
	legacy := w.e2eeDoc(t)
	w.legacyV2(t, legacy)
	// A non-document item signed by the old key.
	item := "expiry:0b1c2d3e-0000-4000-8000-0000000000ee"
	if err := w.ad.Save(ctx, item, []byte("soon")); err != nil {
		t.Fatal(err)
	}
	r := w.rotate(t)
	oldFP, newFP := SignerFingerprint(w.signer.Public()), SignerFingerprint(r.newKey.Public())
	for _, d := range docs[:2] {
		r.allBy(t, d, oldFP)
	}

	// Before: a client that dropped the old key refuses every one of them.
	strict := r.onlyNew()
	if _, err := strict.Load(ctx, w.mainFile(t, docs[0])); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("before the re-sign: %v", err)
	}

	var outcomes []string
	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true, Progress: func(p RepairProgress) { outcomes = append(outcomes, p.Outcome) }})
	if err != nil || rep.Left() != 0 || len(rep.Skipped) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	if rep.Resigned < 3 || rep.Repaired != 1 || len(rep.OtherSigner) != 0 || rep.Total != rep.Signed+rep.Repaired+rep.Resigned {
		t.Fatalf("%+v", rep)
	}
	for _, d := range docs[:2] {
		r.allBy(t, d, newFP)
	}
	r.allBy(t, legacy, newFP)
	if got := r.signersOf(t, item)[item]; got != newFP {
		t.Errorf("the item is signed by %s", got)
	}
	if len(rep.BySigner) == 0 {
		t.Fatalf("%+v", rep)
	}
	// After step 3 everything still opens, with the old key gone from every list.
	for _, d := range append(docs[:2], legacy) {
		if _, err := strict.Load(ctx, w.mainFile(t, d)); err != nil {
			t.Errorf("%s (legacy=%v) after dropping the old key: %v", d, d == legacy, err)
		}
	}
	// And a frame the old key signs now is forged.
	f := w.mainFile(t, docs[0])
	rs, err := strict.Ring.Recipients(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := w.inner.Load(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := strict.peekFrame(ctx, f, stored.Data)
	if err != nil {
		t.Fatal(err)
	}
	cur := fi.b
	if err := w.inner.Save(ctx, f, sealRaw(t, rs, frame(f, cur, []byte("by the revoked key"), w.signer))); err != nil {
		t.Fatal(err)
	}
	if _, err := strict.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Errorf("a frame by the revoked key after step 3: %v", err)
	}
	// While the old key is still listed, the frame it wrote opens and is moved on the next run;
	// then a second run writes nothing and finds only the current signer.
	if rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true}); err != nil || rep.Resigned != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	before := w.snapshot(t)
	rep, err = r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.Repaired != 0 || rep.Resigned != 0 || rep.Left() != 0 || len(rep.OtherSigner) != 0 {
		t.Fatalf("second run: %v %+v", err, rep)
	}
	if len(rep.BySigner) != 1 || !rep.BySigner[0].Current || rep.BySigner[0].Fingerprint != newFP {
		t.Errorf("second run BySigner: %+v", rep.BySigner)
	}
	sameStore(t, "the second run", before, w.snapshot(t))
}

func TestRepairAllResignOnlyTheRecord(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	r := w.rotate(t)
	// Frames already by the new key, the record still by the old one.
	if _, err := r.sharing.Repair(ctx, id); err != nil {
		t.Fatal(err)
	}
	newFP := SignerFingerprint(r.newKey.Public())
	for f, fp := range r.signersOf(t, id) {
		if fp != newFP {
			t.Fatalf("%s: %s", f, fp)
		}
	}
	if r.recordBy(id) == newFP {
		t.Fatal("the record moved on its own")
	}
	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.Resigned != 1 || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	r.allBy(t, id, newFP)
	if _, err := r.onlyNew().Load(ctx, w.mainFile(t, id)); err != nil {
		t.Fatal(err)
	}
}

func TestRepairAllResignWithoutASignerDoesNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.e2eeDoc(t)
	r := w.rotate(t)
	ad := New(w.inner, r.ad.Ring)
	s := &Sharing{Store: w.inner, Adapter: ad}
	before := w.snapshot(t)
	rep, err := s.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.Resigned != 0 || len(rep.OtherSigner) != 0 || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	for _, s := range rep.BySigner {
		if s.Current {
			t.Errorf("no signer, but %+v is current", s)
		}
	}
	sameStore(t, "no signer", before, w.snapshot(t))
}

func TestRepairAllResignWithConcurrentSaves(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	others := []string{w.e2eeDoc(t), w.e2eeDoc(t)}
	r := w.rotate(t)
	f := w.mainFile(t, id)
	const writes = 40
	var wg sync.WaitGroup
	werr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			if err := r.ad.Save(ctx, f, []byte(fmt.Sprintf("w-%d", i))); err != nil {
				werr <- err
				return
			}
		}
	}()
	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	wg.Wait()
	select {
	case err := <-werr:
		t.Fatalf("a concurrent Save failed: %v", err)
	default:
	}
	if err != nil || len(rep.Skipped) != 0 {
		t.Fatalf("%v %+v", err, rep.Skipped)
	}
	// A save that raced the scan was signed by the new key (this adapter's); another run settles
	// anything else and the last write is the one stored.
	if rep2, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true}); err != nil || len(rep2.OtherSigner) != 0 || rep2.Left() != 0 {
		t.Fatalf("%v %+v", err, rep2)
	}
	d, err := r.ad.Load(ctx, f)
	if err != nil || string(d.Data) != fmt.Sprintf("w-%d", writes-1) {
		t.Fatalf("the last concurrent Save was lost: %q %v", d.Data, err)
	}
	for _, doc := range append(others, id) {
		r.allBy(t, doc, SignerFingerprint(r.newKey.Public()))
	}
}

func TestRepairAllResignStopsOnAFrameNoListedKeySigned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	r := w.rotate(t)
	// An adapter that does not list the old key cannot move its frames: forged, reported, not written.
	s := &Sharing{Store: w.inner, Adapter: r.onlyNew(), Signer: r.newKey}
	before := w.snapshot(t)
	rep, err := s.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || skippedBy(rep)[id] != SkipForged || rep.Resigned != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	sameStore(t, "an unlisted old key", before, w.snapshot(t))
}
