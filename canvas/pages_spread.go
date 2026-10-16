package canvas

import (
	"fmt"
	"slices"
)

// Spread view: the shade in the fold between two facing sheets, and the corner tabs that turn to the neighbouring
// pair. Both are drawn only while PageSet.Spread is on.

// foldTab is the side of the square corner tab; its triangle is drawn in one-pixel strips.
const foldTab = 22

// drawGutterShade darkens the inner edge of each sheet of a facing pair, strongest at the fold and fading
// across GutterShadeWidth, the way a book's pages curve into the spine.
func (ps *PageSet) drawGutterShade() {
	s := ps.Style
	if !ps.Spread || s.GutterShade <= 0 || s.GutterShadeWidth <= 0 {
		return
	}
	p := ps.Painter
	const steps = 8
	for _, row := range ps.rows() {
		if len(row) != 2 || row[0] < 0 {
			continue
		}
		lx, ly, lw, lh := ps.PageRect(row[0])
		rx, ry, _, rh := ps.PageRect(row[1])
		step := s.GutterShadeWidth / steps
		for k := range steps {
			a := s.GutterShade * float64(steps-k) / steps
			p.SetFillStyle(fmt.Sprintf("rgba(0, 0, 0, %.3f)", a))
			p.FillRect(lx+lw-step*float64(k+1), ly+1, step, lh-2)
			p.FillRect(rx+step*float64(k), ry+1, step, rh-2)
		}
	}
}

// turnTab is where a sheet's corner tab sits: the bottom outer corner (left for the left sheet of a pair, right for
// the right one). ok is false for a sheet without one (not a spread, or nothing to turn to).
func (ps *PageSet) turnTab(i int) (x, y float64, left, ok bool) {
	if !ps.Spread || ps.OnTurn == nil {
		return 0, 0, false, false
	}
	rows := ps.rows()
	r := slices.IndexFunc(rows, func(row []int) bool { return slices.Contains(row, i) })
	if r < 0 {
		return 0, 0, false, false
	}
	// The left sheet of a pair (or a lone last sheet) turns back; the right sheet turns forward.
	left = len(rows[r]) == 1 || rows[r][0] == i
	if _, can := ps.TurnTarget(i, left); !can {
		return 0, 0, false, false
	}
	px, py, pw, ph := ps.PageRect(i)
	if left {
		return px, py + ph - foldTab, true, true
	}
	return px + pw - foldTab, py + ph - foldTab, false, true
}

// TurnTarget returns the first sheet of the pair before (back) or after the pair that holds sheet i, and whether there
// is one.
func (ps *PageSet) TurnTarget(i int, back bool) (page int, ok bool) {
	rows := ps.rows()
	r := slices.IndexFunc(rows, func(row []int) bool { return slices.Contains(row, i) })
	if r < 0 {
		return 0, false
	}
	if back {
		r--
	} else {
		r++
	}
	if r < 0 || r >= len(rows) {
		return 0, false
	}
	for _, j := range rows[r] {
		if j >= 0 {
			return j, true
		}
	}
	return 0, false
}

// drawTurnTabs draws a small folded corner on each sheet that has a neighbouring pair to turn to.
func (ps *PageSet) drawTurnTabs() {
	p, s := ps.Painter, ps.Style
	for i := range ps.Pages {
		x, y, left, ok := ps.turnTab(i)
		if !ok {
			continue
		}
		// A right triangle in the corner, built from one-pixel strips: the page colour on the fold, the border
		// on its edge.
		for k := range foldTab {
			w := float64(foldTab - k)
			p.SetFillStyle(s.Shadow)
			if left {
				p.FillRect(x, y+float64(k), w, 1)
			} else {
				p.FillRect(x+float64(foldTab)-w, y+float64(k), w, 1)
			}
		}
	}
}

// turnAt handles a press on a corner tab: it asks the host to show the pair that tab leads to.
func (ps *PageSet) turnAt(x, y float64) bool {
	for i := range ps.Pages {
		tx, ty, left, ok := ps.turnTab(i)
		if !ok || x < tx || x > tx+foldTab || y < ty || y > ty+foldTab {
			continue
		}
		if page, can := ps.TurnTarget(i, left); can {
			ps.OnTurn(page)
			return true
		}
	}
	return false
}
