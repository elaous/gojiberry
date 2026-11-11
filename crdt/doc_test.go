package crdt

import "testing"

// syncOnce exchanges each doc's pending outbox with the other.
func syncOnce(a, b *Doc) {
	aOps := a.Drain()
	bOps := b.Drain()
	a.Apply(bOps...)
	b.Apply(aOps...)
}

func TestDisjointEditsConverge(t *testing.T) {
	phone := NewDoc("phone")
	laptop := NewDoc("laptop")

	phone.Put(Block{ID: "1", Kind: "heading", Content: "Fatura #42", Order: 0})
	laptop.Put(Block{ID: "2", Kind: "line-item", Content: "Aula de inglês - R$80", Order: 1})

	syncOnce(phone, laptop)

	wantIDs := map[string]bool{"1": true, "2": true}
	for _, doc := range []*Doc{phone, laptop} {
		if len(doc.Blocks()) != 2 {
			t.Fatalf("expected 2 blocks after sync, got %d", len(doc.Blocks()))
		}
		for _, b := range doc.Blocks() {
			if !wantIDs[b.ID] {
				t.Fatalf("unexpected block %q", b.ID)
			}
		}
	}
}

func TestConcurrentEditSameBlockConvergesRegardlessOfOrder(t *testing.T) {
	seed := NewDoc("seed")
	seed.Put(Block{ID: "note", Kind: "paragraph", Content: "rascunho", Order: 0})
	seedOp := seed.Drain()[0]

	phone := NewDoc("phone")
	tablet := NewDoc("tablet")
	phone.Apply(seedOp)
	tablet.Apply(seedOp)

	phone.Put(Block{ID: "note", Kind: "paragraph", Content: "versão do celular", Order: 0})
	tablet.Put(Block{ID: "note", Kind: "paragraph", Content: "versão do tablet", Order: 0})

	phoneOps := phone.Drain()
	tabletOps := tablet.Drain()

	phone.Apply(tabletOps...)
	tablet.Apply(phoneOps...)

	got, ok := phone.Get("note")
	if !ok {
		t.Fatal("block missing after merge")
	}
	want, _ := tablet.Get("note")
	if got != want {
		t.Fatalf("replicas diverged: phone=%+v tablet=%+v", got, want)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	a := NewDoc("a")
	op := a.Put(Block{ID: "x", Kind: "paragraph", Content: "once", Order: 0})
	a.Drain()

	before, _ := a.Get("x")
	a.Apply(op)
	a.Apply(op)
	a.Apply(op)
	after, _ := a.Get("x")

	if before != after {
		t.Fatalf("re-applying the same op mutated state: before=%+v after=%+v", before, after)
	}
}

func TestDeleteWinsWhenLater(t *testing.T) {
	a := NewDoc("a")
	b := NewDoc("b")

	seedOp := a.Put(Block{ID: "1", Kind: "paragraph", Content: "keep me?", Order: 0})
	b.Apply(seedOp)
	a.Drain()

	delOp := a.Delete("1")
	a.Drain()

	b.Apply(delOp)

	if _, ok := b.Get("1"); ok {
		if blk, _ := b.Get("1"); !blk.Deleted {
			t.Fatalf("expected block to be tombstoned, got %+v", blk)
		}
	}
	for _, blk := range b.Blocks() {
		if blk.ID == "1" {
			t.Fatalf("deleted block %q still visible in Blocks()", blk.ID)
		}
	}
}

func TestBlocksRenderInOrder(t *testing.T) {
	d := NewDoc("solo")
	d.Put(Block{ID: "c", Content: "third", Order: 2})
	d.Put(Block{ID: "a", Content: "first", Order: 0})
	d.Put(Block{ID: "b", Content: "second", Order: 1})

	blocks := d.Blocks()
	wantOrder := []string{"a", "b", "c"}
	for i, id := range wantOrder {
		if blocks[i].ID != id {
			t.Fatalf("position %d: got %q, want %q", i, blocks[i].ID, id)
		}
	}
}

func TestOrderBetweenInsertsWithoutRenumbering(t *testing.T) {
	d := NewDoc("solo")
	d.Put(Block{ID: "a", Content: "first", Order: 0})
	d.Put(Block{ID: "b", Content: "second", Order: 1})

	mid := OrderBetween(0, 1)
	d.Put(Block{ID: "mid", Content: "inserted", Order: mid})

	blocks := d.Blocks()
	wantOrder := []string{"a", "mid", "b"}
	for i, id := range wantOrder {
		if blocks[i].ID != id {
			t.Fatalf("position %d: got %q, want %q", i, blocks[i].ID, id)
		}
	}
}

func TestOutboxOnlyCarriesTheDelta(t *testing.T) {
	d := NewDoc("solo")
	d.Put(Block{ID: "a", Content: "first", Order: 0})
	d.Drain()

	d.Put(Block{ID: "b", Content: "second", Order: 1})
	pending := d.Outbox()
	if len(pending) != 1 {
		t.Fatalf("expected only the un-synced edit in the outbox, got %d ops", len(pending))
	}
	if pending[0].Block.ID != "b" {
		t.Fatalf("expected pending op for block %q, got %q", "b", pending[0].Block.ID)
	}
}
