package encrypt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

type mapKV struct {
	mu   sync.Mutex
	m    map[string]string
	fail error
	// failSet fails only writes (a full quota: reads still work).
	failSet error
}

func (k *mapKV) Get(key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail != nil {
		return "", false, k.fail
	}
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *mapKV) Set(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.fail != nil {
		return k.fail
	}
	if k.failSet != nil {
		return k.failSet
	}
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = value
	return nil
}

func TestFileSeqFloorSurvivesARestartAndOnlyRises(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "floor.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &FileSeqFloor{Path: path}
	if v, err := f.Get(ctx, "a"); err != nil || v != 0 {
		t.Fatalf("a missing file is an empty floor: %d %v", v, err)
	}
	for _, n := range []uint64{5, 3, 9} {
		if err := f.Raise(ctx, "a", n); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Raise(ctx, "b", 2)
	g := &FileSeqFloor{Path: path} // a new process
	if v, _ := g.Get(ctx, "a"); v != 9 {
		t.Errorf("a: %d", v)
	}
	if v, _ := g.Get(ctx, "b"); v != 2 {
		t.Errorf("b: %d", v)
	}
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("the floor is private: %v %v", st, err)
		}
	}
	if ents, _ := os.ReadDir(filepath.Dir(path)); len(ents) != 1 {
		t.Errorf("no temporary files are left: %v", ents)
	}
	// Failures are reported, not swallowed.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Get(ctx, "a"); err == nil {
		t.Error("a damaged floor file must be an error")
	}
	if err := g.Raise(ctx, "a", 10); err == nil {
		t.Error("and must not be overwritten silently")
	}
	bad := &FileSeqFloor{Path: filepath.Join(path, "nope", "floor.json")}
	if err := bad.Raise(ctx, "a", 1); err == nil {
		t.Error("an unwritable floor is an error")
	}
	c := &FileSeqFloor{Path: filepath.Join(t.TempDir(), "c.json")}
	var wg sync.WaitGroup
	for i := uint64(1); i <= 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Raise(ctx, "a", i); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if v, _ := c.Get(ctx, "a"); v != 20 {
		t.Errorf("%d", v)
	}
}

func TestStorageSeqFloorKeepsAFloorInTheKVAndDegradesToMemory(t *testing.T) {
	ctx := context.Background()
	kv := &mapKV{}
	f := &StorageSeqFloor{Prefix: "floor:", Store: kv}
	_ = f.Raise(ctx, "a", 4)
	_ = f.Raise(ctx, "a", 2)
	if kv.m["floor:a"] != "4" {
		t.Errorf("%v", kv.m)
	}
	g := &StorageSeqFloor{Prefix: "floor:", Store: kv} // a reloaded page
	if v, _ := g.Get(ctx, "a"); v != 4 {
		t.Errorf("%d", v)
	}
	kv.fail = errors.New("SecurityError")
	_ = g.Raise(ctx, "a", 7)
	if err := g.Raise(ctx, "a", 8); !errors.Is(err, ErrFloorDegraded) {
		t.Errorf("a failing storage must be reported by Raise: %v", err)
	}
	if v, err := g.Get(ctx, "a"); !errors.Is(err, ErrFloorDegraded) || v != 8 {
		t.Errorf("Get returns the memory's value and the failure: %d %v", v, err)
	}
	if g.Err() == nil {
		t.Error("Err should report the failing storage")
	}
	kv.fail = nil
	kv.m["floor:z"] = "banana"
	if v, err := g.Get(ctx, "z"); !errors.Is(err, ErrFloorDegraded) || v != 0 || g.Err() == nil {
		t.Errorf("%d %v %v", v, err, g.Err())
	}
}

