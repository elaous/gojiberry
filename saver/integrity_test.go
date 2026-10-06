package saver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

func docWithText(name, text string) docfile.Document {
	d := named(name)
	d.Content = []docfile.Item{{Chars: &docfile.Chars{Text: text}}}
	return d
}

func textOf(d docfile.Document) string {
	var b strings.Builder
	for _, it := range d.Content {
		if it.Chars != nil {
			b.WriteString(it.Chars.Text)
		}
	}
	return b.String()
}

// lenientCut finds how to cut a protobuf file so that what is left still parses as a
// (shorter, different) document: protobuf has no length or checksum for the whole
// message, so a file cut at a field boundary is a valid file.
func lenientCut(t *testing.T, data []byte) []byte {
	t.Helper()
	for n := len(data) - 1; n > 0; n-- {
		if d, err := docfile.ProtoBinary.Unmarshal(data[:n]); err == nil {
			if orig, _ := docfile.ProtoBinary.Unmarshal(data); orig.Title != d.Title || len(orig.Content) != len(d.Content) || textOf(orig) != textOf(d) {
				return data[:n]
			}
		}
	}
	t.Fatal("no lenient cut of this document")
	return nil
}

func twoSaves(t *testing.T, s *Saver, c docfile.Codec) (id string) {
	t.Helper()
	ctx := context.Background()
	d := docWithText("plan", "version one")
	e, err := s.Save(ctx, "m", c, d)
	if err != nil {
		t.Fatal(err)
	}
	d2 := docWithText("plan", "version two, longer than the first")
	d2.SetID(e.ID)
	if _, err := s.Save(ctx, "m", c, d2); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func TestACutProtobufFileIsDetectedAndTheDocumentIsRestoredFromItsPreviousCopy(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.Open)
	cur, _ := s.Get(ctx, "m", id)
	stored, _ := mem.Load(ctx, cur.Key)
	cut := lenientCut(t, stored.Data)
	if err := mem.Save(ctx, cur.Key, cut); err != nil { // the file now parses: only the checksum can tell
		t.Fatal(err)
	}

	doc, rec, err := s.LoadChecked(ctx, "m", id)
	if err != nil || !rec.FromPrevious || textOf(doc) != "version one" {
		t.Fatalf("%q %+v %v: the cut file must not be shown as the document", textOf(doc), rec, err)
	}
	// The catalog now points at the good copy, and the damaged file is gone.
	again, rec2, err := s.LoadChecked(ctx, "m", id)
	if err != nil || rec2.FromPrevious || textOf(again) != "version one" {
		t.Fatalf("the second read is of the restored current file: %q %+v %v", textOf(again), rec2, err)
	}
	if _, err := mem.Load(ctx, cur.Key); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the damaged file is deleted: %v", err)
	}
}

func TestADocumentWithNoGoodCopyIsReportedDamagedNotShownCut(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, _ := s.Save(ctx, "m", docfile.Open, docWithText("solo", "only version, with some length to it"))
	stored, _ := mem.Load(ctx, e.Key)
	_ = mem.Save(ctx, e.Key, lenientCut(t, stored.Data))
	_, err := s.Load(ctx, "m", e.ID)
	if !errors.Is(err, ErrUnreadable) || !errors.Is(err, ErrDamaged) {
		t.Fatalf("%v", err)
	}
	// The file is not touched: it may still be worth something to someone.
	if _, err := mem.Load(ctx, e.Key); err != nil {
		t.Errorf("%v", err)
	}
}

// failingLoadsOf fails the loads of one key with an error that is not "not found".
type flaky struct {
	docstore.Adapter
	key string
}

func (f flaky) Load(ctx context.Context, id string) (docstore.Document, error) {
	if id == f.key {
		return docstore.Document{}, errors.New("disk on fire")
	}
	return f.Adapter.Load(ctx, id)
}

