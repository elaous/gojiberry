package crdt

// Doc is one replica's view of a block document: a phone editing
// entirely offline, a laptop, or a sync server. Every replica starts
// from NewDoc and converges with every other replica it ever
// exchanges Ops with, regardless of how long they were disconnected
// or in what order the Ops arrive.
type Doc struct {
	replica string
	clock   uint64
	blocks  map[string]stamped
	outbox  []Op
}

type stamped struct {
	block Block
	ts    Timestamp
}

// NewDoc creates an empty document for the given replica ID, which
// must be unique per device/session (e.g. a phone's install ID) so
// its writes can be told apart from every other replica's.
func NewDoc(replica string) *Doc {
	return &Doc{replica: replica, blocks: make(map[string]stamped)}
}

// tick advances this replica's Lamport clock and stamps a new write.
func (d *Doc) tick() Timestamp {
	d.clock++
	return Timestamp{Counter: d.clock, Replica: d.replica}
}

// observe advances the local clock past ts, the standard Lamport-clock
// rule for keeping a replica's future writes ordered after anything it
// has seen, whether written locally or received from a peer.
func (d *Doc) observe(ts Timestamp) {
	if ts.Counter > d.clock {
		d.clock = ts.Counter
	}
}

// Put creates or edits a block: content, kind, and/or position. Set
// b.Order via OrderBetween/OrderEnd to place it among siblings. The
// resulting Op is queued in the outbox for the next sync.
func (d *Doc) Put(b Block) Op {
	ts := d.tick()
	op := Op{Block: b, TS: ts}
	d.blocks[b.ID] = stamped{block: b, ts: ts}
	d.outbox = append(d.outbox, op)
	return op
}

// Delete tombstones a block rather than removing it outright, so a
// concurrent edit to the same block on another replica can still be
// compared by timestamp instead of silently resurrecting a deleted ID.
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

// Apply merges received Ops into this document. For each op, the
// existing block for that ID is replaced only if op's timestamp is
// later — so applying the same op twice, or applying a batch in any
// order, always lands on the same final state (idempotent, commutative,
// associative: the three properties that make this safe to run
// whenever a connection happens to be available).
func (d *Doc) Apply(ops ...Op) {
	for _, op := range ops {
		d.observe(op.TS)
		cur, ok := d.blocks[op.Block.ID]
		if !ok || op.TS.after(cur.ts) {
			d.blocks[op.Block.ID] = stamped{block: op.Block, ts: op.TS}
		}
	}
}

// Outbox returns the Ops queued since the last Drain, without
// clearing them.
func (d *Doc) Outbox() []Op {
	return append([]Op(nil), d.outbox...)
}

// Drain returns and clears the pending outbox — call this exactly
// when a network connection becomes available, and send only what it
// returns; that's the entire delta-sync payload, however long the
// device was offline.
func (d *Doc) Drain() []Op {
	out := d.outbox
	d.outbox = nil
	return out
}

// Snapshot returns every block this document currently holds
// (including tombstones), stamped with its original write timestamp —
// the full state, for onboarding a brand-new replica that has nothing
// to diff against yet. Ongoing sync after that only needs Drain's
// delta.
func (d *Doc) Snapshot() []Op {
	out := make([]Op, 0, len(d.blocks))
	for _, s := range d.blocks {
		out = append(out, Op{Block: s.block, TS: s.ts})
	}
	return out
}

// Blocks returns every non-deleted block, ordered for rendering.
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

// Get returns a single block by ID (including tombstones) and whether
// it exists at all in this document.
func (d *Doc) Get(id string) (Block, bool) {
	s, ok := d.blocks[id]
	return s.block, ok
}
