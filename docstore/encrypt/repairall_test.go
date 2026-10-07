package encrypt

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"filippo.io/age"
)

// Tests of Sharing.RepairAll (docs/roadmap.md, backlog item 3).

// legacyV2 rewrites every stored file of the document with the unsigned v2 frame (level and seq,
// no signature) and the record as it was before frames were signed.
func (w *world) legacyV2(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	w.rewriteRecord(t, id, func(r *sharedRecord) { r.Signed = false })
	for _, f := range w.files(t, id) {
		d, err := w.inner.Load(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		plain, fi, _, err := w.ad.open(ctx, f, d.Data)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := w.ring.Recipients(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		fr := frame(f, Binding{Level: fi.b.Level, Seq: fi.b.Seq, Hash: fi.b.Hash}, plain, nil)
		if err := w.inner.Save(ctx, f, sealRaw(t, rs, fr)); err != nil {
			t.Fatal(err)
		}
		if v := w.frameVersion(t, f); v != 2 {
			t.Fatalf("legacyV2 wrote v%d", v)
		}
	}
}

// mixedStore has a document in each state RepairAll has to tell apart.
type mixedStore struct {
	v1, v1e2ee, v2, v3, junk, foreign string
}

func (w *world) mixed(t *testing.T) mixedStore {
	t.Helper()
	ctx := context.Background()
	var m mixedStore
	m.v1 = w.history(t)
	w.legacyV1(t, m.v1)
	m.v1e2ee = w.e2eeDoc(t)
	w.legacyV1(t, m.v1e2ee)
	m.v2 = w.history(t)
	w.legacyV2(t, m.v2)
	m.v3 = w.e2eeDoc(t)
	// An unreadable file: the age header and then noise, under a document of its own.
	m.junk = "0b1c2d3e-0000-4000-8000-00000000000a"
	if err := w.inner.Save(ctx, m.junk+"~deadbeef0001.open", ageHeaderJunk(7)); err != nil {
		t.Fatal(err)
	}
	// A confidential document of someone else's: encrypted to keys the keyring does not hold.
	m.foreign = "0b1c2d3e-0000-4000-8000-00000000000b"
	stranger := newPerson(t, "stranger")
	ff := m.foreign + "~deadbeef0002.open"
	if err := w.inner.Save(ctx, ff, sealRaw(t, []age.Recipient{stranger.r.Key}, frame(ff, Binding{Level: ProtectionE2EE, Seq: 1}, []byte("not yours"), nil))); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{m.v1, m.v1e2ee, m.v2} {
		for _, f := range w.files(t, id) {
			if v := w.frameVersion(t, f); v >= 3 {
				t.Fatalf("%s is v%d before RepairAll", f, v)
			}
		}
	}
	return m
}

func skippedBy(rep RepairAllReport) map[string]RepairSkipReason {
	out := map[string]RepairSkipReason{}
	for _, s := range rep.Skipped {
		out[s.Doc] = s.Reason
	}
	return out
}

func TestRepairAllMixedStoreIsIdempotent(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.mixed(t)
	before := w.snapshot(t)
	var seen []RepairProgress
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{Progress: func(p RepairProgress) { seen = append(seen, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 6 || rep.Signed != 1 || rep.Repaired != 3 || len(rep.Skipped) != 2 || rep.Remaining != 0 || rep.Files == 0 {
		t.Fatalf("report %+v", rep)
	}
	if rep.Total != rep.Signed+rep.Repaired+len(rep.Skipped) {
		t.Errorf("report does not add up: %+v", rep)
	}
	sk := skippedBy(rep)
	if sk[m.junk] != SkipUnreadable || sk[m.foreign] != SkipNoKey {
		t.Errorf("skipped %v", sk)
	}
	if len(seen) != 6 || seen[5].Done != 6 || seen[5].Total != 6 {
		t.Errorf("progress %v", seen)
	}
	for _, id := range []string{m.v1, m.v1e2ee, m.v2, m.v3} {
		for _, f := range w.files(t, id) {
			if v := w.frameVersion(t, f); v != 3 {
				t.Errorf("%s is v%d after RepairAll", f, v)
			}
			if _, err := w.ad.Load(ctx, f); err != nil {
				t.Errorf("%s does not load: %v", f, err)
			}
		}
	}
	// The record now says every file is signed, so an unsigned frame is forged from here on.
	if !w.binding(t, w.mainFile(t, m.v1e2ee)).Signed {
		t.Error("the record of a repaired document does not say signed")
	}
	// Skipped documents are byte for byte what they were; nothing was deleted anywhere.
	after := w.snapshot(t)
	for _, doc := range []string{m.junk, m.foreign} {
		for id, d := range before {
			if DocID(id) == doc && after[id] != d {
				t.Errorf("skipped %s was changed", id)
			}
		}
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			t.Errorf("%s was deleted", id)
		}
	}
	// A second run repairs nothing and writes nothing.
	settled := w.snapshot(t)
	rep2, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Repaired != 0 || rep2.Signed != 4 || len(rep2.Skipped) != 2 || rep2.Files != 0 {
		t.Errorf("second run %+v", rep2)
	}
	sameStore(t, "second run", settled, w.snapshot(t))
}

func TestRepairAllWithoutASignerReportsAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.mixed(t)
	before := w.snapshot(t)
	nosign := New(w.inner, w.ring) // no Signer
	sh := &Sharing{Store: w.inner, Adapter: nosign, Signer: w.signer}
	rep, err := sh.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Repaired != 0 || rep.Signed != 1 {
		t.Fatalf("report %+v", rep)
	}
	sk := skippedBy(rep)
	for _, id := range []string{m.v1, m.v1e2ee, m.v2} {
		if sk[id] != SkipNoSigner {
			t.Errorf("%s: %q", id, sk[id])
		}
	}
	if sk[m.junk] != SkipUnreadable || sk[m.foreign] != SkipNoKey {
		t.Errorf("skipped %v", sk)
	}
	sameStore(t, "no signer", before, w.snapshot(t))
}

func TestRepairAllDryRunLimitAndResume(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	m := w.mixed(t)
	before := w.snapshot(t)
	dry, err := w.sharing.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || len(dry.Todo) != 3 || dry.Repaired != 0 {
		t.Fatalf("%v %+v", err, dry)
	}
	sameStore(t, "dry run", before, w.snapshot(t))
	// Limit 1 repairs one document and says how many it did not look at.
	r1, err := w.sharing.RepairAll(ctx, RepairAllOptions{Limit: 1})
	if err != nil || r1.Repaired != 1 || r1.Remaining == 0 {
		t.Fatalf("%v %+v", err, r1)
	}
	// Running again carries on and finishes.
	r2, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || r2.Repaired != 2 || r2.Remaining != 0 || len(r2.Skipped) != 2 {
		t.Fatalf("%v %+v", err, r2)
	}
	for _, id := range []string{m.v1, m.v1e2ee, m.v2} {
		for _, f := range w.files(t, id) {
			if v := w.frameVersion(t, f); v != 3 {
				t.Errorf("%s v%d", f, v)
			}
		}
	}
}

func TestRepairAllStopsOnContextAndResumes(t *testing.T) {
	w := newWorld(t)
	w.mixed(t)
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{Progress: func(RepairProgress) {
		if n++; n == 2 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if rep.Remaining != rep.Total-2 {
		t.Errorf("report %+v", rep)
	}
	rep2, err := w.sharing.RepairAll(context.Background(), RepairAllOptions{})
	if err != nil || rep2.Remaining != 0 || rep.Repaired+rep2.Repaired != 3 {
		t.Fatalf("%v %+v %+v", err, rep, rep2)
	}
}

// A record that already says every file is signed turns an unsigned file into a forgery: RepairAll
// reports it and leaves the owner's explicit AcceptUnsigned to settle it, never vouching itself.
func TestRepairAllDoesNotAcceptUnsignedFilesOfASignedRecord(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	f := w.mainFile(t, id)
	rb := w.binding(t, f)
	planted := sealRaw(t, w.publicRecipients(), frame(f, Binding{Level: ProtectionE2EE, Seq: rb.Seq, Hash: rb.Hash}, []byte("planted"), nil))
	if err := w.inner.Save(ctx, f, planted); err != nil {
		t.Fatal(err)
	}
	before := w.snapshot(t)
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Repaired != 0 || skippedBy(rep)[id] != SkipForged {
		t.Fatalf("%+v", rep)
	}
	sameStore(t, "forged", before, w.snapshot(t))
}

// A Save by another writer while RepairAll runs is not lost, and every file ends signed.
func TestRepairAllWithConcurrentSaves(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	w.legacyV1(t, id)
	others := []string{w.history(t), w.history(t), w.history(t)}
	for _, o := range others {
		w.legacyV2(t, o)
	}
	f := w.mainFile(t, id)
	const writes = 40
	var wg sync.WaitGroup
	werr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			if err := w.ad.Save(ctx, f, []byte(fmt.Sprintf("w-%d", i))); err != nil {
				werr <- err
				return
			}
		}
	}()
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	wg.Wait()
	select {
	case err := <-werr:
		t.Fatalf("a concurrent Save failed: %v", err)
	default:
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 0 {
		t.Fatalf("%+v", rep.Skipped)
	}
	// Another run settles whatever a Save that raced the scan left (a Save through the signing
	// adapter writes v3, so there is nothing), and the last write is the one that is stored.
	if rep2, err := w.sharing.RepairAll(ctx, RepairAllOptions{}); err != nil || rep2.Repaired != 0 {
		t.Fatalf("%v %+v", err, rep2)
	}
	d, err := w.ad.Load(ctx, f)
	if err != nil || string(d.Data) != fmt.Sprintf("w-%d", writes-1) {
		t.Fatalf("the last concurrent Save was lost: %q %v", d.Data, err)
	}
	for _, doc := range append(others, id) {
		for _, g := range w.files(t, doc) {
			if v := w.frameVersion(t, g); v != 3 {
				t.Errorf("%s is v%d", g, v)
			}
		}
	}
}
