package encrypt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

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
	m.junk = "0b1c2d3e-0000-4000-8000-00000000000a"
	if err := w.inner.Save(ctx, m.junk+"~deadbeef0001.open", ageHeaderJunk(7)); err != nil {
		t.Fatal(err)
	}
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
	if !w.binding(t, w.mainFile(t, m.v1e2ee)).Signed {
		t.Error("the record of a repaired document does not say signed")
	}
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
	r1, err := w.sharing.RepairAll(ctx, RepairAllOptions{Limit: 1})
	if err != nil || r1.Repaired != 1 || r1.Remaining == 0 {
		t.Fatalf("%v %+v", err, r1)
	}
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

type listCounter struct {
	*flaky
	lists   atomic.Int64
	saveOn  string
	saveErr error
	onSave  func()
}

func (c *listCounter) List(ctx context.Context) ([]docstore.Meta, error) {
	c.lists.Add(1)
	return c.flaky.List(ctx)
}

func (c *listCounter) SaveIf(ctx context.Context, id string, b []byte, prev string) error {
	if c.saveOn != "" && strings.Contains(id, c.saveOn) {
		if c.onSave != nil {
			c.onSave()
		}
		return c.saveErr
	}
	return c.flaky.SaveIf(ctx, id, b, prev)
}

func (w *world) over(inner docstore.Adapter, mod func(*Adapter)) *Sharing {
	ad := New(inner, w.ring)
	ad.Signer = w.signer
	if mod != nil {
		mod(ad)
	}
	return &Sharing{Store: w.inner, Adapter: ad, Signer: w.signer}
}

func (w *world) legacyItem(t *testing.T, id string, data []byte) {
	t.Helper()
	rs, err := w.ring.Recipients(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.inner.Save(context.Background(), id, sealRaw(t, rs, frame(id, Binding{}, data, nil))); err != nil {
		t.Fatal(err)
	}
}

func TestRepairAllListsTheStoreOncePerRun(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	for i := 0; i < 12; i++ {
		w.e2eeDoc(t) // signed already
	}
	old := []string{w.history(t), w.history(t)}
	for _, id := range old {
		w.legacyV2(t, id)
	}
	cnt := &listCounter{flaky: w.inner}
	sh := w.over(cnt, nil)
	rep, err := sh.RepairAll(ctx, RepairAllOptions{})
	if err != nil || rep.Repaired != 2 || rep.Signed != 12 {
		t.Fatalf("%v %+v", err, rep)
	}
	if n := cnt.lists.Load(); n > 1+3*int64(rep.Repaired) {
		t.Errorf("%d List calls for 14 documents, 2 of them repaired", n)
	}
	cnt.lists.Store(0)
	if rep, err := sh.RepairAll(ctx, RepairAllOptions{}); err != nil || rep.Signed != 14 {
		t.Fatalf("%v %+v", err, rep)
	}
	if n := cnt.lists.Load(); n != 1 {
		t.Errorf("a run that repairs nothing listed the store %d times", n)
	}
}

func TestRepairAllCoversItemsThatAreNotDocuments(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	items := []string{"expiry:abc", "collab:def", "passkey/ghi", "public/jkl", docstore.SessionPrefix + "491234/abc", docstore.LoginStatePrefix + "29000000/def"}
	for _, id := range items {
		w.legacyItem(t, id, []byte("data of "+id))
	}
	signedItem := "expiry:signed"
	if err := w.ad.Save(ctx, signedItem, []byte("x")); err != nil {
		t.Fatal(err)
	}
	junk := "collab:junk"
	if err := w.inner.Save(ctx, junk, ageHeaderJunk(3)); err != nil {
		t.Fatal(err)
	}
	doc := w.history(t)
	docFiles := len(w.files(t, doc))
	w.legacyV2(t, doc)

	before := w.snapshot(t)
	nosign := &Sharing{Store: w.inner, Adapter: New(w.inner, w.ring), Signer: w.signer}
	rep, err := nosign.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sk := skippedBy(rep)
	for _, id := range items {
		if sk[id] != SkipNoSigner {
			t.Errorf("%s: %q", id, sk[id])
		}
	}
	if sk[doc] != SkipNoSigner || sk[junk] != SkipUnreadable || rep.Left() != len(items)+2 {
		t.Errorf("%v left %d", sk, rep.Left())
	}
	sameStore(t, "no signer", before, w.snapshot(t))

	dry, err := w.sharing.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || len(dry.Todo) != len(items)+1 {
		t.Fatalf("%v %+v", err, dry)
	}
	sameStore(t, "dry run", before, w.snapshot(t))

	rep, err = w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Repaired != len(items)+1 || rep.Signed != 1 || len(rep.Skipped) != 1 || rep.Total != len(items)+3 || rep.Left() != 1 {
		t.Fatalf("%+v", rep)
	}
	if rep.Files != len(items)+docFiles {
		t.Errorf("Files = %d, want %d", rep.Files, len(items)+docFiles)
	}
	for _, id := range append(items, signedItem) {
		if v := w.frameVersion(t, id); v != 3 {
			t.Errorf("%s is v%d", id, v)
		}
		if _, err := w.ad.Load(ctx, id); err != nil {
			t.Errorf("%s does not load: %v", id, err)
		}
	}
	if d, err := w.ad.Load(ctx, "public/jkl"); err != nil || string(d.Data) != "data of public/jkl" {
		t.Errorf("content changed: %q %v", d.Data, err)
	}
	settled := w.snapshot(t)
	if rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{}); err != nil || rep.Repaired != 0 || rep.Signed != len(items)+2 {
		t.Fatalf("%v %+v", err, rep)
	}
	sameStore(t, "second run", settled, w.snapshot(t))
}

