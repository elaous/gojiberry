//go:build !(js && wasm)

package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docsearch"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

type memVault struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newVault() *memVault { return &memVault{data: map[string][]byte{}} }

func (v *memVault) Get(name string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return bytes.Clone(v.data[name]), nil
}
func (v *memVault) Put(name string, b []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.data[name] = bytes.Clone(b)
	return nil
}
func (v *memVault) Delete(name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.data, name)
	return nil
}

type countingAdapter struct {
	docstore.Adapter
	loads int
}

func (c *countingAdapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	if id != saver.CatalogKey { // the saver's own bookkeeping is not a document
		c.loads++
	}
	return c.Adapter.Load(ctx, id)
}

type rig struct {
	t      *testing.T
	mem    *countingAdapter
	saver  *saver.Saver
	target string
}

func newSearchRig(t *testing.T) *rig {
	t.Helper()
	mem := &countingAdapter{Adapter: docstore.NewMemoryAdapter()}
	return &rig{t: t, mem: mem, saver: &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: mem}}}, target: "m"}
}

func (r *rig) save(name, text string) saver.Entry {
	r.t.Helper()
	d := docfile.Document{Title: name, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}
	d.SetName(name)
	e, err := r.saver.Save(context.Background(), r.target, docfile.JSON, d)
	if err != nil {
		r.t.Fatal(err)
	}
	return e
}

func (r *rig) resave(id, name, text string) {
	r.t.Helper()
	d := docfile.Document{Title: name, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}
	d.SetName(name)
	d.SetID(id)
	if _, err := r.saver.Save(context.Background(), r.target, docfile.JSON, d); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) view() (docstore.Adapter, map[string]string) {
	r.t.Helper()
	entries, err := r.saver.List(context.Background(), r.target)
	if err != nil {
		r.t.Fatal(err)
	}
	var keys []string
	ids := map[string]string{}
	for _, e := range entries {
		keys = append(keys, e.Key)
		ids[e.Key] = e.ID
	}
	return docsearch.Subset(r.mem, keys), ids
}

func (r *rig) text(id string) string {
	r.t.Helper()
	d, err := r.saver.Load(context.Background(), r.target, id)
	if err != nil {
		r.t.Fatalf("the document cannot be opened: %v", err)
	}
	var b strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			b.WriteString(it.Chars.Text)
		}
	}
	return b.String()
}

func testKey(b byte) []byte {
	k, err := searchKey(bytes.Repeat([]byte{b}, 32), "m")
	if err != nil {
		panic(err)
	}
	return k
}

func refresh(t *testing.T, c *indexCache, v sealedVault, key []byte, r *rig) (docsearch.Result, bool) {
	t.Helper()
	view, _ := r.view()
	res, loaded, err := c.refresh(context.Background(), v, "m", key, view)
	if err != nil {
		t.Fatal(err)
	}
	return res, loaded
}