func TestAStoreFailureIsNotAReasonToShowAnOlderCopy(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.Open)
	cur, _ := s.Get(ctx, "m", id)
	bad := &Saver{Targets: []Target{{Name: "m", Adapter: flaky{mem, cur.Key}}}}
	if doc, err := bad.Load(ctx, "m", id); err == nil || errors.Is(err, ErrUnreadable) || textOf(doc) != "" {
		t.Fatalf("an older copy shown while the current one only failed to read: %q %v", textOf(doc), err)
	}
	// And nothing was switched or deleted.
	if _, err := mem.Load(ctx, cur.Key); err != nil {
		t.Errorf("%v", err)
	}
}

// A crash after the file was written and before the catalog was leaves the catalog
// pointing at the old, intact file: the document must not look damaged.
func TestACrashBetweenTheFileAndTheCatalogLeavesTheOldDocumentIntact(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, _ := s.Save(ctx, "m", docfile.Open, docWithText("doc", "the first save"))
	before, _ := mem.Load(ctx, CatalogKey)
	// The crash: a new generation file is written, the catalog is not.
	orphan := genKey(e.ID, docfile.Open)
	data, _ := docfile.Open.Marshal(docWithText("doc", "an unfinished save"))
	_ = mem.Save(ctx, orphan, data)
	after, _ := mem.Load(ctx, CatalogKey)
	if string(before.Data) != string(after.Data) {
		t.Fatal("the catalog changed")
	}
	doc, rec, err := s.LoadChecked(ctx, "m", e.ID)
	if err != nil || rec.FromPrevious || textOf(doc) != "the first save" {
		t.Fatalf("%q %+v %v: the old file is still the current one and checks out", textOf(doc), rec, err)
	}
}

func TestVerifyRestoresSealsAndReports(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	healthy := twoSaves(t, s, docfile.Open)
	_ = healthy
	restorable := twoSaves(t, s, docfile.Open)
	lostE, _ := s.Save(ctx, "m", docfile.Open, docWithText("lost", "a document with a single copy of some length"))
	legacyE, _ := s.Save(ctx, "m", docfile.Open, docWithText("legacy", "saved before checksums"))

	// Damage the restorable one's current file and the lost one's only file.
	cur, _ := s.Get(ctx, "m", restorable)
	stored, _ := mem.Load(ctx, cur.Key)
	_ = mem.Save(ctx, cur.Key, lenientCut(t, stored.Data))
	ls, _ := mem.Load(ctx, lostE.Key)
	_ = mem.Save(ctx, lostE.Key, lenientCut(t, ls.Data))
	// Make the legacy one look like it was saved before checksums existed.
	raw, _ := mem.Load(ctx, CatalogKey)
	var c catalog
	_ = json.Unmarshal(raw.Data, &c)
	le := c.Docs[legacyE.ID]
	le.Sum = ""
	c.Docs[legacyE.ID] = le
	b, _ := json.Marshal(&c)
	_ = mem.Save(ctx, CatalogKey, b)

	rep, err := s.Verify(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 4 || len(rep.Restored) != 1 || rep.Restored[0] != "plan" || len(rep.Lost) != 1 || rep.Lost[0] != "lost" || rep.Sealed != 1 {
		t.Fatalf("%+v", rep)
	}
	// A second pass finds nothing more to do about the restored and sealed ones.
	rep2, _ := s.Verify(ctx, "m")
	if len(rep2.Restored) != 0 || rep2.Sealed != 0 || len(rep2.Lost) != 1 {
		t.Errorf("%+v", rep2)
	}
	if d, err := s.Load(ctx, "m", restorable); err != nil || textOf(d) != "version one" {
		t.Errorf("%q %v", textOf(d), err)
	}
	if _, err := mem.Load(ctx, lostE.Key); err != nil {
		t.Errorf("the lost document's file is left as it is: %v", err)
	}
}

func TestRebuildKeepsTheNewestAsCurrentAndTheNextAsPrevious(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.Open)
	_ = mem.Delete(ctx, CatalogKey)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	raw, _ := mem.Load(ctx, CatalogKey)
	var c catalog
	_ = json.Unmarshal(raw.Data, &c)
	e := c.Docs[id]
	if e.Key == "" || e.Prev == "" || e.Sum == "" || e.PrevSum == "" || e.Key == e.Prev {
		t.Fatalf("%+v", e)
	}
	if d, err := s.Load(ctx, "m", id); err != nil || textOf(d) != "version two, longer than the first" {
		t.Errorf("%q %v", textOf(d), err)
	}
	if fixed, err := s.Repair(ctx, "m"); err != nil || fixed {
		t.Errorf("a store with previous copies is consistent: %v %v", fixed, err)
	}
}

