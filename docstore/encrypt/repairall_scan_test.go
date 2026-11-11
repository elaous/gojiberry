package encrypt

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docstore"
)

type headCounter struct {
	*flaky
	loaded, headed atomic.Int64
	perHead        map[string]int // the largest head asked for an id
	perLoad        map[string]int
}

func newHeadCounter(f *flaky) *headCounter {
	return &headCounter{flaky: f, perHead: map[string]int{}, perLoad: map[string]int{}}
}

func (c *headCounter) Load(ctx context.Context, id string) (docstore.Document, error) {
	d, err := c.flaky.Load(ctx, id)
	c.loaded.Add(int64(len(d.Data)))
	c.perLoad[id] += len(d.Data)
	return d, err
}

func (c *headCounter) LoadHead(ctx context.Context, id string, n int) ([]byte, error) {
	h, err := c.flaky.LoadHead(ctx, id, n)
	c.headed.Add(int64(len(h)))
	if len(h) > c.perHead[id] {
		c.perHead[id] = len(h)
	}
	return h, err
}

type noHead struct{ docstore.Adapter }

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func scanDigest(rep RepairAllReport) string {
	var sk []string
	for _, s := range rep.Skipped {
		sk = append(sk, fmt.Sprintf("%s:%s:%v", s.Doc, s.Reason, s.Files))
	}
	sort.Strings(sk)
	var by []string
	for _, b := range rep.BySigner {
		by = append(by, fmt.Sprintf("%s/%d/%d/%d", b.Fingerprint, b.Files, b.Docs, b.Records))
	}
	return fmt.Sprintf("total=%d signed=%d repaired=%d enc=%d resigned=%d files=%d todo=%v other=%v skipped=%v by=%v rem=%d",
		rep.Total, rep.Signed, rep.Repaired, rep.Encrypted, rep.Resigned, rep.Files, rep.Todo, rep.OtherSigner, sk, by, rep.Remaining)
}

type bigStore struct {
	mixedStore
	oldBig, signedBig, junkBig, foreignBig, plainBig string
}

func (w *world) bigMixed(t *testing.T) bigStore {
	t.Helper()
	ctx := context.Background()
	b := bigStore{mixedStore: w.mixed(t)}
	b.oldBig, b.signedBig = "expiry:oldbig", "expiry:signedbig"
	w.legacyItem(t, b.oldBig, randomBytes(t, 1<<20))
	if err := w.ad.Save(ctx, b.signedBig, randomBytes(t, 1<<20)); err != nil {
		t.Fatal(err)
	}
	b.junkBig = "collab:junkbig"
	if err := w.inner.Save(ctx, b.junkBig, append(ageHeaderJunk(5), randomBytes(t, 400<<10)...)); err != nil {
		t.Fatal(err)
	}
	b.foreignBig = "passkey/foreignbig"
	stranger := newPerson(t, "stranger")
	if err := w.inner.Save(ctx, b.foreignBig, sealRaw(t, []age.Recipient{stranger.r.Key}, frame(b.foreignBig, Binding{}, randomBytes(t, 300<<10), w.signer))); err != nil {
		t.Fatal(err)
	}
	b.plainBig = "public/plainbig"
	if err := w.inner.Save(ctx, b.plainBig, append([]byte("clear text "), randomBytes(t, 500<<10)...)); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRepairAllScanReadsOnlyTheHeadAndAgreesWithAFullScan(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	b := w.bigMixed(t)
	before := w.snapshot(t)

	full, err := w.over(noHead{w.inner}, nil).RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	sk := skippedBy(full)
	if sk[b.junk] != SkipUnreadable || sk[b.foreign] != SkipNoKey || sk[b.junkBig] != SkipUnreadable ||
		sk[b.foreignBig] != SkipNoKey || sk[b.plainBig] != SkipPlaintext || len(full.Todo) != 4 || full.Signed != 2 {
		t.Fatalf("full scan %s", scanDigest(full))
	}

	cnt := newHeadCounter(w.inner)
	head, err := w.over(cnt, nil).RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if scanDigest(head) != scanDigest(full) {
		t.Fatalf("head scan\n%s\nfull scan\n%s", scanDigest(head), scanDigest(full))
	}
	sameStore(t, "dry run", before, w.snapshot(t))
	for id, n := range cnt.perHead {
		if n > repairHeadBytes {
			t.Errorf("%s: a head of %d bytes", id, n)
		}
	}
	for _, id := range []string{b.oldBig, b.signedBig, b.foreignBig, b.plainBig} {
		if cnt.perLoad[id] != 0 || cnt.perHead[id] == 0 {
			t.Errorf("%s: loaded %d, headed %d", id, cnt.perLoad[id], cnt.perHead[id])
		}
	}
	if cnt.perLoad[b.junkBig] == 0 {
		t.Errorf("%s was not read in full", b.junkBig)
	}
	if cnt.headed.Load()+cnt.loaded.Load() >= 2<<20 {
		t.Errorf("the scan took %d + %d bytes", cnt.headed.Load(), cnt.loaded.Load())
	}
}

func TestRepairAllScanFallsBackWhenTheHeadIsCutShort(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.bigMixed(t)
	full, err := w.over(noHead{w.inner}, nil).RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 100, 1 << 10, 4 << 10, 70 << 10} {
		old := repairHeadBytes
		repairHeadBytes = n
		got, err := w.over(newHeadCounter(w.inner), nil).RepairAll(ctx, RepairAllOptions{DryRun: true})
		repairHeadBytes = old
		if err != nil {
			t.Fatal(err)
		}
		if scanDigest(got) != scanDigest(full) {
			t.Errorf("head of %d bytes\n%s\nfull scan\n%s", n, scanDigest(got), scanDigest(full))
		}
	}
	repairHeadBytes = 2 << 10
	defer func() { repairHeadBytes = 128 << 10 }()
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil || rep.Repaired != len(full.Todo) {
		t.Fatalf("%v %+v", err, rep)
	}
}