func TestKeptIndexMeansOnlyChangedDocumentsAreRead(t *testing.T) {
	r := newSearchRig(t)
	vault, key := newVault(), testKey(1)
	a := r.save("alpha", "the quick brown fox")
	r.save("beta", "jumps over the lazy dog")
	r.save("gamma", "pack my box with five dozen liquor jugs")

	var first indexCache
	res, loaded := refresh(t, &first, vault, key, r)
	if loaded || res.Added != 3 {
		t.Fatalf("first visit: loaded %v, %+v", loaded, res)
	}
	if len(vault.data) != 1 {
		t.Fatalf("the index was not kept: %d entries", len(vault.data))
	}

	r.mem.loads = 0
	var second indexCache
	res, loaded = refresh(t, &second, vault, key, r)
	if !loaded || res.Added+res.Updated+res.Removed != 0 || res.Unchanged != 3 || r.mem.loads != 0 {
		t.Fatalf("second visit: loaded %v, %+v, %d documents read", loaded, res, r.mem.loads)
	}
	if hits := second.ix.Search("lazy dog", 5); len(hits) != 1 || hits[0].Title != "beta" {
		t.Fatalf("the kept index does not find the word: %+v", hits)
	}

	r.resave(a.ID, "alpha", "the quick brown cat")
	r.save("delta", "sphinx of black quartz")
	e, _ := r.saver.List(context.Background(), r.target)
	for _, x := range e {
		if x.Name == "gamma" {
			if err := r.saver.Delete(context.Background(), r.target, x.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.mem.loads = 0
	var third indexCache
	res, loaded = refresh(t, &third, vault, key, r)
	if !loaded || res.Added != 2 || res.Removed != 2 || r.mem.loads != 2 {
		t.Fatalf("third visit: loaded %v, %+v, %d documents read", loaded, res, r.mem.loads)
	}
	if len(third.ix.Search("fox", 5)) != 0 || len(third.ix.Search("cat", 5)) != 1 || len(third.ix.Search("quartz", 5)) != 1 || len(third.ix.Search("liquor", 5)) != 0 {
		t.Fatal("the refreshed index is not in line with the documents")
	}
	var fourth indexCache
	if res, loaded := refresh(t, &fourth, vault, key, r); !loaded || res.Added+res.Updated+res.Removed != 0 {
		t.Fatalf("the refreshed index was not kept: loaded %v, %+v", loaded, res)
	}
}

func TestKeptIndexIsSealedAndATamperedOrForeignOneIsRebuilt(t *testing.T) {
	r := newSearchRig(t)
	vault, key := newVault(), testKey(1)
	r.save("Confidential plans", "the zeppelin schedule")
	var c indexCache
	refresh(t, &c, vault, key, r)
	kept := vault.data[keptName("m")]
	if len(kept) == 0 {
		t.Fatal("nothing kept")
	}
	for _, secret := range []string{"zeppelin", "schedule", "Confidential", "plans"} {
		if bytes.Contains(kept, []byte(secret)) {
			t.Fatalf("the kept index shows %q", secret)
		}
	}

	tampered := bytes.Clone(kept)
	tampered[len(tampered)/2] ^= 0x01
	vault.data[keptName("m")] = tampered
	var d indexCache
	res, loaded := refresh(t, &d, vault, key, r)
	if loaded || res.Added != 1 || len(d.ix.Search("zeppelin", 3)) != 1 {
		t.Fatalf("a tampered index: loaded %v, %+v", loaded, res)
	}
	if bytes.Equal(vault.data[keptName("m")], tampered) {
		t.Fatal("the tampered index was left in place")
	}

	var e indexCache
	res, loaded = refresh(t, &e, vault, testKey(2), r)
	if loaded || res.Added != 1 {
		t.Fatalf("a foreign index: loaded %v, %+v", loaded, res)
	}
	var f indexCache
	if _, loaded := refresh(t, &f, vault, testKey(2), r); !loaded {
		t.Fatal("the rebuilt index was not kept under the new key")
	}
	var g indexCache
	if _, loaded := refresh(t, &g, vault, key, r); loaded {
		t.Fatal("the old key opened an index sealed under another")
	}

	for _, junk := range [][]byte{{}, []byte("GJSI1"), []byte("not an index at all")} {
		vault.data[keptName("m")] = junk
		var h indexCache
		if res, loaded := refresh(t, &h, vault, key, r); loaded || res.Added != 1 {
			t.Fatalf("junk %q: loaded %v, %+v", junk, loaded, res)
		}
	}
}

func TestNoKeyMeansNothingIsKeptOrKeptIndexServed(t *testing.T) {
	r := newSearchRig(t)
	vault, key := newVault(), testKey(1)
	r.save("alpha", "the quick brown fox")
	var c indexCache
	refresh(t, &c, vault, key, r)
	before := bytes.Clone(vault.data[keptName("m")])

	r.save("beta", "a second document")
	if res, loaded := refresh(t, &c, vault, nil, r); loaded || res.Added != 2 {
		t.Fatalf("keyless: loaded %v, %+v: the kept index must not be used", loaded, res)
	}
	if !bytes.Equal(vault.data[keptName("m")], before) {
		t.Fatal("a keyless search wrote the kept index")
	}
	empty := newVault()
	var d indexCache
	refresh(t, &d, empty, nil, r)
	if len(empty.data) != 0 {
		t.Fatal("a keyless search kept an index")
	}
	var e indexCache
	refresh(t, &e, vault, key, r)
	if e.ix.Len() == 0 {
		t.Fatal("no index")
	}
	view := docsearch.Subset(r.mem, nil) // every document gone, as seen without the key
	if _, _, err := e.refresh(context.Background(), vault, "m", nil, view); err != nil {
		t.Fatal(err)
	}
	if e.ix.Len() != 0 {
		t.Fatal("the index built under a key is still served without it")
	}
}

func TestSearchKeyIsPerPlaceAndSecret(t *testing.T) {
	s := bytes.Repeat([]byte{7}, 32)
	a, _ := searchKey(s, "browser")
	b, _ := searchKey(s, "server")
	c, _ := searchKey(bytes.Repeat([]byte{8}, 32), "browser")
	if len(a) != 32 || bytes.Equal(a, b) || bytes.Equal(a, c) {
		t.Fatal("keys are not separated by place and secret")
	}
	if _, err := searchKey(nil, "browser"); err == nil {
		t.Fatal("a key from no secret")
	}
	if keyTag(a) == keyTag(b) || strings.Contains(keyTag(a), string(a[:2])) {
		t.Fatal("the key tag does not tell keys apart")
	}
}

func plan(t *testing.T, r *rig, query, repl string) (*docsearch.Plan, map[string]string) {
	t.Helper()
	view, ids := r.view()
	p, err := docsearch.PlanReplace(context.Background(), view, canvas.FindOptions{Query: query}, repl)
	if err != nil {
		t.Fatal(err)
	}
	return p, ids
}

func keysOf(p *docsearch.Plan) []string {
	var k []string
	for _, c := range p.Changes {
		k = append(k, c.ID)
	}
	return k
}

func TestReplaceAcrossDocumentsKeepsTheOldTextAsAVersion(t *testing.T) {
	ctx := context.Background()
	r := newSearchRig(t)
	a := r.save("alpha", "send it to Acme today")
	b := r.save("beta", "Acme again")
	r.save("gamma", "nothing to see")
	p, ids := plan(t, r, "acme", "Globex")
	if len(p.Changes) != 2 {
		t.Fatalf("%d changes", len(p.Changes))
	}
	done, conflicts, err := applyReplace(ctx, r.saver, r.target, "Sam", "Before replace", p, ids, keysOf(p))
	if err != nil || len(done) != 2 || len(conflicts) != 0 {
		t.Fatalf("%v %d %v", err, len(done), conflicts)
	}
	if got := r.text(a.ID); got != "send it to Globex today" {
		t.Fatalf("alpha opens with %q", got)
	}
	if got := r.text(b.ID); got != "Globex again" {
		t.Fatalf("beta opens with %q", got)
	}
	vs, err := r.saver.Versions(ctx, r.target, a.ID)
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions %v %v", vs, err)
	}
	if vs[0].Name != "Before replace" || vs[0].Author != "Sam" {
		t.Fatalf("the version is %+v", vs[0])
	}
	old, err := r.saver.LoadVersion(ctx, r.target, vs[0].Key)
	if err != nil || old.Content[0].Chars.Text != "send it to Acme today" {
		t.Fatalf("the version does not keep the old text: %v %+v", err, old.Content)
	}
	if rep, err := r.saver.Verify(ctx, r.target); err != nil || len(rep.Lost) != 0 || len(rep.Restored) != 0 {
		t.Fatalf("the saver finds the store damaged: %v %+v", err, rep)
	}

	restored, kept, err := undoReplace(ctx, r.saver, r.target, done)
	if err != nil || restored != 2 || kept != 0 {
		t.Fatalf("undo: %v %d %d", err, restored, kept)
	}
	if got := r.text(a.ID); got != "send it to Acme today" {
		t.Fatalf("alpha after undo: %q", got)
	}
}

func TestReplaceAndUndoNeverOverwriteALaterSave(t *testing.T) {
	ctx := context.Background()
	r := newSearchRig(t)
	a := r.save("alpha", "Acme one")
	b := r.save("beta", "Acme two")
	p, ids := plan(t, r, "acme", "Globex")
	r.resave(a.ID, "alpha", "Acme one, edited meanwhile")
	done, conflicts, err := applyReplace(ctx, r.saver, r.target, "", "Before replace", p, ids, keysOf(p))
	if err != nil || len(done) != 1 || len(conflicts) != 1 {
		t.Fatalf("%v %d %v", err, len(done), conflicts)
	}
	if got := r.text(a.ID); got != "Acme one, edited meanwhile" {
		t.Fatalf("a document saved since the preview was overwritten: %q", got)
	}
	if vs, _ := r.saver.Versions(ctx, r.target, a.ID); len(vs) != 0 {
		t.Fatal("a version was written for a document that was not replaced")
	}
	r.resave(b.ID, "beta", "Globex two, then typed on")
	restored, kept, err := undoReplace(ctx, r.saver, r.target, done)
	if err != nil || restored != 0 || kept != 1 {
		t.Fatalf("undo: %v %d %d", err, restored, kept)
	}
	if got := r.text(b.ID); got != "Globex two, then typed on" {
		t.Fatalf("undo threw away a later save: %q", got)
	}
}

func TestOneChangeKeepsOnlyThatChange(t *testing.T) {
	r := newSearchRig(t)
	vault, key := newVault(), testKey(3)
	for i := 0; i < 12; i++ {
		r.save("doc"+strconv.Itoa(i), strings.Repeat("a long paragraph about nothing in particular ", 40)+strconv.Itoa(i))
	}
	var first indexCache
	refresh(t, &first, vault, key, r)
	base := append([]byte(nil), vault.data[keptName(r.target)]...)

	r.save("late", "one more document about sphinxes")
	var second indexCache
	if res, _ := refresh(t, &second, vault, key, r); res.Added != 1 {
		t.Fatalf("%+v", res)
	}
	if !bytes.Equal(vault.data[keptName(r.target)], base) {
		t.Error("the base was sealed again for one change")
	}
	d := vault.data[deltaName(r.target, 1)]
	if d == nil || len(d) > len(base)/4 {
		t.Fatalf("the change is not kept small: %d bytes against a base of %d", len(d), len(base))
	}
	if bytes.Contains(d, []byte("sphinxes")) {
		t.Error("the change holds readable text")
	}

	r.mem.loads = 0
	var third indexCache
	res, loaded := refresh(t, &third, vault, key, r)
	if !loaded || res.Added+res.Updated+res.Removed != 0 || r.mem.loads != 0 || len(third.ix.Search("sphinxes", 5)) != 1 {
		t.Fatalf("third visit: loaded %v, %+v, %d read", loaded, res, r.mem.loads)
	}

	r.save("later", "yet another about quartz")
	var fourth indexCache
	refresh(t, &fourth, vault, key, r)
	vault.data[deltaName(r.target, 1)][len(d)-1] ^= 1
	r.mem.loads = 0
	var fifth indexCache
	res, loaded = refresh(t, &fifth, vault, key, r)
	if !loaded || res.Added != 2 || r.mem.loads != 2 || len(fifth.ix.Search("quartz", 5)) != 1 || len(fifth.ix.Search("sphinxes", 5)) != 1 {
		t.Fatalf("after an altered change: loaded %v, %+v, %d read", loaded, res, r.mem.loads)
	}
}

func TestManyChangesAreFoldedIntoANewBase(t *testing.T) {
	r := newSearchRig(t)
	vault, key := newVault(), testKey(4)
	r.save("first", strings.Repeat("filler words to make the base big enough ", 200))
	var c indexCache
	refresh(t, &c, vault, key, r)
	first := append([]byte(nil), vault.data[keptName(r.target)]...)
	for i := 0; i <= maxDeltas; i++ {
		r.save("n"+strconv.Itoa(i), "small "+strconv.Itoa(i))
		refresh(t, &c, vault, key, r)
	}
	if bytes.Equal(vault.data[keptName(r.target)], first) {
		t.Error("the base was never sealed again")
	}
	if len(vault.data) > maxDeltas+2 {
		t.Errorf("%d entries kept after folding", len(vault.data))
	}
	var again indexCache
	if res, loaded := refresh(t, &again, vault, key, r); !loaded || res.Added+res.Updated+res.Removed != 0 {
		t.Fatalf("the folded index was not kept: %v %+v", loaded, res)
	}
}