func TestDeleteAndPurgeRemoveEveryCopy(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.Open)
	if err := s.Trash(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
	metas, _ := mem.List(ctx)
	for _, m := range metas {
		if strings.HasPrefix(m.ID, id) {
			t.Errorf("%s is left", m.ID)
		}
	}
	id2 := twoSaves(t, s, docfile.Open)
	if err := s.Delete(ctx, "m", id2); err != nil {
		t.Fatal(err)
	}
	metas, _ = mem.List(ctx)
	for _, m := range metas {
		if strings.HasPrefix(m.ID, id2) {
			t.Errorf("%s is left", m.ID)
		}
	}
}

func TestNoPreviousCopiesWhenTurnedOff(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}, PreviousCopies: -1}
	id := twoSaves(t, s, docfile.Open)
	metas, _ := mem.List(ctx)
	n := 0
	for _, m := range metas {
		if strings.HasPrefix(m.ID, id) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d files for one document with copies off", n)
	}
}

// lockingAdapter stands for a store others share that cannot write conditionally: it
// offers a lock instead, and records whether the catalog was written with it held.
type lockingAdapter struct {
	docstore.Adapter
	held      bool
	locks     int
	badWrites int
}

func (l *lockingAdapter) Lock(context.Context) (func(), error) {
	l.held = true
	l.locks++
	return func() { l.held = false }, nil
}

func (l *lockingAdapter) Save(ctx context.Context, id string, data []byte) error {
	if id == CatalogKey && !l.held {
		l.badWrites++
	}
	return l.Adapter.Save(ctx, id, data)
}