func TestFloorsRefuseAReplayedAndADeletedRecordThroughTheRealStack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "floor.json")
	w := newWorldFloor(t, &FileSeqFloor{Path: path})
	id, old := w.confidentialWithOldRecord(t)
	w2 := newWorldFloor(t, &FileSeqFloor{Path: path})
	w2.inner = w.inner
	w2.ring.People.Store = w.inner
	w2.ad = New(w.inner, w2.ring)
	w2.sharing = &Sharing{Store: w.inner, Adapter: w2.ad, Signer: w.signer, SeqFloor: w2.ring.People.SeqFloor}
	w.replay(t, id, old)
	if _, _, err := w2.ring.Bind(ctx, id+"~aaaaaaaaaaaa.open"); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("replay: %v", err)
	}
	if err := w.inner.Delete(ctx, KeysPrefix+id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w2.ring.Bind(ctx, id+"~aaaaaaaaaaaa.open"); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("deleted: %v", err)
	}
}

func TestFileSeqFloorReadsOnceWritesInBatchesAndSyncsTheDirectory(t *testing.T) {
	ctx := context.Background()
	var synced []string
	old := syncDir
	syncDir = func(dir string) error { synced = append(synced, dir); return nil }
	defer func() { syncDir = old }()

	path := filepath.Join(t.TempDir(), "floor.json")
	f := &FileSeqFloor{Path: path}
	for i := 0; i < 200; i++ {
		if _, err := f.Get(ctx, "a"); err != nil {
			t.Fatal(err)
		}
	}
	if f.reads != 0 {
		t.Errorf("a missing file is read 0 times, got %d", f.reads)
	}
	seqs := map[string]uint64{}
	for i := 0; i < 300; i++ {
		seqs[fmt.Sprintf("doc-%03d", i)] = uint64(i + 1)
	}
	if err := f.RaiseMany(ctx, seqs); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Errorf("RaiseMany of 300 documents is one write, got %d", f.writes)
	}
	if len(synced) != 1 || synced[0] != filepath.Dir(path) {
		t.Errorf("the directory is synced after the rename: %v", synced)
	}
	for i := 0; i < 300; i++ {
		if v, _ := f.Get(ctx, fmt.Sprintf("doc-%03d", i)); v != uint64(i+1) {
			t.Fatalf("doc-%03d: %d", i, v)
		}
		_ = f.Raise(ctx, fmt.Sprintf("doc-%03d", i), uint64(i)) // lower or equal: nothing
	}
	if f.reads != 0 || f.writes != 1 {
		t.Errorf("a floor that wrote the file does not read it again, and a raise that changes nothing writes nothing: %d reads, %d writes", f.reads, f.writes)
	}
	g := &FileSeqFloor{Path: path}
	for i := 0; i < 300; i++ {
		_, _ = g.Get(ctx, fmt.Sprintf("doc-%03d", i))
	}
	if g.reads != 1 {
		t.Errorf("one read, got %d", g.reads)
	}
	h := &FileSeqFloor{Path: path}
	_ = h.Raise(ctx, "from-h", 9)
	if v, _ := g.Get(ctx, "from-h"); v != 9 {
		t.Errorf("the change on disk was not noticed: %d", v)
	}
	if g.reads != 2 {
		t.Errorf("%d", g.reads)
	}
}

