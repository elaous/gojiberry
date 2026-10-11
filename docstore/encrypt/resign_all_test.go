package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
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

func TestRepairAllResignNeedsTheSharingSignerToBeTheAdapters(t *testing.T) {
	ctx := context.Background()
	for name, mod := range map[string]func(r signerRotation) *Sharing{
		"nil": func(r signerRotation) *Sharing {
			return &Sharing{Store: r.w.inner, Adapter: r.ad, OwnerSigners: r.sharing.OwnerSigners}
		},
		"different": func(r signerRotation) *Sharing {
			return &Sharing{Store: r.w.inner, Adapter: r.ad, Signer: r.w.signer, OwnerSigners: r.sharing.OwnerSigners}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			id := w.e2eeDoc(t)
			r := w.rotate(t)
			s := mod(r)
			before := w.snapshot(t)
			for i := 0; i < 2; i++ { // the same on every run, nothing counted Resigned, nothing moved
				rep, err := s.RepairAll(ctx, RepairAllOptions{Resign: true})
				if err != nil || rep.Resigned != 0 || skippedBy(rep)[id] != SkipNoSigner || rep.IndexResigned || rep.IndexErr == nil {
					t.Fatalf("run %d: %v %+v", i, err, rep)
				}
				if dry, err := s.RepairAll(ctx, RepairAllOptions{Resign: true, DryRun: true}); err != nil || skippedBy(dry)[id] != SkipNoSigner || len(dry.Todo) != 0 {
					t.Fatalf("dry run: %v %+v", err, dry)
				}
			}
			sameStore(t, "a mismatched Sharing signer", before, w.snapshot(t))
		})
	}
}

// A Sharing whose Signer is another *Signer built from the same private key as the Adapter's is
// the same signer (the comparison is of public keys, not of pointers): the document is re-signed
// as usual and not skipped.
func TestRepairAllResignAcceptsADistinctSignerOfTheSameKey(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	r := w.rotate(t)
	twin, err := NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if twin == r.ad.Signer || !bytes.Equal(twin.Public(), r.ad.Signer.Public()) {
		t.Fatal("setup: want a distinct Signer of the same key")
	}
	s := &Sharing{Store: w.inner, Adapter: r.ad, Signer: twin, OwnerSigners: r.sharing.OwnerSigners}
	rep, err := s.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.Resigned != 1 || len(rep.Skipped) != 0 || rep.IndexErr != nil || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	r.allBy(t, id, SignerFingerprint(r.newKey.Public()))
}

// An index with no entries and seq 0 signed by the old key is left as it is by ResignIndex (there
// is nothing to sign), so RepairAll must not report it as IndexResigned.
func TestRepairAllResignDoesNotReportAnIndexItDidNotWrite(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	r := w.rotate(t)
	payload, err := json.Marshal(indexRecord{V: 1})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(signedRecord{Payload: payload, Sig: ed25519.Sign(w.signer.key, indexMessage(payload)), Signer: w.signer.Public()})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.inner.Save(ctx, IndexKey, data); err != nil {
		t.Fatal(err)
	}
	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || !rep.IndexOther || rep.IndexResigned || rep.IndexErr != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if d, err := w.inner.Load(ctx, IndexKey); err != nil || !bytes.Equal(d.Data, data) {
		t.Errorf("the empty index was rewritten: %v", err)
	}
}

// failAfterRecord fails the writes of one file, so a re-sign stops after the record was signed.
func TestRepairAllResignCrashBetweenRecordAndFiles(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	r := w.rotate(t)
	oldFP, newFP := SignerFingerprint(w.signer.Public()), SignerFingerprint(r.newKey.Public())
	f := w.mainFile(t, id)
	w.inner.failOn.Store(f)
	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || skippedBy(rep)[id] != SkipError || rep.Resigned != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	// The record moved, the file did not: with both keys listed everything still opens.
	if got := r.recordBy(id); got != newFP {
		t.Fatalf("the record is signed by %s, want the new key", got)
	}
	if got := r.signersOf(t, id)[f]; got != oldFP {
		t.Fatalf("the file is signed by %s", got)
	}
	if _, err := r.ad.Load(ctx, f); err != nil {
		t.Fatalf("both keys listed, after the crash: %v", err)
	}
	// The next run finishes it.
	w.inner.failOn.Store("")
	rep, err = r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.Left() != 0 || rep.Resigned != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	r.allBy(t, id, newFP)
	if _, err := r.onlyNew().Load(ctx, f); err != nil {
		t.Fatal(err)
	}
}