func TestTheCatalogIsUpdatedWhileHoldingTheStoresLock(t *testing.T) {
	ctx := context.Background()
	l := &lockingAdapter{Adapter: docstore.NewMemoryAdapter()}
	s := &Saver{Targets: []Target{{Name: "m", Adapter: l}}}
	if _, err := s.Save(ctx, "m", docfile.Open, docWithText("a", "one")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFolder(ctx, "m", "", "F"); err != nil {
		t.Fatal(err)
	}
	if l.locks < 2 || l.badWrites != 0 || l.held {
		t.Errorf("locks=%d catalog writes without the lock=%d still held=%v", l.locks, l.badWrites, l.held)
	}
}

// Deleting a folder does not remove a trashed document whose current file is bad but
// whose previous copy is good: it is restored from that copy and moved out of the folder.
func TestDeleteFolderKeepsATrashedDocumentWithAGoodPreviousCopy(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	f, _ := s.CreateFolder(ctx, "m", "", "Old")
	d := docWithText("plan", "kept")
	d.SetFolder(f.ID)
	e, err := s.Save(ctx, "m", docfile.JSON, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ctx, "m", e.ID); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.Get(ctx, "m", e.ID)
	_ = mem.Save(ctx, cur.Key, []byte("{ not json"))
	purged, err := s.DeleteFolderReport(ctx, "m", f.ID)
	if err != nil || len(purged) != 0 {
		t.Fatalf("nothing is removed: %v, %v", purged, err)
	}
	got, err := s.Load(ctx, "m", e.ID)
	if err != nil || textOf(got) != "kept" {
		t.Fatalf("the previous copy stands in: %q, %v", textOf(got), err)
	}
	if ent, err := s.Get(ctx, "m", e.ID); err != nil || ent.Folder != "" {
		t.Fatalf("it is out of the folder: %+v, %v", ent, err)
	}
}

// A store whose modification times are coarse (every file the same): a rebuild still
// picks the newest save as the current one, by the save time inside the file.
type coarseTimes struct{ docstore.Adapter }

func (c coarseTimes) List(ctx context.Context) ([]docstore.Meta, error) {
	ms, err := c.Adapter.List(ctx)
	for i := range ms {
		ms[i].UpdatedAt = ms[i].UpdatedAt.Truncate(time.Hour)
	}
	return ms, err
}

func TestRebuildPicksTheNewestSaveWhenModificationTimesTie(t *testing.T) {
	ctx := context.Background()
	for range 10 { // the order of a listing is not fixed
		s := &Saver{Targets: []Target{{Name: "m", Adapter: coarseTimes{docstore.NewMemoryAdapter()}}}}
		id := twoSaves(t, s, docfile.JSON)
		if err := s.Rebuild(ctx, "m"); err != nil {
			t.Fatal(err)
		}
		d, err := s.Load(ctx, "m", id)
		if err != nil || !strings.HasPrefix(textOf(d), "version two") {
			t.Fatalf("after a rebuild: %q, %v", textOf(d), err)
		}
	}
}

// A conflicting catalog write makes the update start over; the file the failed try
// wrote is removed, not left as an orphan.
type conflictOnce struct {
	docstore.Adapter
	done bool
}

func (c *conflictOnce) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	if strings.HasPrefix(id, CatalogKey) && !c.done {
		c.done = true
		return docstore.ErrConflict
	}
	return c.Adapter.(docstore.ConditionalAdapter).SaveIf(ctx, id, data, prev)
}

func TestARetriedSaveLeavesNoOrphanFile(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: &conflictOnce{Adapter: mem}}}}
	e, err := s.Save(ctx, "m", docfile.JSON, docWithText("plan", "one"))
	if err != nil {
		t.Fatal(err)
	}
	metas, _ := mem.List(ctx)
	n := 0
	for _, m := range metas {
		if strings.HasPrefix(m.ID, e.ID) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d files for one saved document", n)
	}
}

// Renaming rewrites the file but does not use up the previous copy: it stays the
// earlier save.
func TestRenameKeepsThePreviousSave(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.JSON)
	if err := s.Rename(ctx, "m", id, "renamed"); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.Get(ctx, "m", id)
	_ = mem.Save(ctx, cur.Key, []byte("{ not json"))
	d, rec, err := s.LoadChecked(ctx, "m", id)
	if err != nil || !rec.FromPrevious || textOf(d) != "version one" || d.Name() != "renamed" {
		t.Fatalf("%q %q %+v %v", textOf(d), d.Name(), rec, err)
	}
}

// After a recovery the new current file carries the catalog's name, so a rebuilt
// catalog keeps it.
func TestRecoveredFileKeepsTheCatalogsMetadata(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.JSON)
	if err := s.Rename(ctx, "m", id, "renamed"); err != nil {
		t.Fatal(err)
	}
	cur, _ := s.Get(ctx, "m", id)
	_ = mem.Save(ctx, cur.Key, []byte("{ not json"))
	if _, _, err := s.LoadChecked(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, "m", id)
	if err != nil || e.Name != "renamed" {
		t.Fatalf("%+v %v", e, err)
	}
}

