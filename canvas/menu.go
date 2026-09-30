package canvas

// MenuItem is one row of a Menu.
type MenuItem struct {
	Label     string
	Action    func()
	Disabled  bool
	Danger    bool // drawn in the danger color (e.g. "Delete")
	Separator bool // a divider line; Label/Action are ignored
}

// MenuStyle controls how a Menu looks.
type MenuStyle struct {
	Font          string
	TextColor     string
	DisabledColor string
	DangerColor   string
	BG            string
	HoverBG       string
	BorderColor   string
	SepColor      string
	PadX          float64
	ItemHeight    float64
	SepHeight     float64
	MinWidth      float64
}

// DefaultMenuStyle is a light look matching DefaultTableStyle.
func DefaultMenuStyle() MenuStyle {
	return MenuStyle{
		Font:          "14px sans-serif",
		TextColor:     "#2e3338",
		DisabledColor: "#a7abb2",
		DangerColor:   "#d1383d",
		BG:            "#ffffff",
		HoverBG:       "#eceef1",
		BorderColor:   "#c4c7cd",
		SepColor:      "#e1e3e7",
		PadX:          12,
		ItemHeight:    28,
		SepHeight:     9,
		MinWidth:      170,
	}
}

// Menu is a context menu drawn on a canvas. Unlike the other
// components in this package it has no view/edit modes -- a menu only
// exists to be interacted with, and is invisible while closed.
//
// Open it at a point with Open, route pointer and keyboard events
// through MouseMove/MouseDown/HandleKey while IsOpen, and call Draw
// last (after everything else) so it paints on top. Clicking an
// enabled item closes the menu and then runs its Action; clicking
// outside closes it and reports the click as not handled so the caller
// can process it normally.
type Menu struct {
	Style   MenuStyle
	Painter Painter

	items   []MenuItem
	x, y, w float64
	open    bool
	hover   int // index of the highlighted item, or -1
}

// NewMenu returns a closed menu with DefaultMenuStyle.
func NewMenu(p Painter) *Menu {
	return &Menu{Style: DefaultMenuStyle(), Painter: p, hover: -1}
}

// IsOpen reports whether the menu is showing.
func (m *Menu) IsOpen() bool { return m.open }

// Close hides the menu.
func (m *Menu) Close() { m.open, m.hover = false, -1 }

func (m *Menu) height() float64 {
	h := 0.0
	for _, it := range m.items {
		h += m.itemHeight(it)
	}
	return h
}

func (m *Menu) itemHeight(it MenuItem) float64 {
	if it.Separator {
		return m.Style.SepHeight
	}
	return m.Style.ItemHeight
}

// Open shows items with the menu's top-left corner at (x, y), shifted
// as needed to stay inside a boundsW x boundsH canvas (a zero bound
// disables clamping on that axis).
func (m *Menu) Open(items []MenuItem, x, y, boundsW, boundsH float64) {
	m.items = items
	m.Painter.SetFont(m.Style.Font)
	m.w = m.Style.MinWidth
	for _, it := range items {
		if !it.Separator {
			m.w = max(m.w, m.Painter.MeasureTextWidth(it.Label)+2*m.Style.PadX)
		}
	}
	if boundsW > 0 {
		x = min(x, boundsW-m.w)
	}
	if boundsH > 0 {
		y = min(y, boundsH-m.height())
	}
	m.x, m.y = max(x, 0), max(y, 0)
	m.open = true
	m.hover = -1
}

// HitTest returns the index of the item at (x, y), or -1.
func (m *Menu) HitTest(x, y float64) int {
	if !m.open || x < m.x || x >= m.x+m.w || y < m.y {
		return -1
	}
	top := m.y
	for i, it := range m.items {
		h := m.itemHeight(it)
		if y < top+h {
			return i
		}
		top += h
	}
	return -1
}

func (m *Menu) enabled(i int) bool {
	return i >= 0 && i < len(m.items) && !m.items[i].Separator && !m.items[i].Disabled
}

// MouseMove updates the highlighted item; it returns true if a redraw
// is needed.
func (m *Menu) MouseMove(x, y float64) bool {
	if !m.open {
		return false
	}
	i := m.HitTest(x, y)
	if !m.enabled(i) {
		i = -1
	}
	changed := i != m.hover
	m.hover = i
	return changed
}

// MouseDown handles a press at (x, y) and reports whether the menu
// consumed it: true for a press on an item (enabled, or not) or on the
// menu's padding; false -- after closing the menu -- for a press
// outside it (or when the menu wasn't open).
func (m *Menu) MouseDown(x, y float64) bool {
	if !m.open {
		return false
	}
	i := m.HitTest(x, y)
	if i < 0 {
		m.Close()
		return false
	}
	m.activate(i)
	return true
}

func (m *Menu) activate(i int) {
	if !m.enabled(i) {
		return
	}
	action := m.items[i].Action
	m.Close()
	if action != nil {
		action()
	}
}

// HandleKey handles ArrowUp/ArrowDown (move, skipping separators and
// disabled items), Enter (activate) and Escape (close). While open the
// menu is modal: it reports every key as consumed.
func (m *Menu) HandleKey(key string) bool {
	if !m.open {
		return false
	}
	switch key {
	case "ArrowDown":
		m.step(1)
	case "ArrowUp":
		m.step(-1)
	case "Enter":
		m.activate(m.hover)
	case "Escape":
		m.Close()
	}
	return true
}

func (m *Menu) step(dir int) {
	n := len(m.items)
	i := m.hover
	for range n {
		if i < 0 && dir < 0 {
			i = n
		}
		i = (i + dir + n) % n
		if m.enabled(i) {
			m.hover = i
			return
		}
	}
}

// Draw paints the menu; it does nothing while closed.
func (m *Menu) Draw() {
	if !m.open {
		return
	}
	p, s := m.Painter, m.Style
	h := m.height()
	p.SetFillStyle(s.BG)
	p.FillRect(m.x, m.y, m.w, h)
	p.SetStrokeStyle(s.BorderColor)
	p.SetLineWidth(1)
	p.StrokeRect(m.x+0.5, m.y+0.5, m.w-1, h-1)

	p.SetFont(s.Font)
	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	top := m.y
	for i, it := range m.items {
		ih := m.itemHeight(it)
		switch {
		case it.Separator:
			p.SetFillStyle(s.SepColor)
			p.FillRect(m.x+1, top+ih/2, m.w-2, 1)
		default:
			if i == m.hover {
				p.SetFillStyle(s.HoverBG)
				p.FillRect(m.x+1, top, m.w-2, ih)
			}
			switch {
			case it.Disabled:
				p.SetFillStyle(s.DisabledColor)
			case it.Danger:
				p.SetFillStyle(s.DangerColor)
			default:
				p.SetFillStyle(s.TextColor)
			}
			p.FillText(it.Label, m.x+s.PadX, top+ih/2)
		}
		top += ih
	}
}
