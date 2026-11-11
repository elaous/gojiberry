package crdt

import (
	"math/rand"
	"testing"
)

func TestCharCRDTConcurrentEditsToDifferentPartsBothSurvive(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Kind: "paragraph", Content: "the quick fox", Order: 0})
	seedOp := seed.Drain()[0]

	phone := NewDocWithStrategy("phone", MergeCharCRDT)
	tablet := NewDocWithStrategy("tablet", MergeCharCRDT)
	phone.Apply(seedOp)
	tablet.Apply(seedOp)

	phone.Put(Block{ID: "note", Kind: "paragraph", Content: "well, the quick fox", Order: 0})
	tablet.Put(Block{ID: "note", Kind: "paragraph", Content: "the quick fox jumps", Order: 0})

	phoneOps := phone.Drain()
	tabletOps := tablet.Drain()
	phone.Apply(tabletOps...)
	tablet.Apply(phoneOps...)

	got, _ := phone.Get("note")
	want, _ := tablet.Get("note")
	if got.Content != want.Content {
		t.Fatalf("replicas diverged: phone=%q tablet=%q", got.Content, want.Content)
	}
	if got.Content != "well, the quick fox jumps" {
		t.Fatalf("expected both edits merged into %q, got %q", "well, the quick fox jumps", got.Content)
	}
}

func TestCharCRDTKnownInterleavingAnomalyForOverlappingReplacements(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Content: "hello world", Order: 0})
	seedOp := seed.Drain()[0]

	phone := NewDocWithStrategy("phone", MergeCharCRDT)
	tablet := NewDocWithStrategy("tablet", MergeCharCRDT)
	phone.Apply(seedOp)
	tablet.Apply(seedOp)

	phone.Put(Block{ID: "note", Content: "goodbye world", Order: 0})
	tablet.Put(Block{ID: "note", Content: "hello universe", Order: 0})

	phoneOps := phone.Drain()
	tabletOps := tablet.Drain()
	phone.Apply(tabletOps...)
	tablet.Apply(phoneOps...)

	got, _ := phone.Get("note")
	want, _ := tablet.Get("note")
	if got.Content != want.Content {
		t.Fatalf("replicas diverged even under the known anomaly -- that would be a real convergence bug: phone=%q tablet=%q", got.Content, want.Content)
	}
}

func TestCharCRDTConvergesRegardlessOfApplyOrder(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Content: "abcdef", Order: 0})
	seedOp := seed.Drain()[0]

	a := NewDocWithStrategy("a", MergeCharCRDT)
	b := NewDocWithStrategy("b", MergeCharCRDT)
	a.Apply(seedOp)
	b.Apply(seedOp)

	a.Put(Block{ID: "note", Content: "abXcdef", Order: 0}) // insert in the middle
	b.Put(Block{ID: "note", Content: "abcdYef", Order: 0}) // insert further along

	aOps := a.Drain()
	bOps := b.Drain()

	a.Apply(bOps...)
	b.Apply(aOps...)

	got, _ := a.Get("note")
	want, _ := b.Get("note")
	if got.Content != want.Content {
		t.Fatalf("replicas diverged depending on apply order: a=%q b=%q", got.Content, want.Content)
	}
}

func TestCharCRDTIsIdempotent(t *testing.T) {
	a := NewDocWithStrategy("a", MergeCharCRDT)
	op := a.Put(Block{ID: "x", Content: "once", Order: 0})
	a.Drain()

	before, _ := a.Get("x")
	a.Apply(op)
	a.Apply(op)
	a.Apply(op)
	after, _ := a.Get("x")

	if before.Content != after.Content {
		t.Fatalf("re-applying the same op mutated state: before=%q after=%q", before.Content, after.Content)
	}
}