// A current file that is gone with a damaged previous copy is not "gone": deleting the
// folder keeps the document.
func TestDeleteFolderKeepsADocumentWithADamagedPreviousCopy(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	f, _ := s.CreateFolder(ctx, "m", "", "Old")
	d := docWithText("plan", "x")
	d.SetFolder(f.ID)
	e, _ := s.Save(ctx, "m", docfile.JSON, d)
	_ = s.Trash(ctx, "m", e.ID)
	cur, _ := s.Get(ctx, "m", e.ID)
	var prev string
	_ = s.view(ctx, "m", func(_ Target, c *catalog) error { prev = c.Docs[e.ID].Prev; return nil })
	_ = mem.Delete(ctx, cur.Key)
	_ = mem.Save(ctx, prev, []byte("{ not json"))
	purged, err := s.DeleteFolderReport(ctx, "m", f.ID)
	if err != nil || len(purged) != 0 {
		t.Fatalf("%v %v", purged, err)
	}
	if _, err := s.Get(ctx, "m", e.ID); err != nil {
		t.Fatalf("the entry is kept: %v", err)
	}
	if _, err := mem.Load(ctx, prev); err != nil {
		t.Fatalf("the damaged copy is left alone: %v", err)
	}
}

// capped is a store with room for a number of bytes: a save that would go over is refused
// with ErrStorageFull, as a browser's quota does.
type capped struct {
	docstore.Adapter
	room int
}

func (c *capped) used(ctx context.Context) int {
	n := 0
	ms, _ := c.List(ctx)
	for _, m := range ms {
		n += m.Size
	}
	return n
}

func (c *capped) Save(ctx context.Context, id string, data []byte) error {
	old := 0
	if d, err := c.Load(ctx, id); err == nil {
		old = len(d.Data)
	}
	if c.used(ctx)-old+len(data) > c.room {
		return docstore.ErrStorageFull
	}
	return c.Adapter.Save(ctx, id, data)
}

// A store with room for little more than one copy of a document still takes saves: the
// previous copy is given up first, and as a last resort the current file is written over.
func TestASaveFitsInAFullStoreByGivingUpTheBackup(t *testing.T) {
	ctx := context.Background()
	for _, rooms := range []struct{ room int }{{room: 2300}, {room: 1500}} {
		mem := &capped{Adapter: docstore.NewMemoryAdapter(), room: rooms.room}
		s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
		text := strings.Repeat("x", 900)
		d := docWithText("plan", text+"1")
		var id string
		for i := 1; i <= 4; i++ {
			d = docWithText("plan", text+string(rune('0'+i)))
			if id != "" {
				d.SetID(id)
			}
			e, err := s.Save(ctx, "m", docfile.JSON, d)
			if err != nil {
				t.Fatalf("room %d, save %d: %v", rooms.room, i, err)
			}
			id = e.ID
			got, err := s.Load(ctx, "m", id)
			if err != nil || textOf(got) != text+string(rune('0'+i)) {
				t.Fatalf("room %d, save %d reads back %q: %v", rooms.room, i, textOf(got)[len(text):], err)
			}
		}
	}
}

// aged reports every file as saved long ago.
type aged struct{ docstore.Adapter }

func (a aged) List(ctx context.Context) ([]docstore.Meta, error) {
	ms, err := a.Adapter.List(ctx)
	for i := range ms {
		ms[i].UpdatedAt = time.Now().Add(-48 * time.Hour)
	}
	return ms, err
}

func TestCollectOrphansRemovesOnlyOldLeftoverGenerations(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: aged{mem}}}}
	id := twoSaves(t, s, docfile.JSON)
	orphan := id + "~aaaaaaaaaaaa.json"
	other := "notes.txt"
	_ = mem.Save(ctx, orphan, []byte("{}"))
	_ = mem.Save(ctx, other, []byte("keep"))
	n, err := s.CollectOrphans(ctx, "m", time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("removed %d: %v", n, err)
	}
	if _, err := mem.Load(ctx, orphan); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the leftover is gone: %v", err)
	}
	if _, err := mem.Load(ctx, other); err != nil {
		t.Errorf("a file that is not a saved generation is left alone: %v", err)
	}
	if d, err := s.Load(ctx, "m", id); err != nil || !strings.HasPrefix(textOf(d), "version two") {
		t.Errorf("the document and its previous copy are untouched: %v", err)
	}
	// A young file might be a save in progress.
	_ = mem.Save(ctx, id+"~bbbbbbbbbbbb.json", []byte("{}"))
	young := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	if n, _ := young.CollectOrphans(ctx, "m", time.Hour); n != 0 {
		t.Errorf("a file just written was removed")
	}
}

