package canvas

// MenuItem is one row of a Menu.
type MenuItem struct {
	Label     string
	Action    func()
	Disabled  bool
	Danger    bool // drawn in the danger color (e.g. "Delete")
	Separator bool // a divider line; Label/Action are ignored
	// Swatch draws a small colour square before the label.
	Swatch string
}

// swatchW is the room a swatch takes before the label.
const swatchW = 22

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

// Menu is a context menu drawn on a canvas.
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

// Hover returns the highlighted item's index, or -1.
func (m *Menu) Hover() int { return m.hover }

// SetHover highlights item i if it can be chosen; -1 clears.
func (m *Menu) SetHover(i int) {
	if i < 0 || m.enabled(i) {
		m.hover = i
	}
}

// Activate runs item i as if clicked.
func (m *Menu) Activate(i int) { m.activate(i) }

// Rect returns the open menu's rectangle (x, y, w, h), zero while closed.
func (m *Menu) Rect() (x, y, w, h float64) {
	if !m.open {
		return 0, 0, 0, 0
	}
	return m.x, m.y, m.w, m.height()
}

// Labels returns the open menu's item labels, with "" for separators.
func (m *Menu) Labels() []string {
	var out []string
	for _, it := range m.items {
		out = append(out, it.Label)
	}
	return out
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

// Open shows the menu with its top-left at (x, y), shifted to fit.
func (m *Menu) Open(items []MenuItem, x, y, boundsW, boundsH float64) {
	m.OpenWithin(items, x, y, [4]float64{0, 0, boundsW, boundsH})
}

// OpenWithin is Open, keeping the menu inside bounds.
func (m *Menu) OpenWithin(items []MenuItem, x, y float64, bounds [4]float64) {
	m.items = items
	m.Painter.SetFont(m.Style.Font)
	m.w = m.Style.MinWidth
	for _, it := range items {
		if !it.Separator {
			extra := 0.0
			if it.Swatch != "" {
				extra = swatchW
			}
			m.w = max(m.w, m.Painter.MeasureTextWidth(it.Label)+extra+2*m.Style.PadX)
		}
	}
	if bounds[2] > 0 {
		x = min(x, bounds[0]+bounds[2]-m.w)
		x = max(x, bounds[0])
	}
	if bounds[3] > 0 {
		y = min(y, bounds[1]+bounds[3]-m.height())
		y = max(y, bounds[1])
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

// MouseMove updates the highlight; reports whether a redraw is needed.
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

// MouseDown handles a press at (x, y); reports whether the menu took it.
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

// HandleKey moves the highlight with arrows, skipping separators and disabled items.
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
			tx := m.x + s.PadX
			if it.Swatch != "" {
				p.SetFillStyle(it.Swatch)
				p.FillRect(tx, top+(ih-14)/2, 14, 14)
				p.SetStrokeStyle(s.BorderColor)
				p.StrokeRect(tx+0.5, top+(ih-14)/2+0.5, 13, 13)
				tx += swatchW
			}
			switch {
			case it.Disabled:
				p.SetFillStyle(s.DisabledColor)
			case it.Danger:
				p.SetFillStyle(s.DangerColor)
			default:
				p.SetFillStyle(s.TextColor)
			}
			p.FillText(it.Label, tx, top+ih/2)
		}
		top += ih
	}
}
