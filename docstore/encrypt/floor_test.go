package encrypt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type mapKV struct {
	mu   sync.Mutex
	m    map[string]string
	fail error
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
	// Concurrent raises (run under -race) keep the highest.
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
	// Storage that fails (a private window, a full quota): the floor still works in memory,
	// and says so.
	kv.fail = errors.New("SecurityError")
	_ = g.Raise(ctx, "a", 7)
	if v, err := g.Get(ctx, "a"); err != nil || v != 7 {
		t.Errorf("%d %v", v, err)
	}
	if g.Err() == nil {
		t.Error("Err should report the failing storage")
	}
	// A damaged value is noted and the memory value is used.
	kv.fail = nil
	kv.m["floor:z"] = "banana"
	if v, err := g.Get(ctx, "z"); err != nil || v != 0 || g.Err() == nil {
		t.Errorf("%d %v %v", v, err, g.Err())
	}
}

// TestFloorsRefuseAReplayedAndADeletedRecordThroughTheRealStack uses the file floor as the
// keyring's, as a server would: the replay and the deletion are refused (ErrStaleRecord) on
// the first read, and a new process with the same file still refuses.
func TestFloorsRefuseAReplayedAndADeletedRecordThroughTheRealStack(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "floor.json")
	w := newWorldFloor(t, &FileSeqFloor{Path: path})
	id, old := w.confidentialWithOldRecord(t)
	// A restarted client: nothing in memory, only the file.
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