func TestRebuildPutsBackTheSaveTheOldCatalogVouchedFor(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.JSON)
	cur, _ := s.Get(ctx, "m", id)
	stored, _ := mem.Load(ctx, cur.Key)
	// A change that still parses: one character of the text.
	flipped := strings.Replace(string(stored.Data), "version two", "version twX", 1)
	_ = mem.Save(ctx, cur.Key, []byte(flipped))
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	// The earlier save, which the old catalog vouched for, is the document again.
	d, rec, err := s.LoadChecked(ctx, "m", id)
	if err != nil || rec.FromPrevious || textOf(d) != "version one" {
		t.Fatalf("the rebuild put the vouched-for save back: %q %+v %v", textOf(d), rec, err)
	}
}

// With no earlier copy to put back, a changed file is still found damaged after a rebuild.
func TestRebuildKeepsAChangedOnlyCopyFlagged(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, err := s.Save(ctx, "m", docfile.JSON, docWithText("plan", "version one"))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := mem.Load(ctx, e.Key)
	_ = mem.Save(ctx, e.Key, []byte(strings.Replace(string(stored.Data), "version one", "version onX", 1)))
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, "m", e.ID); !errors.Is(err, ErrDamaged) {
		t.Fatalf("%v", err)
	}
}

func TestVerifyForgetsABadPreviousCopy(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.JSON)
	var prev string
	_ = s.view(ctx, "m", func(_ Target, c *catalog) error { prev = c.Docs[id].Prev; return nil })
	_ = mem.Save(ctx, prev, []byte("{ not json"))
	rep, err := s.Verify(ctx, "m")
	if err != nil || len(rep.BackupsLost) != 1 || len(rep.Lost) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := mem.Load(ctx, prev); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the bad copy is deleted: %v", err)
	}
	rep, _ = s.Verify(ctx, "m")
	if len(rep.BackupsLost) != 0 {
		t.Errorf("found again: %+v", rep)
	}
}