func TestDamagedFloorFileRunbookVerifyIndexRebuildsIt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "floor.json")
	w := newWorldFloor(t, &FileSeqFloor{Path: path})
	ids := []string{w.e2eeDoc(t), w.e2eeDoc(t)}
	if err := os.WriteFile(path, []byte("{damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := &FileSeqFloor{Path: path}
	if _, err := fresh.Get(ctx, ids[0]); err == nil || !strings.Contains(err.Error(), "VerifyIndex") {
		t.Fatalf("the error should name the way out: %v", err)
	}
	if _, err := w.sharing.VerifyIndex(ctx); err == nil {
		t.Error("with the damaged file VerifyIndex cannot raise it, and says so")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w2 := newWorldFloor(t, &FileSeqFloor{Path: path})
	w2.inner = w.inner
	w2.ring.People.Store = w.inner
	w2.sharing = &Sharing{Store: w.inner, Adapter: New(w.inner, w2.ring), Signer: w.signer, SeqFloor: w2.ring.People.SeqFloor}
	if findings, err := w2.sharing.VerifyIndex(ctx); err != nil || len(findings) != 0 {
		t.Fatalf("%v %v", findings, err)
	}
	for _, id := range ids {
		rec, _, _ := loadRecord(ctx, w.inner, id, w.ring.People.OwnerSigners, nil)
		if v, _ := w2.sharing.SeqFloor.Get(ctx, id); v != rec.Seq || v == 0 {
			t.Errorf("%s: floor %d, record %d", id, v, rec.Seq)
		}
	}
}

func TestVerifyIndexRaisesTheFloorInOneWrite(t *testing.T) {
	ctx := context.Background()
	fl := &FileSeqFloor{Path: filepath.Join(t.TempDir(), "floor.json")}
	w := newWorldFloor(t, fl)
	for i := 0; i < 5; i++ {
		w.e2eeDoc(t)
	}
	fresh := &FileSeqFloor{Path: filepath.Join(t.TempDir(), "fresh.json")}
	s := &Sharing{Store: w.inner, Adapter: w.ad, Signer: w.signer, SeqFloor: fresh}
	if _, err := s.VerifyIndex(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh.writes > 2 { // the index's own seq when it is read, then every document at once
		t.Errorf("VerifyIndex wrote the floor %d times for 5 documents", fresh.writes)
	}
	idx, _ := s.Index(ctx)
	for id := range idx.Docs {
		if v, _ := fresh.Get(ctx, id); v == 0 {
			t.Errorf("%s was not raised", id)
		}
	}
}

func TestDegradedFloorIsReportedThroughTheKeyringAndSharing(t *testing.T) {
	ctx := context.Background()
	var noted []error
	SetFloorDegradedHandler(func(err error) { noted = append(noted, err) })
	defer SetFloorDegradedHandler(nil)
	kv := &mapKV{}
	fl := &StorageSeqFloor{Prefix: "p:", Store: kv}
	w := newWorldFloor(t, fl)
	id := w.e2eeDoc(t)
	if len(noted) != 0 {
		t.Fatalf("a working storage is not reported: %v", noted)
	}
	kv.fail = errors.New("QuotaExceededError")
	if _, err := w.saver.Load(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, w.doc(t, id)); err != nil {
		t.Fatal(err)
	}
	if len(noted) == 0 {
		t.Fatal("the floor failed and nothing said so")
	}
	for _, err := range noted {
		if !errors.Is(err, ErrFloorDegraded) {
			t.Errorf("%v", err)
		}
	}
	if fl.Err() == nil {
		t.Error("Err keeps it for a page that polls")
	}
}

func TestStorageSeqFloorReportsAFailingWrite(t *testing.T) {
	ctx := context.Background()
	kv := &mapKV{failSet: errors.New("QuotaExceededError")}
	f := &StorageSeqFloor{Prefix: "floor:", Store: kv}
	if err := f.Raise(ctx, "a", 3); !errors.Is(err, ErrFloorDegraded) {
		t.Errorf("Raise: %v", err)
	}
	if v, err := f.Get(ctx, "a"); err != nil || v != 3 {
		t.Errorf("the memory holds it and reads still work: %d %v", v, err)
	}
	if f.Err() == nil {
		t.Error("Err")
	}
}

func TestStorageSeqFloorRaiseOverADamagedValueNeverLowersIt(t *testing.T) {
	ctx := context.Background()
	kv := &mapKV{}
	f := &StorageSeqFloor{Prefix: "floor:", Store: kv}
	if err := f.Raise(ctx, "a", 10); err != nil {
		t.Fatal(err)
	}
	kv.m["floor:a"] = "not a number"
	if err := f.Raise(ctx, "a", 3); !errors.Is(err, ErrFloorDegraded) {
		t.Fatalf("a damaged value is reported: %v", err)
	}
	if v := kv.m["floor:a"]; v != "10" {
		t.Errorf("the stored floor is %q after Raise(3) over a damaged value, want 10", v)
	}
	if n, _ := f.Get(ctx, "a"); n != 10 {
		t.Errorf("Get: %d", n)
	}
}
