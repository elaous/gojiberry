package encrypt

import (
	"bytes"
	"context"
	"errors"
	"filippo.io/age"
	"github.com/elaous/gojiberry/docstore"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRekeyAllContinuesAndDoesNotHalfRekeyADocument(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	ring := &mutable{to: []age.Recipient{ana.r.Key}, keys: []age.Identity{ana.id}}
	a := New(inner, ring)
	const d1, d2, d3 = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	for _, id := range []string{d1 + "~a.json", d1 + "~b.json", d2 + "~a.json", d3 + "~a.json"} {
		_ = a.Save(ctx, id, []byte(id))
	}
	_ = inner.Save(ctx, d1+"~b.json", []byte("junk"))
	_ = inner.Save(ctx, d3+"~a.json", []byte("clear"))
	before, _ := inner.Load(ctx, d1+"~a.json")
	ring.to = []age.Recipient{ana.r.Key, bo.r.Key}
	n, err := a.RekeyAll(ctx)
	if err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, id := range []string{d1 + "~b.json", d3 + "~a.json"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("error does not name %s: %v", id, err)
		}
	}
	after, _ := inner.Load(ctx, d1+"~a.json")
	if !bytes.Equal(before.Data, after.Data) {
		t.Error("doc1 was half re-keyed")
	}
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	if _, err := asBo.Load(ctx, d2+"~a.json"); err != nil {
		t.Errorf("doc2 went on: %v", err)
	}
	a.AllowPlaintext = true
	if n, err := a.RekeyIDs(ctx, []string{d3 + "~a.json"}); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if d, err := asBo.Load(ctx, d3+"~a.json"); err != nil || string(d.Data) != "clear" {
		t.Fatalf("migrated: %v", err)
	}
}