func TestRepairAllLeavesSkippedIdsAlone(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := w.history(t)
	w.legacyV2(t, doc)
	plain := map[string][]byte{"open/readme": []byte("in the clear"), "catalog:x": []byte("{}"), "acl:y": []byte("{}"), "keys:zzz": []byte("{}")}
	for id, d := range plain {
		if err := w.inner.Save(ctx, id, d); err != nil {
			t.Fatal(err)
		}
	}
	before := w.snapshot(t)
	sh := w.over(w.inner, func(a *Adapter) {
		a.Skip = func(id string) bool { return DefaultSkip(id) || strings.HasPrefix(id, "open/") }
	})
	rep, err := sh.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 1 || rep.Repaired != 1 || len(rep.Skipped) != 0 || rep.Left() != 0 {
		t.Fatalf("%+v", rep)
	}
	after := w.snapshot(t)
	for id := range plain {
		if after[id] != before[id] {
			t.Errorf("%s was written", id)
		}
	}
	rep, err = w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || skippedBy(rep)["open/readme"] != SkipPlaintext || len(rep.Skipped) != 1 {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestRepairAllReportsPlaintext(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := "0b1c2d3e-0000-4000-8000-0000000000aa"
	f := doc + "~deadbeef0003.open"
	if err := w.inner.Save(ctx, f, []byte("never encrypted")); err != nil {
		t.Fatal(err)
	}
	before := w.snapshot(t)
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if skippedBy(rep)[doc] != SkipPlaintext || rep.Left() != 1 || rep.Skipped[0].Files[0] != f {
		t.Fatalf("%+v", rep)
	}
	sameStore(t, "plaintext", before, w.snapshot(t))
}

func TestRepairAllStopsWhenTheContextEndsInsideRepair(t *testing.T) {
	w := newWorld(t)
	doc := w.history(t)
	w.legacyV2(t, doc)
	ctx, cancel := context.WithCancel(context.Background())
	cnt := &listCounter{flaky: w.inner, saveOn: doc, saveErr: context.Canceled, onSave: cancel}
	rep, err := w.over(cnt, nil).RepairAll(ctx, RepairAllOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if len(rep.Skipped) != 0 || rep.Repaired != 0 || rep.Remaining != 1 || rep.Left() != 1 {
		t.Fatalf("%+v", rep)
	}
}

func TestRepairAllReportsConflictAndDowngrade(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	busy := w.history(t)
	w.legacyV2(t, busy)
	gone := w.e2eeDoc(t)
	w.legacyV2(t, gone)
	if err := w.inner.Delete(ctx, KeysPrefix+gone); err != nil { // the record is deleted
		t.Fatal(err)
	}
	cnt := &listCounter{flaky: w.inner, saveOn: busy, saveErr: docstore.ErrConflict} // a writer that always wins
	before := w.snapshot(t)
	rep, err := w.over(cnt, nil).RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sk := skippedBy(rep)
	if sk[busy] != SkipConflict || sk[gone] != SkipDowngrade || rep.Repaired != 0 || rep.Left() != 2 {
		t.Fatalf("%v %+v", sk, rep)
	}
	sameStore(t, "conflict and downgrade", before, w.snapshot(t))
}

func TestRepairAllDryRunWithLimit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	for i := 0; i < 3; i++ {
		w.legacyV2(t, w.history(t))
	}
	before := w.snapshot(t)
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{DryRun: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Todo) != 2 || rep.Remaining != 1 || rep.Repaired != 0 || rep.Left() != 3 {
		t.Fatalf("%+v", rep)
	}
	sameStore(t, "dry run with limit", before, w.snapshot(t))
}

func TestRepairAllRepairsAFolderSecretOnlyDocument(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	folder := "0b1c2d3e-0000-4000-8000-0000000000fd"
	id := docstore.FolderSecretPrefix + folder
	w.legacyItem(t, id, []byte("Secret folder name"))
	if v := w.frameVersion(t, id); v != 2 {
		t.Fatalf("v%d", v)
	}
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || rep.Total != 1 || rep.Repaired != 1 || rep.Files != 1 || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	if v := w.frameVersion(t, id); v != 3 {
		t.Errorf("v%d", v)
	}
	if d, err := w.ad.Load(ctx, id); err != nil || string(d.Data) != "Secret folder name" {
		t.Errorf("%q %v", d.Data, err)
	}
}

func TestRepairAllCreatesTheRecordOfADocumentWithoutOne(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := w.history(t)
	w.legacyV2(t, doc)
	if _, digest, err := loadRecord(ctx, w.inner, doc, w.ring.People.OwnerSigners, nil); err != nil || digest != "" {
		t.Fatalf("the document has a record: %q %v", digest, err)
	}
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || rep.Repaired != 1 || rep.Left() != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	rec, digest, err := loadRecord(ctx, w.inner, doc, w.ring.People.OwnerSigners, nil)
	if err != nil || digest == "" || !rec.Signed {
		t.Fatalf("record %+v %q %v", rec, digest, err)
	}
	for _, f := range w.files(t, doc) {
		if v := w.frameVersion(t, f); v != 3 {
			t.Errorf("%s v%d", f, v)
		}
	}
}

type delOnList struct {
	*flaky
	id    string
	n     int
	calls atomic.Int64
}

func (d *delOnList) List(ctx context.Context) ([]docstore.Meta, error) {
	if int(d.calls.Add(1)) == d.n {
		if err := d.Delete(ctx, d.id); err != nil {
			return nil, err
		}
	}
	return d.flaky.List(ctx)
}

func TestRepairAllIgnoresAnItemDeletedAfterTheScan(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.legacyItem(t, "expiry:gone", []byte("x"))
	w.legacyItem(t, "expiry:kept", []byte("y"))
	sh := w.over(&delOnList{flaky: w.inner, id: "expiry:gone", n: 2}, nil)
	var outcomes []string
	rep, err := sh.RepairAll(ctx, RepairAllOptions{Progress: func(p RepairProgress) { outcomes = append(outcomes, p.Doc+"="+p.Outcome) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 0 || rep.Left() != 0 || rep.Repaired != 1 || rep.Files != 1 || rep.Resigned != 0 {
		t.Fatalf("%+v (%v)", rep, outcomes)
	}
	if v := w.frameVersion(t, "expiry:kept"); v != 3 {
		t.Errorf("expiry:kept is v%d", v)
	}
	if _, err := w.inner.Load(ctx, "expiry:gone"); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("expiry:gone: %v", err)
	}
}

func TestRepairAllWithConcurrentSaveOfAnItem(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	const item = "expiry:race"
	w.legacyItem(t, item, []byte("old"))
	others := []string{"expiry:a", "expiry:b", "collab:c"}
	for _, o := range others {
		w.legacyItem(t, o, []byte("o"))
	}
	const writes = 40
	var wg sync.WaitGroup
	werr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			if err := w.ad.Save(ctx, item, []byte(fmt.Sprintf("w-%d", i))); err != nil {
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
	if err != nil || len(rep.Skipped) != 0 {
		t.Fatalf("%v %+v", err, rep)
	}
	if rep2, err := w.sharing.RepairAll(ctx, RepairAllOptions{}); err != nil || rep2.Repaired != 0 || rep2.Left() != 0 {
		t.Fatalf("%v %+v", err, rep2)
	}
	d, err := w.ad.Load(ctx, item)
	if err != nil || string(d.Data) != fmt.Sprintf("w-%d", writes-1) {
		t.Fatalf("the last concurrent Save was lost: %q %v", d.Data, err)
	}
	for _, id := range append(others, item) {
		if v := w.frameVersion(t, id); v != 3 {
			t.Errorf("%s is v%d", id, v)
		}
	}
}

type errRing struct {
	Keyring
	id  string
	err error
}

func (r errRing) Recipients(ctx context.Context, id string) ([]age.Recipient, error) {
	if id == r.id {
		return nil, r.err
	}
	return r.Keyring.Recipients(ctx, id)
}

func TestRepairAllKeyringNotFoundIsNotGone(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.legacyItem(t, "expiry:live", []byte("x"))
	kerr := fmt.Errorf("keyring has no record: %w", docstore.ErrNotFound)
	sh := w.over(w.inner, func(a *Adapter) { a.Ring = errRing{Keyring: w.ring, id: "expiry:live", err: kerr} })
	var outcomes []string
	rep, err := sh.RepairAll(ctx, RepairAllOptions{Progress: func(p RepairProgress) { outcomes = append(outcomes, p.Doc+"="+p.Outcome) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Doc != "expiry:live" || rep.Skipped[0].Reason != SkipError ||
		errors.Is(rep.Skipped[0].Err, docstore.ErrNotFound) || !strings.Contains(rep.Skipped[0].Err.Error(), kerr.Error()) || rep.Left() != 1 || rep.Repaired != 0 {
		t.Fatalf("%+v (%v)", rep, outcomes)
	}
	if len(outcomes) != 1 || outcomes[0] != "expiry:live=error" {
		t.Errorf("outcomes %v", outcomes)
	}
	if v := w.frameVersion(t, "expiry:live"); v == 3 {
		t.Error("the item was rewritten")
	}
}

type notFoundFloor struct{}

func (notFoundFloor) Get(context.Context, string) (uint64, error) {
	return 0, fmt.Errorf("floor: %w", docstore.ErrNotFound)
}
func (notFoundFloor) Raise(context.Context, string, uint64) error { return nil }

type hideKeys struct{ docstore.Adapter }

func (h hideKeys) Load(ctx context.Context, id string) (docstore.Document, error) {
	if strings.HasPrefix(id, KeysPrefix) {
		return docstore.Document{}, docstore.ErrNotFound
	}
	return h.Adapter.Load(ctx, id)
}

func TestRepairAllStoreKeyringNotFoundIsNotGone(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := w.history(t)
	w.legacyV2(t, doc)
	ring := w.ring
	ring.People.Store = hideKeys{w.inner}
	ring.People.SeqFloor = notFoundFloor{}
	sh := w.over(w.inner, func(a *Adapter) { a.Ring = ring })
	var outcomes []string
	rep, err := sh.RepairAll(ctx, RepairAllOptions{Progress: func(p RepairProgress) { outcomes = append(outcomes, p.Doc+"="+p.Outcome) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Doc != doc || rep.Skipped[0].Reason != SkipError ||
		errors.Is(rep.Skipped[0].Err, docstore.ErrNotFound) || !strings.Contains(rep.Skipped[0].Err.Error(), "floor") || rep.Left() != 1 {
		t.Fatalf("%+v (%v)", rep, outcomes)
	}
	if len(outcomes) != 1 || outcomes[0] != doc+"=error" {
		t.Errorf("outcomes %v", outcomes)
	}
}

type hideOnList struct {
	*flaky
	id    string
	calls atomic.Int64
}

func (h *hideOnList) List(ctx context.Context) ([]docstore.Meta, error) {
	ms, err := h.flaky.List(ctx)
	if err != nil || h.calls.Add(1) < 2 {
		return ms, err
	}
	out := ms[:0:0]
	for _, m := range ms {
		if m.ID != h.id {
			out = append(out, m)
		}
	}
	return out, nil
}

func TestRepairAllNotFoundWithTheFilesStoredIsNotGone(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.legacyItem(t, "expiry:live", []byte("x"))
	sh := w.over(&hideOnList{flaky: w.inner, id: "expiry:live"}, nil)
	var outcomes []string
	rep, err := sh.RepairAll(ctx, RepairAllOptions{Progress: func(p RepairProgress) { outcomes = append(outcomes, p.Doc+"="+p.Outcome) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Reason != SkipError || rep.Left() != 1 || len(outcomes) != 1 || outcomes[0] != "expiry:live=error" {
		t.Fatalf("%+v (%v)", rep, outcomes)
	}
	if !strings.Contains(rep.Skipped[0].Err.Error(), "some files of this item are gone, rerun to settle") {
		t.Errorf("message %q", rep.Skipped[0].Err)
	}
}

type idRing struct {
	Keyring
	id  string
	err error
}

func (r idRing) Identities(ctx context.Context, id string) ([]age.Identity, error) {
	if id == r.id {
		return nil, r.err
	}
	return r.Keyring.Identities(ctx, id)
}

func TestKeyringIdentitiesNotFoundIsNotGone(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.legacyItem(t, "expiry:live", []byte("x"))
	kerr := fmt.Errorf("keyring has no identities: %w", docstore.ErrNotFound)
	sh := w.over(w.inner, func(a *Adapter) { a.Ring = idRing{Keyring: w.ring, id: "expiry:live", err: kerr} })

	n, err := sh.Adapter.RekeyAll(ctx)
	if err == nil || errors.Is(err, docstore.ErrNotFound) || n != 0 || !strings.Contains(err.Error(), kerr.Error()) {
		t.Fatalf("RekeyAll: %d %v", n, err)
	}

	rep, err := sh.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Doc != "expiry:live" || rep.Skipped[0].Reason != SkipUnreadable ||
		errors.Is(rep.Skipped[0].Err, docstore.ErrNotFound) || !strings.Contains(rep.Skipped[0].Err.Error(), kerr.Error()) || rep.Left() != 1 || rep.Repaired != 0 {
		t.Fatalf("RepairAll: %+v", rep)
	}
	if v := w.frameVersion(t, "expiry:live"); v == 3 {
		t.Error("the item was rewritten")
	}
}
