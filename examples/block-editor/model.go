package main

import (
	"strings"

	"github.com/elaous/gojiberry/crdt"
)

// Block kinds this demo understands.
const (
	KindHeading  = "heading"
	KindLineItem = "line-item"
	KindNote     = "note"
)

// NewHeading is a title block, e.g. the client/invoice name.
func NewHeading(id, text string, order float64) crdt.Block {
	return crdt.Block{ID: id, Kind: KindHeading, Content: text, Order: order}
}

func NewLineItem(id, description, amount string, order float64) crdt.Block {
	description = strings.ReplaceAll(description, "\t", " ")
	return crdt.Block{ID: id, Kind: KindLineItem, Content: description + "\t" + amount, Order: order}
}

func ParseLineItem(b crdt.Block) (description, amount string) {
	description, amount, _ = strings.Cut(b.Content, "\t")
	return description, amount
}

// NewNote is free-form text, e.g. a reminder about a discount.
func NewNote(id, text string, order float64) crdt.Block {
	return crdt.Block{ID: id, Kind: KindNote, Content: text, Order: order}
}
