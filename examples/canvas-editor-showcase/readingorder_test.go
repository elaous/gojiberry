//go:build !(js && wasm)

package main

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/components"
)

func TestFloatingObjectsAreReadWhereTheyAre(t *testing.T) {
	text := func(s string) components.A11yBlock {
		return components.A11yBlock{Kind: components.A11yParagraph, Text: s, Mapped: true}
	}
	note := func(s string, x, y float64) placedBlock {
		return placedBlock{block: components.A11yBlock{Kind: components.A11yParagraph, Text: "Note: " + s}, x: x, y: y}
	}
	blocks := []components.A11yBlock{text("A"), text("B"), text("C"), text("D")}
	ys := []float64{10, 60, 110, 160}
	var got []string
	read := func(objs ...placedBlock) string {
		got = got[:0]
		for _, b := range orderByPosition(blocks, ys, objs) {
			got = append(got, b.Text)
		}
		return strings.Join(got, " | ")
	}
	if g := read(); g != "A | B | C | D" {
		t.Errorf("no objects: %s", g)
	}
	// between B and C; at the very top; below everything
	if g := read(note("mid", 0, 90), note("top", 0, 0), note("end", 0, 400)); g != "Note: top | A | B | Note: mid | C | D | Note: end" {
		t.Errorf("placed: %s", g)
	}
	// the same height: left to right
	if g := read(note("right", 300, 90), note("left", 20, 90)); g != "A | B | Note: left | Note: right | C | D" {
		t.Errorf("same height: %s", g)
	}
	// a block with no place of its own stays with the one before it
	ys2 := []float64{10, -1, 110, 160}
	got = got[:0]
	for _, b := range orderByPosition(blocks, ys2, []placedBlock{note("n", 0, 60)}) {
		got = append(got, b.Text)
	}
	if strings.Join(got, " | ") != "A | Note: n | B | C | D" && strings.Join(got, " | ") != "A | B | Note: n | C | D" {
		t.Errorf("unplaced block: %v", got)
	}
}
