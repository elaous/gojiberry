package crdt

import "strings"

// Doc is one replica's view of a block document.
type Doc struct {
	replica  string
	clock    uint64
	blocks   map[string]stamped
	outbox   []Op
	strategy MergeStrategy
	// nil unless MergeCharCRDT; nil-map reads are fine
	texts map[string]*Text
}

type stamped struct {
	block Block
	ts    Timestamp
}

// NewDoc creates an empty document for a replica.
func NewDoc(replica string) *Doc {
	return NewDocWithStrategy(replica, MergeLWW)
}

// NewDocWithStrategy creates an empty document with a merge strategy.
func NewDocWithStrategy(replica string, strategy MergeStrategy) *Doc {
	return &Doc{replica: replica, blocks: make(map[string]stamped), strategy: strategy, texts: make(map[string]*Text)}
}

// Strategy reports the MergeStrategy this document uses.
func (d *Doc) Strategy() MergeStrategy {
	return d.strategy
}

// Clock is the highest Lamport counter seen.
func (d *Doc) Clock() uint64 { return d.clock }

func (d *Doc) tick() Timestamp {
	d.clock++
	return Timestamp{Counter: d.clock, Replica: d.replica}
}

func (d *Doc) observe(ts Timestamp) {
	if ts.Counter > d.clock {
		d.clock = ts.Counter
	}
}

// Put creates or edits a block: content, kind, or position.
func (d *Doc) Put(b Block) Op {
	if d.strategy == MergeCharCRDT && !LWWKind(b.Kind) {
		return d.putCharCRDT(b)
	}
	return d.putLWW(b)
}

// LWWKindPrefix starts the Kind of blocks that merge last-writer-wins.
const LWWKindPrefix = "lww"

// LWWKind reports whether blocks of this kind merge last-writer-wins.
func LWWKind(kind string) bool { return strings.HasPrefix(kind, LWWKindPrefix) }

func (d *Doc) putLWW(b Block) Op {
	ts := d.tick()
	op := Op{Block: b, TS: ts}
	d.blocks[b.ID] = stamped{block: b, ts: ts}
	d.outbox = append(d.outbox, op)
	return op
}

func (d *Doc) putCharCRDT(b Block) Op {
	ts := d.tick()
	text := d.textFor(b.ID)
	old, oldIDs := text.visible()
	new := []rune(b.Content)

	var charOps []CharOp
	lastID := Timestamp{} // zero value: "start of text"
	for _, e := range diffRunes(old, new) {
		switch e.kind {
		case editKeep:
			lastID = oldIDs[e.oldIndex]
		case editDelete:
			id := oldIDs[e.oldIndex]
			text.delete(id)
			charOps = append(charOps, CharOp{Kind: CharDelete, ID: id})
		case editInsert:
			id := d.tick()
			text.insert(id, lastID, e.r)
			charOps = append(charOps, CharOp{Kind: CharInsert, ID: id, Origin: lastID, Value: e.r})
			lastID = id
		}
	}

	merged := b
	merged.Content = text.String()
	op := Op{Block: merged, TS: ts, CharOps: charOps}
	d.blocks[b.ID] = stamped{block: merged, ts: ts}
	d.outbox = append(d.outbox, op)
	return op
}

// Format marks the visible characters [from, to) of block id.
func (d *Doc) Format(id string, from, to int, key, value string, growEnd bool) (Op, bool) {
	if d.strategy != MergeCharCRDT {
		return Op{}, false
	}
	text, ok := d.texts[id]
	if !ok {
		return Op{}, false
	}
	start, end, ok := text.anchorsFor(from, to, growEnd)
	if !ok {
		return Op{}, false
	}
	ts := d.tick()
	m := Mark{ID: ts, Key: key, Value: value, Start: start, End: end}
	text.addMark(m)
	op := Op{Block: d.blocks[id].block, TS: ts, CharOps: []CharOp{{Kind: CharMark, Mark: &m}}}
	d.outbox = append(d.outbox, op)
	return op, true
}

func (d *Doc) Runs(id string) []Run {
	if text, ok := d.texts[id]; ok {
		return text.Runs()
	}
	if b, ok := d.blocks[id]; ok && b.block.Content != "" {
		return []Run{{Text: b.block.Content, Attrs: map[string]string{}}}
	}
	return nil
}

func (d *Doc) textFor(id string) *Text {
	t, ok := d.texts[id]
	if !ok {
		t = &Text{}
		d.texts[id] = t
	}
	return t
}

