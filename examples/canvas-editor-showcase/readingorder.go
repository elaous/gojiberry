package main

import (
	"sort"

	"github.com/elaous/gojiberry/components"
)

// A floating note, table or picture is not in the text flow, so the reading copy has no place for it by itself.
// It is put where a sighted reader meets it: after the text that is above it on the page, before the text that is
// below it (reading order is the visual order, top to bottom, left to right).

// placedBlock is a block of the reading copy for a floating object, with the place of the object on the sheets.
type placedBlock struct {
	block components.A11yBlock
	x, y  float64
}

// orderByPosition merges the floating objects into the blocks of the text. ys holds the top of each text block on
// the sheets (the same flow coordinates as the objects), or a negative number for a block with no place of its own,
// which stays with the block before it. Objects at the same height keep their left-to-right order.
func orderByPosition(blocks []components.A11yBlock, ys []float64, objs []placedBlock) []components.A11yBlock {
	objs = append([]placedBlock(nil), objs...)
	sort.SliceStable(objs, func(i, j int) bool {
		if objs[i].y != objs[j].y {
			return objs[i].y < objs[j].y
		}
		return objs[i].x < objs[j].x
	})
	out := make([]components.A11yBlock, 0, len(blocks)+len(objs))
	next := 0
	for i, b := range blocks {
		if i < len(ys) && ys[i] >= 0 {
			for next < len(objs) && objs[next].y < ys[i] {
				out = append(out, objs[next].block)
				next++
			}
		}
		out = append(out, b)
	}
	for ; next < len(objs); next++ {
		out = append(out, objs[next].block)
	}
	return out
}
