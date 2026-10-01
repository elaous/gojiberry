package canvas

import "math"

// tablePress is a press on the table icon, not yet known to be a click
// (select the table) or a drag (move it).
type tablePress struct {
	x, y           float64
	startX, startY float64
	hit            Hit
	moved          bool
}

const dragThreshold = 4 // px a press must travel to count as a drag

func (v *TableView) snap(n float64) float64 {
	if v.Snap <= 0 {
		return n
	}
	return math.Round(n/v.Snap) * v.Snap
}

func (v *TableView) pageSet() *PageSet {
	if v.Pages != nil {
		return v.Pages
	}
	if v.flow != nil {
		return v.flow.Pages
	}
	return nil
}

// resolve updates X, Y from the Anchor.
func (v *TableView) resolve() {
	if ps := v.pageSet(); v.Anchor != nil && ps != nil {
		v.X, v.Y = ps.Resolve(*v.Anchor)
	}
}

// SetFlow sets the text flow to wrap around and registers the table as
// one of its obstacles (removing it from any previous flow). Text
// wraps only while Wrap is on.
func (v *TableView) SetFlow(f *TextFlow) {
	if v.flow != nil {
		v.flow.RemoveObstacle(v)
	}
	v.flow = f
	if f != nil {
		f.AddObstacle(v)
	}
}

// Flow returns the flow set with SetFlow.
func (v *TableView) Flow() *TextFlow { return v.flow }

// Rect returns the table's cells (not its handles or "+" bars): x, y, w, h.
func (v *TableView) Rect() (x, y, w, h float64) {
	g := v.layout()
	return v.X, v.Y, g.right() - v.X, g.bottom() - v.Y
}

// Exclusion implements Obstacle: while Wrap is on, the table plus
// WrapMargin is off limits to text.
func (v *TableView) Exclusion(y0, y1 float64) (x0, x1 float64, ok bool) {
	if !v.Wrap {
		return 0, 0, false
	}
	x, y, w, h := v.Rect()
	m := v.WrapMargin
	if y1 <= y-m || y0 >= y+h+m {
		return 0, 0, false
	}
	return x - m, x + w + m, true
}

// ContainsPoint implements Hitter: the cells, and in edit mode the
// handles, bars and an open menu, keep presses from the text beneath.
func (v *TableView) ContainsPoint(x, y float64) bool {
	rx, ry, rw, rh := v.Rect()
	if inRect(x, y, [4]float64{rx, ry, rw, rh}) {
		return true
	}
	return v.mode == ModeEdit && (v.HitTest(x, y).Kind != HitNone || v.menu.HitTest(x, y) >= 0)
}

// dragTable moves the table while the icon is held down, once the pointer
// has travelled far enough to be a drag; the position snaps to Snap,
// relative to the page when anchored.
func (v *TableView) dragTable(x, y float64) bool {
	p := v.press
	dx, dy := x-p.x, y-p.y
	if !p.moved {
		if math.Hypot(dx, dy) < dragThreshold {
			return false
		}
		p.moved = true
		v.menu.Close()
		v.sel = Selection{}
		v.editing = false
	}
	nx, ny := max(v.snap(p.startX+dx), 0), max(v.snap(p.startY+dy), 0)
	if ps := v.pageSet(); v.Anchor != nil && ps != nil {
		a := ps.AnchorAt(p.startX+dx, p.startY+dy)
		a.X, a.Y = v.snap(a.X), v.snap(a.Y)
		nx, ny = ps.Resolve(a)
	}
	if nx == v.X && ny == v.Y {
		return false
	}
	v.X, v.Y = nx, ny
	if ps := v.pageSet(); v.Anchor != nil && ps != nil {
		*v.Anchor = ps.AnchorAt(nx, ny)
	}
	if v.OnMove != nil {
		v.OnMove(v)
	}
	return true
}

// MouseUp ends a press on the table icon: if the pointer never moved
// far, it was a click, and selects the table and opens its menu.
func (v *TableView) MouseUp(x, y float64) bool {
	p := v.press
	if p == nil {
		return false
	}
	v.press = nil
	if !p.moved {
		v.selectHandle(p.hit)
	}
	return true
}

// Moving reports whether the table is being dragged to a new position.
func (v *TableView) Moving() bool { return v.press != nil && v.press.moved }

var (
	_ Obstacle = (*TableView)(nil)
	_ Hitter   = (*TableView)(nil)
)
