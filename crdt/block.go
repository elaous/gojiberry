package crdt

import "sort"

// Timestamp totally orders writes across replicas.
type Timestamp struct {
	Counter uint64
	Replica string
}

func (t Timestamp) after(other Timestamp) bool {
	if t.Counter != other.Counter {
		return t.Counter > other.Counter
	}
	return t.Replica > other.Replica
}

type Block struct {
	ID      string
	Kind    string
	Content string
	// Order is a fractional index that positions the block.
	Order   float64
	Deleted bool
}

// Op is one mutation, exchanged between replicas.
type Op struct {
	Block Block
	TS    Timestamp
	// CharOps carries per-character CRDT edits for the block's text.
	CharOps []CharOp
}

// OrderBetween returns a fractional position between a and b.
func OrderBetween(before, after float64) float64 {
	return before + (after-before)/2
}

// OrderEnd returns a position after every current block.
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

func sortBlocks(blocks []Block) {
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].Order != blocks[j].Order {
			return blocks[i].Order < blocks[j].Order
		}
		return blocks[i].ID < blocks[j].ID
	})
}