func TestOpenFileCarriesTheHistoryAndSavingItBringsItBack(t *testing.T) {
	ctx := context.Background()
	src := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	e, err := src.Save(ctx, "m", docfile.JSON, docWithText("plan", "one"))
	if err != nil {
		t.Fatal(err)
	}
	d1 := docWithText("plan", "one")
	d1.SetID(e.ID)
	if _, err := src.SaveVersion(ctx, "m", docfile.JSON, d1); err != nil {
		t.Fatal(err)
	}
	d2 := docWithText("plan", "two")
	d2.SetID(e.ID)
	if _, err := src.Save(ctx, "m", docfile.JSON, d2); err != nil {
		t.Fatal(err)
	}
	if _, err := src.SaveVersion(ctx, "m", docfile.JSON, d2); err != nil {
		t.Fatal(err)
	}
	data, err := src.ExportOpen(ctx, "m", e.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := docfile.Open.Unmarshal(data)
	if err != nil || len(doc.History) != 2 || textOf(*doc.History[0].Doc) != "one" || textOf(*doc.History[1].Doc) != "two" || doc.History[0].Saved >= doc.History[1].Saved {
		t.Fatalf("history in the file: %d %v", len(doc.History), err)
	}
	dst := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	got, err := dst.Save(ctx, "m", docfile.JSON, doc)
	if err != nil {
		t.Fatal(err)
	}
	vs, err := dst.Versions(ctx, "m", got.ID)
	if err != nil || len(vs) != 2 {
		t.Fatalf("versions restored: %d %v", len(vs), err)
	}
	if stored, err := dst.Load(ctx, "m", got.ID); err != nil || len(stored.History) != 0 {
		t.Fatalf("the stored document has no history inside: %v", err)
	}
}

func TestOpenSizesAndDownloadWithoutHistory(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	e, _ := s.Save(ctx, "m", docfile.JSON, docWithText("plan", strings.Repeat("a", 3000)))
	d := docWithText("plan", strings.Repeat("b", 2000))
	d.SetID(e.ID)
	_, _ = s.SaveVersion(ctx, "m", docfile.JSON, d)
	_, _ = s.SaveVersion(ctx, "m", docfile.JSON, d)
	sz, err := s.OpenSizes(ctx, "m", e.ID)
	if err != nil || sz.Versions != 2 || sz.Current < 3000 || sz.History < 4000 {
		t.Fatalf("%+v %v", sz, err)
	}
	with, _ := s.ExportOpen(ctx, "m", e.ID, true)
	without, _ := s.ExportOpen(ctx, "m", e.ID, false)
	if int64(len(without)) != sz.Current || int64(len(with)) != sz.Current+sz.History {
		t.Errorf("sizes %d/%d against %+v", len(without), len(with), sz)
	}
	doc, _ := docfile.Open.Unmarshal(without)
	if len(doc.History) != 0 {
		t.Error("no history in the file without it")
	}
}

// An older file the old catalog knows no checksum for is not vouched for: it does not
// win over the newer file.
func TestRebuildDoesNotPutBackAnUnvouchedOlderFile(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, err := s.Save(ctx, "m", docfile.JSON, docWithText("plan", "version two"))
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := mem.Load(ctx, e.Key)
	// A leftover with the same ID, older by its own save time, that no catalog lists.
	old := docWithText("plan", "version one")
	old.SetID(e.ID)
	old.Updated = 1
	data, _ := docfile.JSON.Marshal(old)
	_ = mem.Save(ctx, e.ID+"~aaaaaaaaaaaa.json", data)
	// The newer file changes (still parses).
	_ = mem.Save(ctx, e.Key, []byte(strings.Replace(string(stored.Data), "version two", "version twX", 1)))
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	var cur catalogEntry
	_ = s.view(ctx, "m", func(_ Target, c *catalog) error { cur = c.Docs[e.ID]; return nil })
	if cur.Key != e.Key {
		t.Fatalf("the unvouched leftover won: %+v", cur)
	}
	if _, err := mem.Load(ctx, e.Key); err != nil {
		t.Errorf("the newer file was deleted: %v", err)
	}
}

// Three files for one ID: the vouched-for one is the document, the changed one is
// skipped, and only an old enough skipped file is deleted.
func TestRebuildWithThreeFilesAndTheChangedOneIsCollectedOnlyWhenOld(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	id := twoSaves(t, s, docfile.JSON)
	var cur catalogEntry
	_ = s.view(ctx, "m", func(_ Target, c *catalog) error { cur = c.Docs[id]; return nil })
	stored, _ := mem.Load(ctx, cur.Key)
	_ = mem.Save(ctx, cur.Key, []byte(strings.Replace(string(stored.Data), "version two", "version twX", 1)))
	extra := docWithText("plan", "stray")
	extra.SetID(id)
	extra.Updated = 1
	data, _ := docfile.JSON.Marshal(extra)
	_ = mem.Save(ctx, id+"~cccccccccccc.json", data)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	var skipped []string
	var now catalogEntry
	_ = s.view(ctx, "m", func(_ Target, c *catalog) error { skipped, now = c.Skipped, c.Docs[id]; return nil })
	if now.Key == cur.Key || !slices.Contains(skipped, cur.Key) {
		t.Fatalf("the changed file is skipped and not the document: %+v %v", now, skipped)
	}
	// Young: kept.
	if n, _ := s.CollectOrphans(ctx, "m", time.Hour); n != 0 {
		t.Errorf("a young skipped file was removed (%d)", n)
	}
	if _, err := mem.Load(ctx, cur.Key); err != nil {
		t.Fatalf("kept: %v", err)
	}
	// Old: removed.
	old := &Saver{Targets: []Target{{Name: "m", Adapter: aged{mem}}}}
	if n, err := old.CollectOrphans(ctx, "m", time.Hour); err != nil || n == 0 {
		t.Fatalf("removed %d: %v", n, err)
	}
	if _, err := mem.Load(ctx, cur.Key); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the old changed file is gone: %v", err)
	}
}

