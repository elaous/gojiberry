package crdt

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func newCharDoc(replica string) *Doc { return NewDocWithStrategy(replica, MergeCharCRDT) }

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

	edit := func(f func(string) string) {
		cur, _ := b.Get("d")
		b.Put(Block{ID: "d", Content: f(cur.Content)})
	}
	edit(func(s string) string { return "X" + s })             // before "hello"
	edit(func(s string) string { return s[:3] + "Y" + s[3:] }) // inside hello
	edit(func(s string) string { return s[:7] + "Z" + s[7:] }) // right after hello
	edit(func(s string) string { return s + "W" })             // after the link
	sync2(a, b)
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
	b.Put(Block{ID: "d", Content: "01 89"})
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
	ops := a.Drain()
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
	var ty Text
	ty.apply(CharOp{Kind: CharDelete, ID: Timestamp{2, "r"}})
	ty.apply(CharOp{Kind: CharInsert, ID: Timestamp{2, "r"}, Origin: Timestamp{1, "r"}, Value: 'b'})
	ty.apply(CharOp{Kind: CharInsert, ID: Timestamp{1, "r"}, Value: 'a'})
	if ty.String() != "a" {
		t.Fatalf("early delete: %q", ty.String())
	}
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
	fresh.Format("d", 1, 3, "i", "1", false)
	a.Apply(fresh.Drain()...)
	if describe(a.Runs("d")) != "[h|bold=1][el|bold=1,i=1][lo|bold=1]" {
		t.Fatalf("%q", describe(a.Runs("d")))
	}
	if !reflect.DeepEqual(a.texts["d"].MarkKeys(), []string{"bold", "i"}) {
		t.Fatal(a.texts["d"].MarkKeys())
	}
}

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
			default:
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

func TestSpliceAndPositions(t *testing.T) {
	a, b := pair("hello world")
	if _, ok := a.Splice("d", 6, 5, []rune("there")); !ok {
		t.Fatal("splice")
	}
	if cur, _ := a.Get("d"); cur.Content != "hello there" {
		t.Fatal(cur.Content)
	}
	b.Splice("d", 0, 0, []rune(">> "))
	sync2(a, b)
	got, _ := a.Get("d")
	other, _ := b.Get("d")
	if got.Content != ">> hello there" || other.Content != got.Content {
		t.Fatalf("%q %q", got.Content, other.Content)
	}
	for _, bad := range [][2]int{{-1, 0}, {0, -1}, {5, 99}, {99, 0}} {
		if _, ok := a.Splice("d", bad[0], bad[1], nil); ok {
			t.Errorf("%v should be refused", bad)
		}
	}
	lww := NewDoc("l")
	if _, ok := lww.Splice("d", 0, 0, []rune("x")); ok {
		t.Fatal("LWW has no splice")
	}
	c := newCharDoc("c")
	c.Splice("new", 0, 0, []rune("hi"))
	if cur, _ := c.Get("new"); cur.Content != "hi" {
		t.Fatal(cur)
	}
	ids := a.VisibleIDs("d")
	if len(ids) != len(">> hello there") {
		t.Fatal(len(ids))
	}
	anchor := ids[5] // 'l' of "hello"... position after it is 6
	if p, ok := a.PositionAfter("d", anchor); !ok || p != 6 {
		t.Fatalf("%d %v", p, ok)
	}
	a.Splice("d", 0, 3, nil) // remove ">> "
	if p, _ := a.PositionAfter("d", anchor); p != 3 {
		t.Fatalf("moved with the text: %d", p)
	}
	a.Splice("d", 2, 1, nil) // delete the anchor character itself
	if p, ok := a.PositionAfter("d", anchor); !ok || p != 2 {
		t.Fatalf("deleted anchor keeps its place: %d %v", p, ok)
	}
	if _, ok := a.PositionAfter("d", Timestamp{999, "zz"}); ok {
		t.Fatal("unknown character")
	}
	if _, ok := a.PositionAfter("nope", anchor); ok || a.VisibleIDs("nope") != nil {
		t.Fatal("unknown block")
	}
}

func TestLWWBlocksInACharDoc(t *testing.T) {
	a, b := newCharDoc("a"), newCharDoc("b")
	a.Put(Block{ID: "comment:1", Kind: "lww:side", Content: `{"text":"one"}`})
	sync2(a, b)
	a.Put(Block{ID: "comment:1", Kind: "lww:side", Content: `{"text":"from a"}`})
	b.Put(Block{ID: "comment:1", Kind: "lww:side", Content: `{"text":"from b!"}`})
	sync2(a, b)
	ga, _ := a.Get("comment:1")
	gb, _ := b.Get("comment:1")
	if ga.Content != gb.Content || (ga.Content != `{"text":"from a"}` && ga.Content != `{"text":"from b!"}`) {
		t.Fatalf("a whole value won: %q %q", ga.Content, gb.Content)
	}
	a.Delete("comment:1")
	sync2(a, b)
	if _, ok := b.Get("comment:1"); !ok {
		t.Fatal("tombstone kept")
	}
	for _, blk := range b.Blocks() {
		if blk.ID == "comment:1" {
			t.Fatal("deleted block listed")
		}
	}
	a.Put(Block{ID: "style:Loud", Kind: "lww:side", Content: "x"})
	c := newCharDoc("c")
	c.Apply(a.Snapshot()...)
	if g, ok := c.Get("style:Loud"); !ok || g.Content != "x" {
		t.Fatal(g)
	}
	if !LWWKind("lww") || LWWKind("para") {
		t.Fatal("LWWKind")
	}
}