func TestConcurrentSaveAndRekeyLoseNothing(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	a := New(inner, Static{To: []age.Recipient{ana.r.Key, bo.r.Key}, Keys: []age.Identity{ana.id}})
	_ = a.Save(ctx, "d", []byte("0"))
	var wg sync.WaitGroup
	wg.Add(2)
	last := 0
	go func() {
		defer wg.Done()
		for i := 1; i <= 30; i++ {
			if err := a.Save(ctx, "d", []byte(strconv.Itoa(i))); err != nil {
				t.Error(err)
			}
			last = i
		}
	}()
	go func() {
		defer wg.Done()
		for range 30 {
			if err := a.Rekey(ctx, "d"); err != nil && !errors.Is(err, docstore.ErrConflict) {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	d, err := a.Load(ctx, "d")
	if err != nil || string(d.Data) != strconv.Itoa(last) {
		t.Fatalf("the last save won: %q %v", d.Data, err)
	}
}

type failing struct {
	docstore.Adapter
	fail map[string]int
}

func (f *failing) Save(ctx context.Context, id string, data []byte) error {
	if f.fail[id] > 0 {
		f.fail[id]--
		return errors.New("injected failure")
	}
	return f.Adapter.Save(ctx, id, data)
}

func (f *failing) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if f.fail[id] > 0 {
		f.fail[id]--
		return errors.New("injected failure")
	}
	return f.Adapter.(docstore.ConditionalAdapter).SaveIf(ctx, id, data, prev)
}

func TestRemoveCanBeRetriedAfterAMidRekeyFailure(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	mem := docstore.NewMemoryAdapter()
	inner := &failing{Adapter: mem, fail: map[string]int{}}
	ad, sh := sharingSetup(t, inner, ana)
	f1, f2, f3 := docA+"~aaaaaaaaaaaa.json", docA+"~bbbbbbbbbbbb.json", "versions/"+docA+"/00000000000000000001.json"
	for _, id := range []string{f1, f2, f3} {
		_ = ad.Save(ctx, id, []byte(id))
	}
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	asBo := New(mem, Static{Keys: []age.Identity{bo.id}})
	for _, id := range []string{f1, f2, f3} {
		if _, err := asBo.Load(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	inner.fail[f2] = 1
	n, err := sh.Remove(ctx, docA, bo.r.Fingerprint)
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if p, _ := sh.Pending(ctx, docA); !p {
		t.Fatal("the record says a re-key is pending")
	}
	if _, err := asBo.Load(ctx, f2); err != nil {
		t.Fatal("f2 is the file that was not re-keyed")
	}
	n, err = sh.Remove(ctx, docA, bo.r.Fingerprint)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, id := range []string{f1, f2, f3} {
		if _, err := asBo.Load(ctx, id); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s: %v", id, err)
		}
	}
	if p, _ := sh.Pending(ctx, docA); p {
		t.Error("nothing pending now")
	}
	if _, err := sh.Remove(ctx, docA, bo.r.Fingerprint); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("%v", err)
	}
	inner.fail[f1] = 1
	cy := newPerson(t, "cy")
	if err := sh.Add(ctx, docA, cy.r, cy.r.Fingerprint); err == nil {
		t.Fatal("injected")
	}
	if n, err := sh.Repair(ctx, docA); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := New(mem, Static{Keys: []age.Identity{cy.id}}).Load(ctx, f1); err != nil {
		t.Fatal(err)
	}
}

func TestAddOfAListedKeyStillRekeys(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	ad, sh := sharingSetup(t, inner, ana)
	f := docA + "~aaaaaaaaaaaa.json"
	_ = ad.Save(ctx, f, []byte("x"))
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	_ = New(inner, Static{To: []age.Recipient{ana.r.Key}}).Save(ctx, f, []byte("stale"))
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	if _, err := asBo.Load(ctx, f); !errors.Is(err, ErrNoKey) {
		t.Fatal("stale file")
	}
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if d, err := asBo.Load(ctx, f); err != nil || string(d.Data) != "stale" {
		t.Fatalf("adding again re-keys: %v", err)
	}
}

func TestRemoveEverywhereContinuesPastAFailureAndCanBeRerun(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	mem := docstore.NewMemoryAdapter()
	inner := &failing{Adapter: mem, fail: map[string]int{}}
	ad, sh := sharingSetup(t, inner, ana)
	files := map[string]string{
		"11111111-aaaa-4aaa-8aaa-111111111111": "11111111-aaaa-4aaa-8aaa-111111111111~aaaaaaaaaaaa.json",
		"22222222-bbbb-4bbb-8bbb-222222222222": "22222222-bbbb-4bbb-8bbb-222222222222~bbbbbbbbbbbb.json",
		"33333333-cccc-4ccc-8ccc-333333333333": "33333333-cccc-4ccc-8ccc-333333333333~cccccccccccc.json",
	}
	var ids []string
	for doc, f := range files {
		_ = ad.Save(ctx, f, []byte(f))
		if err := sh.Add(ctx, doc, bo.r, bo.r.Fingerprint); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, doc)
	}
	sort.Strings(ids)
	inner.fail[files[ids[1]]] = 1
	done, n, err := sh.RemoveEverywhere(ctx, bo.r.Fingerprint)
	if err == nil || !strings.Contains(err.Error(), ids[1]) || len(done) != 2 || n != 2 {
		t.Fatalf("%v %d %v", done, n, err)
	}
	used, err := sh.Usage(ctx, bo.r.Fingerprint)
	if err != nil || len(used) != 1 || used[0] != ids[1] {
		t.Fatalf("%v %v", used, err)
	}
	done, n, err = sh.RemoveEverywhere(ctx, bo.r.Fingerprint)
	if err != nil || len(done) != 1 || n != 1 {
		t.Fatalf("%v %d %v", done, n, err)
	}
	asBo := New(mem, Static{Keys: []age.Identity{bo.id}})
	for _, f := range files {
		if _, err := asBo.Load(ctx, f); !errors.Is(err, ErrNoKey) {
			t.Errorf("%s: %v", f, err)
		}
	}
	if used, _ := sh.Usage(ctx, bo.r.Fingerprint); len(used) != 0 {
		t.Errorf("%v", used)
	}
}

func TestSaveIsNotLostToARekeyOnAStoreWithoutSaveIf(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := unconditional{docstore.NewMemoryAdapter()}
	a := New(inner, Static{To: []age.Recipient{ana.r.Key, bo.r.Key}, Keys: []age.Identity{ana.id}})
	const id = "44444444-4444-4444-8444-444444444444"
	_ = a.Save(ctx, id, []byte("0"))
	var wg sync.WaitGroup
	wg.Add(3)
	last := 0
	go func() {
		defer wg.Done()
		for i := 1; i <= 100; i++ {
			if err := a.Save(ctx, id, []byte(strconv.Itoa(i))); err != nil {
				t.Error(err)
			}
			last = i
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			if err := a.Rekey(ctx, id); err != nil {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 30 {
			if _, err := a.RekeyAll(ctx); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	d, err := a.Load(ctx, id)
	if err != nil || string(d.Data) != strconv.Itoa(last) {
		t.Fatalf("the last save was lost: %q %v", d.Data, err)
	}
}

type ghost struct{ docstore.Adapter }

func (g ghost) List(ctx context.Context) ([]docstore.Meta, error) {
	m, err := g.Adapter.List(ctx)
	return append(m, docstore.Meta{ID: "55555555-5555-4555-8555-555555555555~a.json"}), err
}

func TestRekeyAllSkipsAFileThatVanished(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	a := New(ghost{docstore.NewMemoryAdapter()}, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	const id = "66666666-6666-4666-8666-666666666666"
	_ = a.Save(ctx, id, []byte("x"))
	if n, err := a.RekeyAll(ctx); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestRekeyRekeyAllAndRekeyDocumentGroupTheSame(t *testing.T) {
	ctx := context.Background()
	ana, bo := newPerson(t, "ana"), newPerson(t, "bo")
	inner := docstore.NewMemoryAdapter()
	ring := &mutable{to: []age.Recipient{ana.r.Key}, keys: []age.Identity{ana.id}}
	a := New(inner, ring)
	const d = "77777777-7777-4777-8777-777777777777"
	files := []string{d + ".json", d + "~prev.json", "versions/" + d + "/1.json"}
	for _, f := range files {
		_ = a.Save(ctx, f, []byte(f))
	}
	for _, f := range files {
		if g := DocID(f); g != d || groupOf(f) != d {
			t.Fatalf("%s groups as %q", f, g)
		}
	}
	asBo := New(inner, Static{Keys: []age.Identity{bo.id}})
	ring.to = []age.Recipient{ana.r.Key, bo.r.Key}
	if err := a.Rekey(ctx, files[0]); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, err := asBo.Load(ctx, f); err != nil {
			t.Errorf("Rekey of one file left %s: %v", f, err)
		}
	}
}

type onDocWrite struct {
	docstore.Adapter
	fn   func()
	done bool
}

func (h *onDocWrite) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if !h.done && !strings.HasPrefix(id, KeysPrefix) {
		h.done = true
		h.fn()
	}
	return h.Adapter.(docstore.ConditionalAdapter).SaveIf(ctx, id, data, prev)
}

func TestFinishDoesNotClearPendingWhenTheListChangedDuringTheRekey(t *testing.T) {
	ctx := context.Background()
	ana, bo, cy := newPerson(t, "ana"), newPerson(t, "bo"), newPerson(t, "cy")
	mem := docstore.NewMemoryAdapter()
	h := &onDocWrite{Adapter: mem}
	ad, sh := sharingSetup(t, h, ana)
	f := docA + "~aaaaaaaaaaaa.json"
	_ = ad.Save(ctx, f, []byte("x"))
	h.fn = func() {
		_, err := sh.mutate(ctx, docA, func(rec *sharedRecord) (bool, error) {
			p, _ := parseShared(sharedEntry{Type: cy.r.Type, Key: cy.r.Text, Label: "cy"})
			rec.Recipients = append(rec.Recipients, sharedEntry{Label: "cy", Type: p.Type, Fingerprint: p.Fingerprint, Key: p.Text, Added: time.Now().Unix()})
			rec.Pending = true
			return true, nil
		})
		if err != nil {
			t.Error(err)
		}
	}
	if err := sh.Add(ctx, docA, bo.r, bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if p, _ := sh.Pending(ctx, docA); p {
		t.Error("nothing is pending after the second pass")
	}
	for name, p := range map[string]person{"bo": bo, "cy": cy} {
		if _, err := New(mem, Static{Keys: []age.Identity{p.id}}).Load(ctx, f); err != nil {
			t.Errorf("%s cannot open the file: %v", name, err)
		}
	}
}

func TestRekeyAllReportsProgress(t *testing.T) {
	ctx := context.Background()
	ana := newPerson(t, "ana")
	inner := docstore.NewMemoryAdapter()
	a := New(inner, Static{To: []age.Recipient{ana.r.Key}, Keys: []age.Identity{ana.id}})
	for _, id := range []string{"11111111-1111-4111-8111-111111111111~a.json", "11111111-1111-4111-8111-111111111111~b.json", "22222222-2222-4222-8222-222222222222~a.json", "33333333-3333-4333-8333-333333333333~a.json"} {
		_ = a.Save(ctx, id, []byte(id))
	}
	var steps [][2]int
	a.RekeyProgress = func(done, total int) { steps = append(steps, [2]int{done, total}) }
	if _, err := a.RekeyAll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0] != [2]int{1, 3} || steps[2] != [2]int{3, 3} {
		t.Errorf("progress: %v, want one step per document: 1/3 2/3 3/3", steps)
	}
}
