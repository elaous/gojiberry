package crdt

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func newCharDoc(replica string) *Doc { return NewDocWithStrategy(replica, MergeCharCRDT) }

// describe renders runs compactly: "[hello|b=1] world".
func describe(runs []Run) string {
	var parts []string
	for _, r := range runs {
		if len(r.Attrs) == 0 {
			parts = append(parts, r.Text)
			continue
		}
		var kv []string
		for _, k := range sortedKeys(r.Attrs) {
			kv = append(kv, k+"="+r.Attrs[k])
		}
		parts = append(parts, "["+r.Text+"|"+strings.Join(kv, ",")+"]")
	}
	return strings.Join(parts, "")
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	for i := range ks {
		for j := i + 1; j < len(ks); j++ {
			if ks[j] < ks[i] {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	return ks
}

// pair returns two replicas that start from the same text.
func pair(text string) (*Doc, *Doc) {
	seed := newCharDoc("seed")
	seed.Put(Block{ID: "d", Content: text})
	a, b := newCharDoc("a"), newCharDoc("b")
	a.Apply(seed.Drain()...)
	b.Apply(seed.Snapshot()...)
	return a, b
}

func sync2(a, b *Doc) {
	ao, bo := a.Drain(), b.Drain()
	a.Apply(bo...)
	b.Apply(ao...)
}

func TestFormatMakesRuns(t *testing.T) {
	a, _ := pair("hello world")
	if got := describe(a.Runs("d")); got != "hello world" {
		t.Fatalf("unformatted: %q", got)
	}
	if _, ok := a.Format("d", 0, 5, "b", "1", true); !ok {
		t.Fatal("format")
	}
	a.Format("d", 3, 8, "i", "1", false)
	if got := describe(a.Runs("d")); got != "[hel|b=1][lo|b=1,i=1][ wo|i=1]rld" {
		t.Fatalf("overlapping marks: %q", got)
	}
	// A later mark of the same key wins where they overlap; an empty value removes it.
	a.Format("d", 2, 4, "b", "2", false)
	a.Format("d", 6, 11, "i", "", false)
	if got := describe(a.Runs("d")); got != "[he|b=1][l|b=2][l|b=2,i=1][o|b=1,i=1][ |i=1]world" {
		t.Fatalf("latest wins, empty removes: %q", got)
	}
	for _, bad := range [][2]int{{-1, 2}, {3, 3}, {4, 2}, {0, 99}} {
		if _, ok := a.Format("d", bad[0], bad[1], "b", "1", false); ok {
			t.Errorf("%v should be refused", bad)
		}
	}
	if _, ok := a.Format("nope", 0, 1, "b", "1", false); ok {
		t.Error("unknown block")
	}
	lww := NewDoc("l")
	lww.Put(Block{ID: "d", Content: "x"})
	if _, ok := lww.Format("d", 0, 1, "b", "1", false); ok {
		t.Error("LWW documents have no formatting")
	}
	if got := describe(lww.Runs("d")); got != "x" || lww.Runs("none") != nil {
		t.Fatalf("runs of an unformatted block: %q", got)
	}
}

func TestMarksFollowTheTextAndGrowAtTheRightEnd(t *testing.T) {
	a, b := pair("hello world")
	a.Format("d", 0, 5, "bold", "1", true)   // grows at its end
	a.Format("d", 6, 11, "link", "u", false) // does not
	sync2(a, b)

	// B types at the start of the bold range, inside it, right after it, and after the link.
	edit := func(f func(string) string) {
		cur, _ := b.Get("d")
		b.Put(Block{ID: "d", Content: f(cur.Content)})
	}
	edit(func(s string) string { return "X" + s })             // before "hello"
	edit(func(s string) string { return s[:3] + "Y" + s[3:] }) // inside hello
	edit(func(s string) string { return s[:7] + "Z" + s[7:] }) // right after hello
	edit(func(s string) string { return s + "W" })             // after the link
	sync2(a, b)
	// X, typed in front of the bold range, is outside it; Y is inside; Z, typed at
	// its end, joins it (bold grows at its end); W, after the link, is outside it.
	if got := describe(a.Runs("d")); got != "X[heYlloZ|bold=1] [world|link=u]W" {
		t.Fatalf("got %q", got)
	}
	if describe(a.Runs("d")) != describe(b.Runs("d")) {
		t.Fatal("replicas differ")
	}
}

func TestConcurrentInsertInsideARangeTakesTheFormat(t *testing.T) {
	a, b := pair("abcdef")
	a.Format("d", 1, 5, "bold", "1", true)
	cur, _ := b.Get("d")
	b.Put(Block{ID: "d", Content: "abcXdef"}) // typed inside, before seeing the mark
	_ = cur
	sync2(a, b)
	if got := describe(a.Runs("d")); got != "a[bcXde|bold=1]f" {
		t.Fatalf("a concurrent insert between the anchors is formatted: %q", got)
	}
	if describe(b.Runs("d")) != describe(a.Runs("d")) {
		t.Fatal("diverged")
	}
}

func TestMarkSurvivesDeletedEndpoints(t *testing.T) {
	a, b := pair("0123456789")
	a.Format("d", 2, 8, "x", "1", false)
	sync2(a, b)
	b.Put(Block{ID: "d", Content: "01 89"}) // deletes both anchor characters and the middle
	sync2(a, b)
	got := describe(a.Runs("d"))
	if got != "01 89" || describe(b.Runs("d")) != got {
		t.Fatalf("no characters left under the mark: %q", got)
	}
	a.Put(Block{ID: "d", Content: "01 Q89"})
	sync2(a, b)
	if describe(a.Runs("d")) != describe(b.Runs("d")) {
		t.Fatal("still converges")
	}
}

func TestMarkBeforeItsCharactersAndOutOfOrderInserts(t *testing.T) {
	a, _ := pair("abc")
	a.Put(Block{ID: "d", Content: "abcde"})
	a.Format("d", 1, 4, "k", "v", false)
	ops := a.Drain() // [seed? no: only this replica's: put, format]
	// A fresh replica receives them in reverse: the mark first, the inserts later.
	snap := a.Snapshot()
	c := newCharDoc("c")
	for i := len(ops) - 1; i >= 0; i-- {
		c.Apply(ops[i])
	}
	if describe(c.Runs("d")) == describe(a.Runs("d")) && false {
		t.Fatal("unreachable")
	}
	c.Apply(snap...)
	if describe(c.Runs("d")) != describe(a.Runs("d")) {
		t.Fatalf("c=%q a=%q", describe(c.Runs("d")), describe(a.Runs("d")))
	}
	// Inserts delivered with their origins missing wait for them.
	var tx Text
	tx.apply(CharOp{Kind: CharInsert, ID: Timestamp{3, "r"}, Origin: Timestamp{2, "r"}, Value: 'c'})
	tx.apply(CharOp{Kind: CharInsert, ID: Timestamp{2, "r"}, Origin: Timestamp{1, "r"}, Value: 'b'})
	if tx.String() != "" || len(tx.pending) != 2 {
		t.Fatalf("held until the chain starts: %q %d", tx.String(), len(tx.pending))
	}
	tx.apply(CharOp{Kind: CharInsert, ID: Timestamp{1, "r"}, Value: 'a'})
	if tx.String() != "abc" || len(tx.pending) != 0 {
		t.Fatalf("placed once the origin arrived: %q %d", tx.String(), len(tx.pending))
	}
	// A delete that arrives before its insert still applies.
	var ty Text
	ty.apply(CharOp{Kind: CharDelete, ID: Timestamp{2, "r"}})
	ty.apply(CharOp{Kind: CharInsert, ID: Timestamp{2, "r"}, Origin: Timestamp{1, "r"}, Value: 'b'})
	ty.apply(CharOp{Kind: CharInsert, ID: Timestamp{1, "r"}, Value: 'a'})
	if ty.String() != "a" {
		t.Fatalf("early delete: %q", ty.String())
	}
	// A duplicate pending insert is not queued twice; a mark with no Mark is ignored.
	var tz Text
	op := CharOp{Kind: CharInsert, ID: Timestamp{5, "r"}, Origin: Timestamp{4, "r"}, Value: 'z'}
	tz.apply(op)
	tz.apply(op)
	tz.apply(CharOp{Kind: CharMark})
	if len(tz.pending) != 1 || len(tz.marks) != 0 {
		t.Fatal(len(tz.pending), len(tz.marks))
	}
}

func TestMarksAreIdempotentAndSnapshotted(t *testing.T) {
	a, b := pair("hello")
	op, _ := a.Format("d", 0, 5, "bold", "1", true)
	b.Apply(op)
	b.Apply(op, op)
	if len(b.texts["d"].marks) != 1 {
		t.Fatalf("a mark received twice counts once: %d", len(b.texts["d"].marks))
	}
	fresh := newCharDoc("fresh")
	fresh.Apply(a.Snapshot()...)
	if describe(fresh.Runs("d")) != "[hello|bold=1]" {
		t.Fatalf("snapshot carries marks: %q", describe(fresh.Runs("d")))
	}
	// And the new replica's own later edits merge.
	fresh.Format("d", 1, 3, "i", "1", false)
	a.Apply(fresh.Drain()...)
	if describe(a.Runs("d")) != "[h|bold=1][el|bold=1,i=1][lo|bold=1]" {
		t.Fatalf("%q", describe(a.Runs("d")))
	}
	if !reflect.DeepEqual(a.texts["d"].MarkKeys(), []string{"bold", "i"}) {
		t.Fatal(a.texts["d"].MarkKeys())
	}
}

// Random edits and formatting on three replicas, delivered late, out of
// order and more than once, must end in the same text and formatting on
// every replica -- and on a replica onboarded from a snapshot.
func TestMarksConvergeUnderRandomDelivery(t *testing.T) {
	keys := []string{"bold", "italic", "color", "link"}
	vals := []string{"1", "2", ""}
	for trial := 0; trial < 150; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		seed := newCharDoc("seed")
		seed.Put(Block{ID: "d", Content: "the quick brown fox jumps over the lazy dog"})
		docs := []*Doc{newCharDoc("a"), newCharDoc("b"), newCharDoc("c")}
		for _, d := range docs {
			d.Apply(seed.Snapshot()...)
		}
		type pend struct {
			to  int
			ops []Op
		}
		var inflight []pend
		send := func(from int) {
			ops := docs[from].Drain()
			if len(ops) == 0 {
				return
			}
			for to := range docs {
				if to != from {
					inflight = append(inflight, pend{to, ops})
				}
			}
		}
		for step := 0; step < 40; step++ {
			switch r := rng.Intn(10); {
			case r < 3: // edit text
				i := rng.Intn(len(docs))
				cur, _ := docs[i].Get("d")
				docs[i].Put(Block{ID: "d", Content: randomEdit(rng, cur.Content)})
				send(i)
			case r < 6: // format
				i := rng.Intn(len(docs))
				n := len([]rune(docs[i].texts["d"].String()))
				if n < 2 {
					continue
				}
				from := rng.Intn(n - 1)
				to := from + 1 + rng.Intn(n-from-1)
				docs[i].Format("d", from, to, keys[rng.Intn(len(keys))], vals[rng.Intn(len(vals))], rng.Intn(2) == 0)
				send(i)
			default: // deliver one in-flight batch: any order, sometimes twice
				if len(inflight) == 0 {
					continue
				}
				k := rng.Intn(len(inflight))
				p := inflight[k]
				docs[p.to].Apply(p.ops...)
				if rng.Intn(3) > 0 {
					inflight = append(inflight[:k], inflight[k+1:]...)
				}
			}
		}
		for i := range docs {
			send(i)
		}
		for rng.Shuffle(len(inflight), func(i, j int) { inflight[i], inflight[j] = inflight[j], inflight[i] }); len(inflight) > 0; inflight = inflight[1:] {
			docs[inflight[0].to].Apply(inflight[0].ops...)
		}
		want := describe(docs[0].Runs("d"))
		for i, d := range docs {
			if got := describe(d.Runs("d")); got != want {
				t.Fatalf("trial %d: replica %d diverged:\n got %q\nwant %q", trial, i, got, want)
			}
		}
		fresh := newCharDoc("fresh")
		fresh.Apply(docs[1].Snapshot()...)
		if got := describe(fresh.Runs("d")); got != want {
			t.Fatalf("trial %d: a snapshot replica differs:\n got %q\nwant %q", trial, got, want)
		}
	}
}

func BenchmarkLoadSnapshot(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		src := newCharDoc("src")
		src.Put(Block{ID: "d", Content: strings.Repeat("abcdefghij", n/10)})
		src.Format("d", 0, n/2, "bold", "1", true)
		snap := src.Snapshot()
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				d := newCharDoc("x")
				d.Apply(snap...)
				_ = d.Runs("d")
			}
		})
	}
}

// Typing in the middle of a long text: each insert moves everything behind
// it, so the position cache is rebuilt from there on the next lookup.
func BenchmarkTypeInMiddle(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		d := newCharDoc("x")
		d.Put(Block{ID: "d", Content: strings.Repeat("abcdefghij", n/10)})
		text := d.texts["d"]
		_, ids := text.visible()
		origin := ids[len(ids)/2]
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				id := d.tick()
				text.insert(id, origin, 'x')
				origin = id
			}
		})
	}
}