// Delete tombstones a block instead of removing it.
func (d *Doc) Delete(id string) Op {
	b := d.blocks[id].block
	b.ID = id
	b.Deleted = true
	ts := d.tick()
	op := Op{Block: b, TS: ts}
	d.blocks[id] = stamped{block: b, ts: ts}
	d.outbox = append(d.outbox, op)
	return op
}

// Apply merges received Ops into this document.
func (d *Doc) Apply(ops ...Op) {
	for _, op := range ops {
		d.observe(op.TS)
		if d.strategy == MergeCharCRDT && !LWWKind(op.Block.Kind) && !d.isLWWBlock(op.Block.ID) {
			d.applyCharCRDT(op)
			continue
		}
		d.applyLWW(op)
	}
}

func (d *Doc) isLWWBlock(id string) bool {
	s, ok := d.blocks[id]
	return ok && LWWKind(s.block.Kind)
}

func (d *Doc) applyLWW(op Op) {
	cur, ok := d.blocks[op.Block.ID]
	if !ok || op.TS.after(cur.ts) {
		d.blocks[op.Block.ID] = stamped{block: op.Block, ts: op.TS}
	}
}

func (d *Doc) applyCharCRDT(op Op) {
	text := d.textFor(op.Block.ID)
	for _, c := range op.CharOps {
		d.observe(c.ID)
		if c.Mark != nil {
			d.observe(c.Mark.ID)
		}
		text.apply(c)
	}

	cur, ok := d.blocks[op.Block.ID]
	merged := op.Block
	merged.Content = text.String()
	switch {
	case !ok || op.TS.after(cur.ts):
		d.blocks[op.Block.ID] = stamped{block: merged, ts: op.TS}
	case len(op.CharOps) > 0:
		cur.block.Content = merged.Content
		d.blocks[op.Block.ID] = cur
	}
}

// Outbox returns the Ops queued since the last Drain.
func (d *Doc) Outbox() []Op {
	return append([]Op(nil), d.outbox...)
}

// Drain returns and clears the pending outbox.
func (d *Doc) Drain() []Op {
	out := d.outbox
	d.outbox = nil
	return out
}

// Snapshot returns every block, including tombstones.
func (d *Doc) Snapshot() []Op {
	out := make([]Op, 0, len(d.blocks))
	for id, s := range d.blocks {
		op := Op{Block: s.block, TS: s.ts}
		if d.strategy == MergeCharCRDT {
			if text, ok := d.texts[id]; ok {
				op.CharOps = text.snapshotOps()
			}
		}
		out = append(out, op)
	}
	return out
}

// Blocks returns every non-deleted block, in render order.
func (d *Doc) Blocks() []Block {
	out := make([]Block, 0, len(d.blocks))
	for _, s := range d.blocks {
		if !s.block.Deleted {
			out = append(out, s.block)
		}
	}
	sortBlocks(out)
	return out
}

// Get returns a block by ID, tombstones included.
func (d *Doc) Get(id string) (Block, bool) {
	s, ok := d.blocks[id]
	return s.block, ok
}

func (d *Doc) Splice(id string, at, del int, ins []rune) (Op, bool) {
	if d.strategy != MergeCharCRDT {
		return Op{}, false
	}
	text := d.textFor(id)
	_, ids := text.visible()
	if at < 0 || del < 0 || at+del > len(ids) {
		return Op{}, false
	}
	ts := d.tick()
	var charOps []CharOp
	for _, cid := range ids[at : at+del] {
		text.delete(cid)
		charOps = append(charOps, CharOp{Kind: CharDelete, ID: cid})
	}
	origin := Timestamp{}
	if at > 0 {
		origin = ids[at-1]
	}
	for _, r := range ins {
		cid := d.tick()
		text.insert(cid, origin, r)
		charOps = append(charOps, CharOp{Kind: CharInsert, ID: cid, Origin: origin, Value: r})
		origin = cid
	}
	b := d.blocks[id].block
	b.ID = id
	b.Content = text.String()
	op := Op{Block: b, TS: ts, CharOps: charOps}
	d.blocks[id] = stamped{block: b, ts: ts}
	d.outbox = append(d.outbox, op)
	return op, true
}

// VisibleIDs returns the ids of block id's visible characters.
func (d *Doc) VisibleIDs(id string) []Timestamp {
	if text, ok := d.texts[id]; ok {
		_, ids := text.visible()
		return ids
	}
	return nil
}

func (d *Doc) PositionAfter(id string, cid Timestamp) (int, bool) {
	text, ok := d.texts[id]
	if !ok {
		return 0, false
	}
	i := text.indexOf(cid)
	if i < 0 {
		return 0, false
	}
	n := 0
	for _, c := range text.chars[:i+1] {
		if !c.deleted {
			n++
		}
	}
	return n, true
}
