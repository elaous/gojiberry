package crdt

import "strings"

// Doc is one replica's view of a block document: a phone editing
// entirely offline, a laptop, or a sync server. Every replica starts
// from NewDoc and converges with every other replica it ever
// exchanges Ops with, regardless of how long they were disconnected
// or in what order the Ops arrive.
type Doc struct {
	replica  string
	clock    uint64
	blocks   map[string]stamped
	outbox   []Op
	strategy MergeStrategy
	// texts holds each block's per-character CRDT state, keyed by
	// block ID. Only populated and consulted when strategy is
	// MergeCharCRDT; nil-map reads (len 0) are fine in MergeLWW mode
	// since it's never written to.
	texts map[string]*Text
}

type stamped struct {
	block Block
	ts    Timestamp
}

// NewDoc creates an empty document for the given replica ID, which
// must be unique per device/session (e.g. a phone's install ID) so
// its writes can be told apart from every other replica's. Resolves
// conflicting writes to the same block via MergeLWW; use
// NewDocWithStrategy for MergeCharCRDT.
func NewDoc(replica string) *Doc {
	return NewDocWithStrategy(replica, MergeLWW)
}

// NewDocWithStrategy creates an empty document like NewDoc, but lets
// the caller pick how Apply resolves a conflicting write to the same
// block's Content. Every replica syncing the same document must use
// the same MergeStrategy -- mixing them defeats MergeCharCRDT's
// per-character merging on whichever side runs MergeLWW instead.
func NewDocWithStrategy(replica string, strategy MergeStrategy) *Doc {
	return &Doc{replica: replica, blocks: make(map[string]stamped), strategy: strategy, texts: make(map[string]*Text)}
}

// Strategy reports which MergeStrategy this document resolves
// conflicting writes with.
func (d *Doc) Strategy() MergeStrategy {
	return d.strategy
}

// Clock is the highest Lamport counter this replica has produced or seen.
func (d *Doc) Clock() uint64 { return d.clock }

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
	if d.strategy == MergeCharCRDT && !LWWKind(b.Kind) {
		return d.putCharCRDT(b)
	}
	return d.putLWW(b)
}

// LWWKindPrefix starts the Kind of blocks that are always merged
// last-writer-wins, even in a MergeCharCRDT document: whole-value records
// such as a comment or a style, whose Content must not be merged
// character by character.
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

// putCharCRDT diffs b.Content against this block's current merged
// text at the rune level, translates the edit script into CharOps
// against the block's Text, and queues the (typically much smaller)
// delta rather than the whole Content string.
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

// Format marks the visible characters [from, to) of block id with key =
// value (an empty value removes the key there) and queues the op. It is a
// single small op however long the range is. growEnd says whether text
// typed at the end of the range joins it (bold, colour) or not (links,
// comments); text typed at its start never does. Formatting needs
// MergeCharCRDT, and reports false (and does nothing) under MergeLWW, for
// an unknown block, or for an empty or out-of-range span.
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

// Runs returns block id's visible text as runs of equal formatting (one
// run, with no attributes, for a block that was never formatted). Only
// MergeCharCRDT documents have formatting.
func (d *Doc) Runs(id string) []Run {
	if text, ok := d.texts[id]; ok {
		return text.Runs()
	}
	if b, ok := d.blocks[id]; ok && b.block.Content != "" {
		return []Run{{Text: b.block.Content, Attrs: map[string]string{}}}
	}
	return nil
}

// textFor returns the Text for a block ID, creating an empty one on
// first use.
func (d *Doc) textFor(id string) *Text {
	t, ok := d.texts[id]
	if !ok {
		t = &Text{}
		d.texts[id] = t
	}
	return t
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

// Apply merges received Ops into this document. In MergeLWW (the
// default), the existing block for an op's ID is replaced only if the
// op's timestamp is later. In MergeCharCRDT, Content is merged at
// per-character granularity regardless of timestamp order -- only
// Kind/Order/Deleted are gated by the timestamp comparison. Either
// way, applying the same op twice, or applying a batch in any order,
// always lands on the same final state (idempotent, commutative,
// associative: the three properties that make this safe to run
// whenever a connection happens to be available).
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

// isLWWBlock reports whether the block already stored under id is an LWW
// kind (so a later op for it, whatever its own Kind says, merges LWW too).
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
		d.observe(c.ID) // keep later local writes after everything seen
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
		// This op's Kind/Order/Deleted also win, same as MergeLWW.
		d.blocks[op.Block.ID] = stamped{block: merged, ts: op.TS}
	case len(op.CharOps) > 0:
		// An older op can still carry real character edits that must
		// be merged in, even though its metadata loses to what's
		// already stored.
		cur.block.Content = merged.Content
		d.blocks[op.Block.ID] = cur
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
// delta. In MergeCharCRDT mode, each Op also carries the full CharOps
// needed to reconstruct that block's Text (tombstones included) on
// the new replica, not just its currently-visible Content -- without
// that, the new replica couldn't correctly merge a future concurrent
// edit against the right character-level structure.
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

// Splice replaces del visible characters of block id starting at visible
// index at with ins, as character ops (the block is created if it does not
// exist), and queues the op. Unlike Put it does not diff the whole text, so
// it is the call for editors that already know what changed. It needs
// MergeCharCRDT and reports false under MergeLWW or for a range outside the
// text.
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

// VisibleIDs returns the ids of block id's visible characters, in order:
// stable names for positions, to keep cursors in place across remote edits.
func (d *Doc) VisibleIDs(id string) []Timestamp {
	if text, ok := d.texts[id]; ok {
		_, ids := text.visible()
		return ids
	}
	return nil
}

// PositionAfter returns how many visible characters of block id come up to
// and including character cid: the visible index just behind it. It works
// for a character that has since been deleted (the position where it was).
// ok is false if the character is not known.
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