func TestRepairAllResignDryRunAndLimit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	docs := []string{w.e2eeDoc(t), w.e2eeDoc(t), w.e2eeDoc(t)}
	r := w.rotate(t)
	newFP := SignerFingerprint(r.newKey.Public())
	before := w.snapshot(t)

	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true, DryRun: true})
	if err != nil || len(rep.Todo) != 3 || rep.Resigned != 0 || len(rep.OtherSigner) != 0 || rep.Left() != 3 || !rep.IndexOther || rep.IndexResigned {
		t.Fatalf("%v %+v", err, rep)
	}
	// Without Resign the same documents are Signed and OtherSigner, not Todo.
	rep, err = r.sharing.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || len(rep.Todo) != 0 || len(rep.OtherSigner) != 3 || rep.Signed != 3 || !rep.IndexOther {
		t.Fatalf("%v %+v", err, rep)
	}
	sameStore(t, "dry runs", before, w.snapshot(t))

	rep, err = r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true, Limit: 2})
	if err != nil || rep.Resigned != 2 || rep.Remaining != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
	done := 0
	for _, d := range docs {
		if r.recordBy(d) == newFP {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("%d documents moved with Limit 2", done)
	}
	rep, err = r.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.Resigned != 1 || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	for _, d := range docs {
		r.allBy(t, d, newFP)
	}
}

func TestRepairAllResignSignsTheIndexAndItLoadsStrict(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	r := w.rotate(t)
	newOnly := []ed25519.PublicKey{r.newKey.Public()}
	if _, _, err := loadIndex(ctx, w.inner, newOnly, nil); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("the index before: %v", err)
	}
	// A run without Resign only reports it.
	rep, err := r.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || !rep.IndexOther || rep.IndexResigned || !bytes.Equal(rep.IndexBy, w.signer.Public()) {
		t.Fatalf("%v %+v", err, rep)
	}
	if _, _, err := loadIndex(ctx, w.inner, newOnly, nil); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("a report-only run changed the index: %v", err)
	}
	// Sharing.ResignIndex alone makes it load with only the new key, entries unchanged.
	if err := r.sharing.ResignIndex(ctx); err != nil {
		t.Fatal(err)
	}
	idx, _, err := loadIndex(ctx, w.inner, newOnly, nil)
	if err != nil || idx.Docs[id].Level != ProtectionE2EE {
		t.Fatalf("%v %+v", err, idx)
	}
	// RepairAll with Resign does it too, and a second run finds nothing to do.
	w2 := newWorld(t)
	w2.e2eeDoc(t)
	r2 := w2.rotate(t)
	rep, err = r2.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || !rep.IndexOther || !rep.IndexResigned || rep.IndexErr != nil {
		t.Fatalf("%v %+v", err, rep)
	}
	if _, _, err := loadIndex(ctx, w2.inner, []ed25519.PublicKey{r2.newKey.Public()}, nil); err != nil {
		t.Fatalf("the index after RepairAll: %v", err)
	}
	rep, err = r2.sharing.RepairAll(ctx, RepairAllOptions{Resign: true})
	if err != nil || rep.IndexOther || rep.IndexResigned || !bytes.Equal(rep.IndexBy, r2.newKey.Public()) {
		t.Fatalf("second run: %v %+v", err, rep)
	}
}

// RevokeSigner signs everything again with the key that stays, runs again if the old key wrote meanwhile, takes
// the old key out of the lists, and reports what it wrote after the last check.
func TestRevokeSignerReplacesTheKeyAndReportsWhatItWroteInTheWindow(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	a, b := w.e2eeDoc(t), w.e2eeDoc(t)
	r := w.rotate(t)
	oldKey := w.signer.Public()

	// a holder of the old key writes frames straight into the store, signed by it, with no record to check
	oldWriter := New(w.inner, Static{To: w.ring.People.Owners, Keys: w.ring.People.Keys})
	oldWriter.Signer = w.signer
	passes, removed := 0, 0
	late := w.mainFile(t, b)
	o := RevokeSignerOptions{
		Progress: func(p RepairProgress) {
			// the old key keeps writing during the first pass: the document just moved gets a new frame signed by it
			if passes == 0 {
				passes = 1
				if err := oldWriter.Save(ctx, w.mainFile(t, p.Doc), []byte("written by the old key during the run")); err != nil {
					t.Error(err)
				}
			}
		},
		Remove: func(context.Context) error {
			removed++
			// and once more, right before it is removed: this one cannot be signed again in time
			if err := oldWriter.Save(ctx, late, []byte("the last word of the old key")); err != nil {
				t.Error(err)
			}
			r.sharing.Adapter = r.onlyNew()
			r.sharing.OwnerSigners = []ed25519.PublicKey{r.newKey.Public()}
			return nil
		},
	}
	rep, err := r.sharing.RevokeSigner(ctx, oldKey, o)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Removed || removed != 1 || rep.Passes < 2 {
		t.Errorf("removed %v (%d calls), passes %d: a write during the first pass needs a second", rep.Removed, removed, rep.Passes)
	}
	if len(rep.Late) != 1 || rep.Late[0] != b {
		t.Errorf("late: %v, want the document the old key wrote last: %s", rep.Late, b)
	}
	// document a is the new key's, record and files
	onlyNew := signerRotation{w: w, newKey: r.newKey, ad: r.sharing.Adapter, sharing: r.sharing}
	onlyNew.allBy(t, a, SignerFingerprint(r.newKey.Public()))
	// the old key's frame in b is refused from now on
	if _, err := r.sharing.Adapter.Load(ctx, late); !errors.Is(err, ErrForgedFrame) {
		t.Errorf("the old key's late frame loads: %v", err)
	}
}