func TestCharCRDTDeleteThenInsertConverges(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Content: "hello", Order: 0})
	seedOp := seed.Drain()[0]

	a := NewDocWithStrategy("a", MergeCharCRDT)
	b := NewDocWithStrategy("b", MergeCharCRDT)
	a.Apply(seedOp)
	b.Apply(seedOp)

	a.Put(Block{ID: "note", Content: "helloworld", Order: 0}) // pure append
	b.Put(Block{ID: "note", Content: "hell", Order: 0})       // pure trim

	aOps := a.Drain()
	bOps := b.Drain()
	a.Apply(bOps...)
	b.Apply(aOps...)

	got, _ := a.Get("note")
	want, _ := b.Get("note")
	if got.Content != want.Content {
		t.Fatalf("replicas diverged: a=%q b=%q", got.Content, want.Content)
	}
	if got.Content != "hellworld" {
		t.Fatalf("expected the trim and the append to both apply, got %q", got.Content)
	}
}

func TestCharCRDTHandlesUnicode(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Content: "inglês", Order: 0})
	seedOp := seed.Drain()[0]

	a := NewDocWithStrategy("a", MergeCharCRDT)
	b := NewDocWithStrategy("b", MergeCharCRDT)
	a.Apply(seedOp)
	b.Apply(seedOp)

	a.Put(Block{ID: "note", Content: "aulas de inglês", Order: 0})
	b.Put(Block{ID: "note", Content: "inglês básico", Order: 0})

	aOps := a.Drain()
	bOps := b.Drain()
	a.Apply(bOps...)
	b.Apply(aOps...)

	got, _ := a.Get("note")
	want, _ := b.Get("note")
	if got.Content != want.Content {
		t.Fatalf("replicas diverged over unicode content: a=%q b=%q", got.Content, want.Content)
	}
	if got.Content != "aulas de inglês básico" {
		t.Fatalf("expected both unicode edits merged, got %q", got.Content)
	}
}

func TestCharCRDTSnapshotOnboardsNewReplicaMergeably(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Content: "hello world", Order: 0})

	fresh := NewDocWithStrategy("new-device", MergeCharCRDT)
	fresh.Apply(seed.Snapshot()...)

	got, ok := fresh.Get("note")
	if !ok || got.Content != "hello world" {
		t.Fatalf("expected onboarded content %q, got %+v (ok=%v)", "hello world", got, ok)
	}

	seed.Put(Block{ID: "note", Content: "well, hello world", Order: 0})
	fresh.Put(Block{ID: "note", Content: "hello world indeed", Order: 0})

	seedOps := seed.Drain()
	freshOps := fresh.Drain()
	seed.Apply(freshOps...)
	fresh.Apply(seedOps...)

	got, _ = fresh.Get("note")
	want, _ := seed.Get("note")
	if got.Content != want.Content {
		t.Fatalf("onboarded replica diverged: fresh=%q seed=%q", got.Content, want.Content)
	}
	if got.Content != "well, hello world indeed" {
		t.Fatalf("expected both edits merged post-onboarding, got %q", got.Content)
	}
}

func TestCharCRDTSnapshotCarriesTombstones(t *testing.T) {
	seed := NewDocWithStrategy("seed", MergeCharCRDT)
	seed.Put(Block{ID: "note", Content: "hello world", Order: 0})
	seed.Put(Block{ID: "note", Content: "hello", Order: 0}) // deletes " world" at the char level

	fresh := NewDocWithStrategy("new-device", MergeCharCRDT)
	fresh.Apply(seed.Snapshot()...)

	got, ok := fresh.Get("note")
	if !ok || got.Content != "hello" {
		t.Fatalf("expected onboarded content %q, got %+v (ok=%v)", "hello", got, ok)
	}
}

func TestCharCRDTBlockLevelDeleteStillTombstonesWhole(t *testing.T) {
	a := NewDocWithStrategy("a", MergeCharCRDT)
	b := NewDocWithStrategy("b", MergeCharCRDT)

	seedOp := a.Put(Block{ID: "1", Content: "keep me?", Order: 0})
	b.Apply(seedOp)
	a.Drain()

	delOp := a.Delete("1")
	b.Apply(delOp)

	for _, blk := range b.Blocks() {
		if blk.ID == "1" {
			t.Fatalf("deleted block %q still visible in Blocks()", blk.ID)
		}
	}
}

