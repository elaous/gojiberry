package canvas

import "math"

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

func (v *TableView) resolve() {
	if ps := v.pageSet(); v.Anchor != nil && ps != nil {
		v.X, v.Y = ps.Resolve(*v.Anchor)
	}
}

// SetFlow sets the text flow to wrap around the table.
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

// Rect returns the table's cell rectangle: x, y, w, h.
func (v *TableView) Rect() (x, y, w, h float64) {
	g := v.layout()
	return v.X, v.Y, g.right() - v.X, g.bottom() - v.Y
}

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

// ContainsPoint: the cells, and in edit mode the handles.
func (v *TableView) ContainsPoint(x, y float64) bool {
	rx, ry, rw, rh := v.Rect()
	if inRect(x, y, [4]float64{rx, ry, rw, rh}) {
		return true
	}
	return v.mode == ModeEdit && (v.HitTest(x, y).Kind != HitNone || v.menu.HitTest(x, y) >= 0)
}

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

func (v *TableView) Moving() bool { return v.press != nil && v.press.moved }

var (
	_ Obstacle = (*TableView)(nil)
	_ Hitter   = (*TableView)(nil)
)