func TestRevokeSignerRefusesWhenItCannotMoveEverythingOrWouldRevokeItself(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.e2eeDoc(t)
	r := w.rotate(t)
	oldKey := w.signer.Public()
	called := false
	remove := func(context.Context) error { called = true; return nil }

	if _, err := r.sharing.RevokeSigner(ctx, r.newKey.Public(), RevokeSignerOptions{Remove: remove}); !errors.Is(err, ErrRevokeCurrentSigner) {
		t.Errorf("revoking the signer in use: %v", err)
	}
	if _, err := r.sharing.RevokeSigner(ctx, oldKey, RevokeSignerOptions{}); !errors.Is(err, ErrRevokeNeedsSigner) {
		t.Errorf("no Remove: %v", err)
	}
	// a document that cannot be opened cannot be signed again: the key is kept
	files := w.files(t, w.e2eeDoc(t))
	if err := w.inner.Save(ctx, files[0], []byte("damaged")); err != nil {
		t.Fatal(err)
	}
	rep, err := r.sharing.RevokeSigner(ctx, oldKey, RevokeSignerOptions{Remove: remove})
	if !errors.Is(err, ErrRevokeCannotResign) || called || rep.Removed || len(rep.Skipped) == 0 {
		t.Errorf("a damaged document: err %v removed-called %v report %+v", err, called, rep)
	}
	// a key that keeps writing stops it after MaxPasses
	w2 := newWorld(t)
	doc := w2.e2eeDoc(t)
	r2 := w2.rotate(t)
	keepWriting := New(w2.inner, Static{To: w2.ring.People.Owners, Keys: w2.ring.People.Keys})
	keepWriting.Signer = w2.signer
	rep, err = r2.sharing.RevokeSigner(ctx, w2.signer.Public(), RevokeSignerOptions{
		MaxPasses: 2,
		Progress: func(RepairProgress) {
			_ = keepWriting.Save(ctx, w2.mainFile(t, doc), []byte(fmt.Sprint("again ", rep.Passes)))
		},
		Remove: remove,
	})
	if !errors.Is(err, ErrRevokeStillWriting) || rep.Removed {
		t.Errorf("a key that keeps writing: %v %+v", err, rep)
	}
}

// A run whose Remove fails is finished by running it again: the signing is found done, and the key goes.
func TestRevokeSignerIsRestartable(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := w.e2eeDoc(t)
	r := w.rotate(t)
	failing := true
	remove := func(context.Context) error {
		if failing {
			return errors.New("the config could not be written")
		}
		r.sharing.Adapter = r.onlyNew()
		r.sharing.OwnerSigners = []ed25519.PublicKey{r.newKey.Public()}
		return nil
	}
	rep, err := r.sharing.RevokeSigner(ctx, w.signer.Public(), RevokeSignerOptions{Remove: remove})
	if err == nil || rep.Removed || rep.Resigned != 1 {
		t.Fatalf("first run: %v %+v", err, rep)
	}
	failing = false
	rep, err = r.sharing.RevokeSigner(ctx, w.signer.Public(), RevokeSignerOptions{Remove: remove})
	if err != nil || !rep.Removed || rep.Resigned != 0 || len(rep.Late) != 0 {
		t.Fatalf("second run: %v %+v", err, rep)
	}
	signerRotation{w: w, newKey: r.newKey, ad: r.sharing.Adapter, sharing: r.sharing}.allBy(t, doc, SignerFingerprint(r.newKey.Public()))
}
