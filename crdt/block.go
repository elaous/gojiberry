// Package crdt is the offline-first sync engine behind gojiberry's
// block editor: a last-writer-wins (LWW) register per block, keyed by
// block ID. Two replicas that never see the same network at the same
// time can each mutate their own copy freely; exchanging the small set
// of Ops produced since the last sync (a "delta", not the whole
// document) merges into an identical result on both sides regardless
// of delivery order, duplication, or which side went first — the
// three properties (commutative, associative, idempotent) that make a
// CRDT safe to sync over an unreliable, metered mobile connection.
package crdt

import "sort"

// Timestamp totally orders writes across replicas without a shared
// clock: Counter is a Lamport clock (bumped past the highest value
// this replica has seen, locally or received), and Replica breaks ties
// between two writes with the same Counter deterministically.
type Timestamp struct {
	Counter uint64
	Replica string
}

// after reports whether t happened after other, using Replica only to
// break an exact Counter tie so every replica resolves conflicts to
// the same winner without communicating further.
func (t Timestamp) after(other Timestamp) bool {
	if t.Counter != other.Counter {
		return t.Counter > other.Counter
	}
	return t.Replica > other.Replica
}

// Block is one unit of the document: a heading, paragraph, list item,
// or table row — the "vertical block" the mobile UI edits and renders,
// as opposed to a position on a fixed printable page.
type Block struct {
	ID      string
	Kind    string
	Content string
	// Order is a fractional index: inserting a block between two
	// existing ones only needs a value between their Orders, never a
	// renumbering pass across the whole document (see OrderBetween).
	Order   float64
	Deleted bool
}

// Op is a single mutation and the unit exchanged between replicas —
// the "micro-data packet" a delta-sync transfers instead of the whole
// document.
type Op struct {
	Block Block
	TS    Timestamp
}

// OrderBetween returns a fractional position strictly between before
// and after, for inserting a block between two existing ones without
// touching either neighbor's Order.
func OrderBetween(before, after float64) float64 {
	return before + (after-before)/2
}

// OrderEnd returns a position after every block currently in blocks
// (0 if empty), for appending a new block at the end of the list.
func OrderEnd(blocks []Block) float64 {
	max := 0.0
	found := false
	for _, b := range blocks {
		if !found || b.Order > max {
			max = b.Order
			found = true
		}
	}
	if !found {
		return 0
	}
	return max + 1
}

// sortBlocks orders visible blocks for rendering: by Order, then ID as
// a stable tiebreaker so equal-Order blocks (which shouldn't normally
// occur) still render deterministically on every replica.
func sortBlocks(blocks []Block) {
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Order != blocks[j].Order {
			return blocks[i].Order < blocks[j].Order
		}
		return blocks[i].ID < blocks[j].ID
	})
}