// listOrdered lists the files in the given key order (any others after them).
type listOrdered struct {
	docstore.Adapter
	order []string
}

func (l listOrdered) List(ctx context.Context) ([]docstore.Meta, error) {
	ms, err := l.Adapter.List(ctx)
	rank := func(m docstore.Meta) int {
		if i := slices.Index(l.order, m.ID); i >= 0 {
			return i
		}
		return len(l.order)
	}
	slices.SortStableFunc(ms, func(a, b docstore.Meta) int { return rank(a) - rank(b) })
	return ms, err
}

// The outcome of a rebuild with a vouched-for save, a changed newer file and a stray does
// not depend on the order of the listing (the stray first or last, and the changed file
// before or after the vouched one), and the catalog it writes agrees with the store.
func TestRebuildWithAStrayDoesNotDependOnListOrder(t *testing.T) {
	ctx := context.Background()
	for _, order := range []string{"S B A", "S A B", "B S A", "B A S", "A S B", "A B S"} {
		mem := docstore.NewMemoryAdapter()
		s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
		id := twoSaves(t, s, docfile.JSON)
		var cur catalogEntry
		_ = s.view(ctx, "m", func(_ Target, c *catalog) error { cur = c.Docs[id]; return nil })
		stored, _ := mem.Load(ctx, cur.Key)
		_ = mem.Save(ctx, cur.Key, []byte(strings.Replace(string(stored.Data), "version two", "version twX", 1)))
		stray := docWithText("plan", "stray")
		stray.SetID(id)
		stray.Updated = 1
		data, _ := docfile.JSON.Marshal(stray)
		strayKey := id + "~cccccccccccc.json"
		_ = mem.Save(ctx, strayKey, data)
		keys := map[string]string{"S": strayKey, "B": cur.Key, "A": cur.Prev}
		var keyOrder []string
		for _, k := range strings.Fields(order) {
			keyOrder = append(keyOrder, keys[k])
		}

		s = &Saver{Targets: []Target{{Name: "m", Adapter: listOrdered{mem, keyOrder}}}}
		if err := s.Rebuild(ctx, "m"); err != nil {
			t.Fatal(err)
		}
		c := s.peekCatalog(ctx, s.Targets[0])
		if c == nil {
			t.Fatalf("%s: no catalog", order)
		}
		now := c.Docs[id]
		if now.Key != cur.Prev || !slices.Contains(c.Skipped, cur.Key) {
			t.Errorf("%s: the vouched file is the document, the changed one skipped: %+v %v", order, now, c.Skipped)
		}
		if now.Prev == cur.Key {
			t.Errorf("%s: the changed file is not the previous copy: %+v", order, now)
		}
		if now.Prev == strayKey && slices.Contains(c.Skipped, strayKey) {
			t.Errorf("%s: the stray is both previous copy and skipped: %+v %v", order, now, c.Skipped)
		}
		if bad, err := s.disagrees(ctx, s.Targets[0], c); err != nil || bad {
			t.Errorf("%s: the rebuilt catalog disagrees with the store (%v): %+v %v", order, err, now, c.Skipped)
		}
		if d, _, err := s.LoadChecked(ctx, "m", id); err != nil || textOf(d) != "version one" {
			t.Errorf("%s: %q %v", order, textOf(d), err)
		}
	}
}