func TestRepairAllScanWithMoreRecipientsThanTheHeadHolds(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := "expiry:crowd"
	var rs []age.Recipient
	for i := 0; i < 40; i++ {
		x, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		rs = append(rs, x.Recipient())
	}
	rs = append(rs, w.server.Recipient())
	if err := w.inner.Save(ctx, id, sealRaw(t, rs, frame(id, Binding{}, randomBytes(t, 100<<10), w.signer))); err != nil {
		t.Fatal(err)
	}
	old := repairHeadBytes
	repairHeadBytes = 66 << 10
	defer func() { repairHeadBytes = old }()
	cnt := newHeadCounter(w.inner)
	rep, err := w.over(cnt, nil).RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || rep.Signed != 1 || rep.Left() != 0 {
		t.Fatalf("%v %s", err, scanDigest(rep))
	}
	if cnt.perLoad[id] == 0 {
		t.Errorf("the file was judged from its head alone: head %d, load %d", cnt.perHead[id], cnt.perLoad[id])
	}
}

func TestRepairAllScanHeadReadDoesNotBypassExpiry(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	clk := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := clk
	ea := docstore.NewExpiringAdapter(w.inner, docstore.FixedTTL(24*time.Hour))
	ea.Now = func() time.Time { return now }
	id := "expiry:lapsing"
	rs, err := w.ring.Recipients(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := ea.Save(ctx, "lapsing", sealRaw(t, rs, frame("lapsing", Binding{}, []byte("old frame"), nil))); err != nil {
		t.Fatal(err)
	}
	ad := New(ea, w.ring)
	ad.Signer = w.signer
	if sc := ad.scanFrames(ctx, []string{"lapsing"}, false); len(sc.unsigned) != 1 || sc.err != nil {
		t.Fatalf("live: %+v", sc)
	}
	now = clk.Add(48 * time.Hour)
	if _, ok := w.inner.Load(ctx, "lapsing"); ok != nil {
		t.Fatal("the inner store lost the file")
	}
	if sc := ad.scanFrames(ctx, []string{"lapsing"}, false); len(sc.unsigned) != 0 || len(sc.signers) != 0 || sc.err != nil {
		t.Fatalf("an expired file was scanned: %+v", sc)
	}
	if _, ok := any(ad).(docstore.HeadReader); ok {
		t.Error("the encrypting Adapter must not expose a head read: it would hand out ciphertext")
	}
}

func TestRepairAllScanThroughExpiringAdapterReadsOnlyTheHead(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	cnt := newHeadCounter(w.inner)
	ea := docstore.NewExpiringAdapter(cnt, docstore.FixedTTL(24*time.Hour))
	ad := New(ea, w.ring)
	ad.Signer = w.signer
	id := "passkey/viaexp"
	rs, err := w.ring.Recipients(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := ea.Save(ctx, id, sealRaw(t, rs, frame(id, Binding{}, randomBytes(t, 1<<20), nil))); err != nil {
		t.Fatal(err)
	}
	cnt.loaded.Store(0)
	cnt.headed.Store(0)
	sh := &Sharing{Store: w.inner, Adapter: ad, Signer: w.signer}
	rep, err := sh.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || len(rep.Todo) != 1 || rep.Todo[0] != id {
		t.Fatalf("%v %s", err, scanDigest(rep))
	}
	if cnt.loaded.Load() != 0 || cnt.headed.Load() == 0 || cnt.headed.Load() > int64(repairHeadBytes) {
		t.Errorf("loaded %d, headed %d", cnt.loaded.Load(), cnt.headed.Load())
	}
}

func (w *world) clearStore(t *testing.T) (clearDoc, clearItem string) {
	t.Helper()
	ctx := context.Background()
	clearDoc = "0b1c2d3e-0000-4000-8000-0000000000c1"
	if err := w.inner.Save(ctx, clearDoc+"~deadbeef0001.open", []byte("never encrypted document")); err != nil {
		t.Fatal(err)
	}
	clearItem = "public/clear"
	if err := w.inner.Save(ctx, clearItem, []byte("never encrypted item")); err != nil {
		t.Fatal(err)
	}
	return clearDoc, clearItem
}

func TestRepairAllEncryptPlaintextNeedsItsPreconditions(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.clearStore(t)
	before := w.snapshot(t)
	if _, err := w.sharing.RepairAll(ctx, RepairAllOptions{EncryptPlaintext: true}); err == nil || !strings.Contains(err.Error(), "AllowPlaintext") {
		t.Errorf("without AllowPlaintext: %v", err)
	}
	nosign := w.over(w.inner, func(a *Adapter) { a.AllowPlaintext = true; a.Signer = nil })
	if _, err := nosign.RepairAll(ctx, RepairAllOptions{EncryptPlaintext: true}); err == nil {
		t.Error("without a Signer it must not run")
	}
	sameStore(t, "refused", before, w.snapshot(t))
}

func TestRepairAllEncryptPlaintext(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	old := w.history(t)
	w.legacyV2(t, old)
	clearDoc, clearItem := w.clearStore(t)
	kept := map[string][]byte{"open/readme": []byte("in the clear"), "catalog:x": []byte("{}"), "acl:y": []byte("{}")}
	for id, d := range kept {
		if err := w.inner.Save(ctx, id, d); err != nil {
			t.Fatal(err)
		}
	}
	skipOpen := func(a *Adapter) {
		a.AllowPlaintext = true
		a.Skip = func(id string) bool { return DefaultSkip(id) || strings.HasPrefix(id, "open/") }
	}
	sh := w.over(w.inner, skipOpen)
	before := w.snapshot(t)

	rep, err := sh.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || skippedBy(rep)[clearDoc] != SkipPlaintext || skippedBy(rep)[clearItem] != SkipPlaintext || rep.Encrypted != 0 {
		t.Fatalf("%v %s", err, scanDigest(rep))
	}
	dry, err := sh.RepairAll(ctx, RepairAllOptions{DryRun: true, EncryptPlaintext: true})
	if err != nil || len(dry.Todo) != 3 || len(dry.Skipped) != 0 || dry.Encrypted != 0 || dry.Left() != 3 {
		t.Fatalf("%v %s", err, scanDigest(dry))
	}
	sameStore(t, "dry run", before, w.snapshot(t))

	rep, err = sh.RepairAll(ctx, RepairAllOptions{EncryptPlaintext: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Encrypted != 2 || rep.Repaired != 1 || len(rep.Skipped) != 0 || rep.Left() != 0 || rep.Total != 3 || rep.Files < 3 {
		t.Fatalf("%s", scanDigest(rep))
	}
	for _, f := range []string{clearDoc + "~deadbeef0001.open", clearItem} {
		if v := w.frameVersion(t, f); v != 3 {
			t.Errorf("%s is v%d", f, v)
		}
	}
	if d, err := sh.Adapter.Load(ctx, clearItem); err != nil || string(d.Data) != "never encrypted item" {
		t.Errorf("content changed: %q %v", d.Data, err)
	}
	if d, err := sh.Adapter.Load(ctx, clearDoc+"~deadbeef0001.open"); err != nil || string(d.Data) != "never encrypted document" {
		t.Errorf("content changed: %q %v", d.Data, err)
	}
	after := w.snapshot(t)
	for id, d := range kept {
		if got, _ := w.inner.Load(ctx, id); !bytes.Equal(got.Data, d) || after[id] != before[id] {
			t.Errorf("%s, left readable by Skip, was written", id)
		}
	}
	settled := w.snapshot(t)
	rep2, err := sh.RepairAll(ctx, RepairAllOptions{EncryptPlaintext: true})
	if err != nil || rep2.Encrypted != 0 || rep2.Repaired != 0 || rep2.Signed != 3 || rep2.Left() != 0 || rep2.Files != 0 {
		t.Fatalf("%v %s", err, scanDigest(rep2))
	}
	sameStore(t, "second run", settled, w.snapshot(t))
}

func TestRepairAllEncryptPlaintextIsAllOrNonePerDocument(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := "0b1c2d3e-0000-4000-8000-0000000000c2"
	if err := w.inner.Save(ctx, doc+"~deadbeef0001.open", []byte("clear")); err != nil {
		t.Fatal(err)
	}
	if err := w.inner.Save(ctx, doc+"~deadbeef0002.open", ageHeaderJunk(9)); err != nil {
		t.Fatal(err)
	}
	before := w.snapshot(t)
	sh := w.over(w.inner, func(a *Adapter) { a.AllowPlaintext = true })
	rep, err := sh.RepairAll(ctx, RepairAllOptions{EncryptPlaintext: true})
	if err != nil || skippedBy(rep)[doc] != SkipUnreadable || rep.Encrypted != 0 {
		t.Fatalf("%v %s", err, scanDigest(rep))
	}
	sameStore(t, "unreadable sibling", before, w.snapshot(t))
}

func TestRepairAllCursorWalksTheStoreInSlices(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	var items []string
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("passkey/item%02d", i)
		w.legacyItem(t, id, []byte("x"))
		items = append(items, id)
	}
	for i := 0; i < 3; i++ {
		if err := w.ad.Save(ctx, fmt.Sprintf("public/signed%d", i), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.inner.Save(ctx, "collab:junk", ageHeaderJunk(1)); err != nil {
		t.Fatal(err)
	}
	full, err := w.sharing.RepairAll(ctx, RepairAllOptions{DryRun: true})
	if err != nil || len(full.Todo) != 10 || full.Signed != 3 || len(full.Skipped) != 1 || full.Next != "" {
		t.Fatalf("%v %s next %q", err, scanDigest(full), full.Next)
	}

	var seen []string
	var sum RepairAllReport
	after := ""
	slices := 0
	for {
		rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{Limit: 3, After: after, Progress: func(p RepairProgress) { seen = append(seen, p.Doc) }})
		if err != nil {
			t.Fatal(err)
		}
		slices++
		sum.Repaired += rep.Repaired
		sum.Signed += rep.Signed
		sum.Files += rep.Files
		sum.Skipped = append(sum.Skipped, rep.Skipped...)
		if rep.Total != rep.Signed+rep.Repaired+len(rep.Skipped)+rep.Remaining {
			t.Errorf("slice %d does not add up: %s", slices, scanDigest(rep))
		}
		if rep.Finished != (rep.Next == "") {
			t.Errorf("finished %v with next %q", rep.Finished, rep.Next)
		}
		if rep.Next == "" {
			if rep.Remaining != 0 {
				t.Errorf("finished with %d remaining", rep.Remaining)
			}
			break
		}
		if rep.Next <= after && after != "" {
			t.Fatalf("the cursor did not advance: %q after %q", rep.Next, after)
		}
		if rep.Remaining == 0 {
			t.Errorf("a cursor %q and nothing remaining", rep.Next)
		}
		after = rep.Next
		if slices > 20 {
			t.Fatal("does not finish")
		}
	}
	if len(seen) != full.Total || !sort.StringsAreSorted(seen) {
		t.Fatalf("seen %d of %d: %v", len(seen), full.Total, seen)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] == seen[i-1] {
			t.Errorf("%s twice", seen[i])
		}
	}
	if sum.Repaired != len(full.Todo) || sum.Signed != full.Signed || len(sum.Skipped) != len(full.Skipped) {
		t.Errorf("slices sum to %s, a full run to %s", scanDigest(sum), scanDigest(full))
	}
	if slices < 4 {
		t.Errorf("%d slices", slices)
	}
	for _, id := range items {
		if v := w.frameVersion(t, id); v != 3 {
			t.Errorf("%s is v%d", id, v)
		}
	}
}

func TestRepairAllCursorEdges(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := w.history(t)
	w.legacyV2(t, doc)
	ids := []string{"collab:a", "expiry:b", "passkey/c", "public/d"}
	for _, id := range ids {
		w.legacyItem(t, id, []byte("x"))
	}
	groups := append([]string{doc}, ids...)
	sort.Strings(groups)
	run := func(after string) (RepairAllReport, []string) {
		var seen []string
		rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{DryRun: true, After: after, Progress: func(p RepairProgress) { seen = append(seen, p.Doc) }})
		if err != nil {
			t.Fatal(err)
		}
		return rep, seen
	}
	for _, after := range []string{groups[len(groups)-1], "zzzz", groups[len(groups)-1] + "~"} {
		rep, seen := run(after)
		if len(seen) != 0 || rep.Total != 0 || rep.Next != "" || rep.Left() != 0 {
			t.Errorf("After %q: %v %s next %q", after, seen, scanDigest(rep), rep.Next)
		}
	}
	rep, seen := run("")
	if len(seen) != len(groups) || rep.Total != len(groups) {
		t.Fatalf("%v", seen)
	}
	for i := range groups {
		rep, seen := run(groups[i])
		if want := groups[i+1:]; strings.Join(seen, ",") != strings.Join(want, ",") || rep.Total != len(want) {
			t.Errorf("After %q: %v, want %v", groups[i], seen, want)
		}
		_, seen = run(groups[i] + "\x00")
		if want := groups[i+1:]; strings.Join(seen, ",") != strings.Join(want, ",") {
			t.Errorf("After %q+NUL: %v, want %v", groups[i], seen, want)
		}
	}
	var file string
	for _, f := range w.files(t, doc) {
		file = f
	}
	for i, g := range groups {
		if g == doc {
			_, seen := run(file)
			if want := groups[i+1:]; strings.Join(seen, ",") != strings.Join(want, ",") {
				t.Errorf("After %q: %v, want %v", file, seen, want)
			}
		}
	}
	ctxc, cancel := context.WithCancel(ctx)
	n := 0
	rep, err := w.sharing.RepairAll(ctxc, RepairAllOptions{DryRun: true, Progress: func(RepairProgress) {
		if n++; n == 2 {
			cancel()
		}
	}})
	if err == nil || rep.Next != groups[1] || rep.Remaining != len(groups)-2 || rep.Finished {
		t.Errorf("cut short: %v next %q remaining %d finished %v", err, rep.Next, rep.Remaining, rep.Finished)
	}
	dead, stop := context.WithCancel(ctx)
	stop()
	early, err := w.sharing.RepairAll(dead, RepairAllOptions{DryRun: true})
	if !errors.Is(err, context.Canceled) || early.Next != "" || early.Finished || early.Remaining != len(groups) || early.Left() == 0 {
		t.Errorf("cancelled first: %v next %q finished %v remaining %d", err, early.Next, early.Finished, early.Remaining)
	}
	early, err = w.sharing.RepairAll(dead, RepairAllOptions{DryRun: true, After: groups[1]})
	if !errors.Is(err, context.Canceled) || early.Next != groups[1] || early.Finished || early.Remaining != len(groups)-2 {
		t.Errorf("cancelled first after a cursor: %v next %q finished %v remaining %d", err, early.Next, early.Finished, early.Remaining)
	}
	rest, seen := run(rep.Next)
	if strings.Join(seen, ",") != strings.Join(groups[2:], ",") || rest.Next != "" {
		t.Errorf("resumed: %v", seen)
	}
	sh := w.sharing
	last, err := sh.RepairAll(ctx, RepairAllOptions{DryRun: true, Limit: len(groups)})
	if err != nil || last.Next != "" || last.Remaining != 0 || !last.Finished {
		t.Errorf("%v next %q remaining %d finished %v", err, last.Next, last.Remaining, last.Finished)
	}
}
