package main

import (
	"sort"

	"github.com/elaous/gojiberry/components"
)

type placedBlock struct {
	block components.A11yBlock
	x, y  float64
}

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
