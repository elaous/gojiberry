// Package main demonstrates gojiberry's core pitch end to end: a
// mobile-first, block-based document (an invoice, in this demo) that
// two replicas can edit fully offline and reconcile with a CRDT
// delta-sync, plus a second, decoupled component that renders the
// same blocks as a clean final document instead of the raw editing
// view — data capture and output rendering never share one layout.
package main

import (
	"strings"

	"github.com/elaous/gojiberry/crdt"
)

// Block kinds this demo understands. A real app would have many more
// (checklists, tables, photos, ...); the point is that adding one
// never touches the sync engine, which only ever sees opaque Blocks.
const (
	KindHeading  = "heading"
	KindLineItem = "line-item"
	KindNote     = "note"
)

// NewHeading is a title block, e.g. the client/invoice name.
func NewHeading(id, text string, order float64) crdt.Block {
	return crdt.Block{ID: id, Kind: KindHeading, Content: text, Order: order}
}

// NewLineItem is a billable row: a description and an amount, packed
// into Block.Content with a tab separator so the sync engine still
// only ever handles a single opaque string per block.
func NewLineItem(id, description, amount string, order float64) crdt.Block {
	// The tab is the separator, so a tab inside the description would be taken for it
	// and move the rest into the amount.
	description = strings.ReplaceAll(description, "\t", " ")
	return crdt.Block{ID: id, Kind: KindLineItem, Content: description + "\t" + amount, Order: order}
}

// ParseLineItem splits a line-item block's Content back into its
// description and amount.
func ParseLineItem(b crdt.Block) (description, amount string) {
	description, amount, _ = strings.Cut(b.Content, "\t")
	return description, amount
}

// NewNote is free-form text, e.g. a reminder about a discount.
func NewNote(id, text string, order float64) crdt.Block {
	return crdt.Block{ID: id, Kind: KindNote, Content: text, Order: order}
}