func TestCharCRDTOutOfOrderDeleteBeforeInsertConverges(t *testing.T) {
	var text Text
	insertOp := CharOp{Kind: CharInsert, ID: Timestamp{Counter: 1, Replica: "a"}, Value: 'x'}
	deleteOp := CharOp{Kind: CharDelete, ID: Timestamp{Counter: 1, Replica: "a"}}

	text.apply(deleteOp)
	text.apply(insertOp)

	if got := text.String(); got != "" {
		t.Fatalf("expected the character to be created already-deleted, got %q", got)
	}
}

func TestDocStrategySwitch(t *testing.T) {
	lww := NewDoc("solo")
	if lww.Strategy() != MergeLWW {
		t.Fatalf("expected NewDoc's default strategy to be MergeLWW, got %v", lww.Strategy())
	}

	charCRDT := NewDocWithStrategy("solo", MergeCharCRDT)
	if charCRDT.Strategy() != MergeCharCRDT {
		t.Fatalf("expected NewDocWithStrategy(..., MergeCharCRDT) to report MergeCharCRDT, got %v", charCRDT.Strategy())
	}
}

func randomEdit(rng *rand.Rand, s string) string {
	runes := []rune(s)
	pos := rng.Intn(len(runes) + 1)
	if len(runes) > 0 && rng.Intn(2) == 0 {
		n := 1 + rng.Intn(3)
		end := pos + n
		if end > len(runes) {
			end = len(runes)
		}
		if pos > end {
			pos = end
		}
		out := append([]rune{}, runes[:pos]...)
		out = append(out, runes[end:]...)
		return string(out)
	}
	n := 1 + rng.Intn(4)
	ins := make([]rune, n)
	for i := range ins {
		ins[i] = rune('a' + rng.Intn(26))
	}
	out := append([]rune{}, runes[:pos]...)
	out = append(out, ins...)
	out = append(out, runes[pos:]...)
	return string(out)
}

func TestCharCRDTRandomizedConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for trial := 0; trial < 200; trial++ {
		seed := NewDocWithStrategy("seed", MergeCharCRDT)
		seed.Put(Block{ID: "note", Content: "the quick brown fox jumps over the lazy dog", Order: 0})
		seedOp := seed.Drain()[0]

		a := NewDocWithStrategy("a", MergeCharCRDT)
		b := NewDocWithStrategy("b", MergeCharCRDT)
		a.Apply(seedOp)
		b.Apply(seedOp)

		for round := 0; round < 6; round++ {
			aContent, _ := a.Get("note")
			bContent, _ := b.Get("note")

			a.Put(Block{ID: "note", Content: randomEdit(rng, aContent.Content), Order: 0})
			b.Put(Block{ID: "note", Content: randomEdit(rng, bContent.Content), Order: 0})

			aOps := a.Drain()
			bOps := b.Drain()
			a.Apply(bOps...)
			b.Apply(aOps...)

			got, _ := a.Get("note")
			want, _ := b.Get("note")
			if got.Content != want.Content {
				t.Fatalf("trial %d round %d: replicas diverged: a=%q b=%q", trial, round, got.Content, want.Content)
			}
		}
	}
}

func TestSnapshotKeepsDeletesThatArrivedBeforeTheirInsert(t *testing.T) {
	a := Timestamp{Counter: 1, Replica: "a"}
	b := Timestamp{Counter: 2, Replica: "a"}
	var text Text
	text.apply(CharOp{Kind: CharInsert, ID: a, Value: 'r'})
	z := Timestamp{Counter: 4, Replica: "a"}
	text.apply(CharOp{Kind: CharDelete, ID: z})
	text.apply(CharOp{Kind: CharInsert, ID: z, Origin: b, Value: 'z'}) // its origin b is missing: parked
	var copy Text
	for _, op := range text.snapshotOps() {
		copy.apply(op)
	}
	copy.apply(CharOp{Kind: CharInsert, ID: b, Origin: a, Value: 'q'}) // the origin finally arrives
	text.apply(CharOp{Kind: CharInsert, ID: b, Origin: a, Value: 'q'})
	if text.String() != copy.String() {
		t.Fatalf("direct apply gives %q, via snapshot %q", text.String(), copy.String())
	}
}
