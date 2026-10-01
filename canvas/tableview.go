package canvas

import "strings"

// HitKind says what a point on a TableView is over.
type HitKind int

const (
	HitNone         HitKind = iota
	HitCell                 // a table cell; Hit.Row/Col say which
	HitAddColumn            // the "+" bar on the right edge
	HitAddRow               // the "+" bar along the bottom edge
	HitColumnHandle         // the grip above a column; Hit.Col says which
	HitRowHandle            // the grip left of a row; Hit.Row says which
	HitTableHandle          // the 2x2-boxes icon at the top-left corner
)

// SelKind is what a Selection covers.
type SelKind int

const (
	SelNone SelKind = iota
	SelColumn
	SelRow
	SelTable
)

// Selection is the selected column, row or whole table (Index is the
// column/row number and is unused for SelTable).
type Selection struct {
	Kind  SelKind
	Index int
}

// Hit is the result of TableView.HitTest.
type Hit struct {
	Kind     HitKind
	Row, Col int // valid for HitCell
}

// TableView draws a Table on a canvas and, in ModeEdit, handles editing it, the way
// Obsidian's tables work: click a cell to edit it in place; Tab /
// Shift+Tab, Enter and the arrow keys move between cells; Tab or Enter
// past the last cell adds a row; and hovering the table reveals a "+"
// bar on the right edge (add column) and along the bottom edge (add
// row).
//
// Coordinates are canvas-local pixels with the table's top-left corner
// at (X, Y). Feed it pointer events with MouseMove/MouseDown/MouseLeave
// and keyboard events with HandleKey/InsertText, then call Draw after
// any of them that returned true (or whenever Dirty reports it). In a
// browser, Bind wires all of that up for you.
type TableView struct {
	Table   *Table
	Style   TableStyle
	X, Y    float64
	Painter Painter

	// OnChange, if set, is called after any edit to the table's
	// contents or shape (typing, adding a row or column).
	OnChange func(*Table)

	// OnDeleteTable, if set, adds a "Delete table" item to the table
	// handle's menu and is called when it's chosen. The view can't
	// delete itself, so removing it from the page is up to the caller.
	OnDeleteTable func()

	// CanvasW/CanvasH, if non-zero, keep context menus inside the
	// canvas (Bind sets them from the canvas element).
	CanvasW, CanvasH float64

	menu     *Menu
	viewport [4]float64
	sel      Selection
	mode     Mode
	refresh  func() // set by Bind
	editing  bool
	row, col int
	cursor   int // rune index into the active cell's text
	hover    Hit
	inRegion bool // pointer is over the table or its "+" bars (or their gap)
	caretOn  bool
}

// NewTableView returns a view of t drawn with DefaultTableStyle, in
// ModeView; call SetMode(ModeEdit) to make it editable.
func NewTableView(t *Table, p Painter) *TableView {
	return &TableView{Table: t, Style: DefaultTableStyle(), Painter: p, X: 36, Y: 36, caretOn: true, menu: NewMenu(p)}
}

// SetPainter implements PainterSetter.
func (v *TableView) SetPainter(p ImagePainter) {
	v.Painter = p
	v.menu.Painter = p
}

// SetViewport implements ViewportSetter: menus stay inside the visible part.
func (v *TableView) SetViewport(x, y, w, h float64) { v.viewport = [4]float64{x, y, w, h} }

// WantsFocus implements Focuser: a cell is being edited or a menu is open.
func (v *TableView) WantsFocus() bool { return v.editing || v.menu.IsOpen() }

// CSSCursor implements CursorProvider.
func (v *TableView) CSSCursor() string {
	if v.mode != ModeEdit {
		return ""
	}
	switch v.hover.Kind {
	case HitAddColumn, HitAddRow, HitColumnHandle, HitRowHandle, HitTableHandle:
		return "pointer"
	case HitCell:
		return "text"
	}
	return ""
}

// Mode returns the current mode.
func (v *TableView) Mode() Mode { return v.mode }

// SetMode switches between ModeView and ModeEdit. Leaving edit mode
// stops any in-progress cell edit and clears hover state. Returns true
// if a redraw is needed.
func (v *TableView) SetMode(m Mode) bool {
	if m == v.mode {
		return false
	}
	v.mode = m
	if m == ModeView {
		v.editing = false
		v.sel = Selection{}
		v.menu.Close()
		v.hover, v.inRegion = Hit{}, false
	}
	return true
}

// Refresh redraws the canvas if the view has been Bind-ed (no-op
// otherwise); call it after SetMode or after changing Table/Style.
func (v *TableView) Refresh() {
	if v.refresh != nil {
		v.refresh()
	}
}

// Active returns the cell being edited, and whether there is one.
func (v *TableView) Active() (row, col int, ok bool) { return v.row, v.col, v.editing }

// Cursor is the caret's rune offset within the active cell's text.
func (v *TableView) Cursor() int { return v.cursor }

// Hover returns what the pointer was last over.
func (v *TableView) Hover() Hit { return v.hover }

// SetCaretVisible sets the caret blink phase (Bind toggles it on a
// timer). Returns true if a redraw is needed.
func (v *TableView) SetCaretVisible(on bool) bool {
	changed := v.caretOn != on && v.editing
	v.caretOn = on
	return changed
}

func (v *TableView) font(row int) string {
	if row == 0 {
		return v.Style.HeaderFont
	}
	return v.Style.Font
}

// geometry is the computed layout: colX has Cols()+1 cumulative left
// edges (the last is the table's right edge) and rowY likewise.
type geometry struct {
	colX, rowY []float64
}

func (g geometry) right() float64  { return g.colX[len(g.colX)-1] }
func (g geometry) bottom() float64 { return g.rowY[len(g.rowY)-1] }

func (v *TableView) layout() geometry {
	t, s := v.Table, v.Style
	g := geometry{colX: make([]float64, t.Cols()+1), rowY: make([]float64, t.Rows()+1)}
	widths := make([]float64, t.Cols())
	for c := range widths {
		widths[c] = s.MinColWidth
	}
	for r, row := range t.Cells {
		v.Painter.SetFont(v.font(r))
		for c, text := range row {
			widths[c] = max(widths[c], v.Painter.MeasureTextWidth(text)+2*s.PadX)
		}
	}
	g.colX[0], g.rowY[0] = v.X, v.Y
	for c, w := range widths {
		g.colX[c+1] = g.colX[c] + w
	}
	for r := range t.Cells {
		g.rowY[r+1] = g.rowY[r] + s.RowHeight
	}
	return g
}

// Size returns the width and height of canvas needed to draw the table
// with its handles (above and left of X, Y) and "+" bars -- handy for
// sizing the canvas. It does not account for an open menu.
func (v *TableView) Size() (w, h float64) {
	g := v.layout()
	s := v.Style
	return g.right() + s.BarGap + s.BarSize, g.bottom() + s.BarGap + s.BarSize
}

// ColumnEdges returns Cols()+1 x-positions: each column's left edge and,
// last, the table's right edge.
func (v *TableView) ColumnEdges() []float64 { return v.layout().colX }

// RowEdges returns Rows()+1 y-positions: each row's top edge and, last,
// the table's bottom edge.
func (v *TableView) RowEdges() []float64 { return v.layout().rowY }

// Selection returns what is currently selected.
func (v *TableView) Selection() Selection { return v.sel }

// Menu returns the view's context menu (open while a handle's menu is
// showing).
func (v *TableView) Menu() *Menu { return v.menu }

// barRects returns the add-column bar (right edge, full table height)
// and add-row bar (bottom edge, full table width) as x, y, w, h.
func (v *TableView) barRects(g geometry) (col, row [4]float64) {
	s := v.Style
	col = [4]float64{g.right() + s.BarGap, v.Y, s.BarSize, g.bottom() - v.Y}
	row = [4]float64{v.X, g.bottom() + s.BarGap, g.right() - v.X, s.BarSize}
	return col, row
}

func inRect(x, y float64, r [4]float64) bool {
	return x >= r[0] && x < r[0]+r[2] && y >= r[1] && y < r[1]+r[3]
}

// HitTest reports what is at (x, y).
func (v *TableView) HitTest(x, y float64) Hit {
	g := v.layout()
	colBar, rowBar := v.barRects(g)
	hs, gap := v.Style.HandleSize, v.Style.BarGap
	switch {
	case inRect(x, y, colBar):
		return Hit{Kind: HitAddColumn}
	case inRect(x, y, rowBar):
		return Hit{Kind: HitAddRow}
	case inRect(x, y, [4]float64{v.X - gap - hs, v.Y - gap - hs, hs, hs}):
		return Hit{Kind: HitTableHandle}
	case inRect(x, y, [4]float64{v.X, v.Y - gap - hs, g.right() - v.X, hs}):
		return Hit{Kind: HitColumnHandle, Col: indexAt(g.colX, x)}
	case inRect(x, y, [4]float64{v.X - gap - hs, v.Y, hs, g.bottom() - v.Y}):
		return Hit{Kind: HitRowHandle, Row: indexAt(g.rowY, y)}
	case x < v.X || x >= g.right() || y < v.Y || y >= g.bottom():
		return Hit{}
	}
	return Hit{Kind: HitCell, Row: indexAt(g.rowY, y), Col: indexAt(g.colX, x)}
}

// indexAt returns which of the cumulative edges' spans contains pos.
func indexAt(edges []float64, pos float64) int {
	i := 0
	for i+2 < len(edges) && pos >= edges[i+1] {
		i++
	}
	return i
}

// MouseMove updates hover state; it returns true if a redraw is needed.
func (v *TableView) MouseMove(x, y float64) bool {
	if v.mode != ModeEdit {
		return false
	}
	menuChanged := v.menu.MouseMove(x, y)
	g := v.layout()
	s := v.Style
	h := v.HitTest(x, y)
	region := x >= v.X-s.BarGap-s.HandleSize && y >= v.Y-s.BarGap-s.HandleSize && x < g.right()+s.BarGap+s.BarSize && y < g.bottom()+s.BarGap+s.BarSize
	changed := h != v.hover || region != v.inRegion || menuChanged
	v.hover, v.inRegion = h, region
	return changed
}

// MouseLeave clears hover state, hiding the "+" bars (unless a cell is
// being edited). Returns true if a redraw is needed.
func (v *TableView) MouseLeave() bool {
	changed := v.hover != (Hit{}) || v.inRegion
	v.hover, v.inRegion = Hit{}, false
	return changed
}

// MouseDown handles a primary-button press at (x, y): it starts editing
// the clicked cell (caret placed at the click), or adds a column/row if
// a "+" bar was clicked -- and starts editing the new column's header /
// the new row's first cell. A press outside the table stops editing.
// Returns true if a redraw is needed.
func (v *TableView) MouseDown(x, y float64) bool {
	if v.mode != ModeEdit {
		return false
	}
	if v.menu.MouseDown(x, y) {
		return true
	}
	h := v.HitTest(x, y)
	v.hover = h
	switch h.Kind {
	case HitColumnHandle, HitRowHandle, HitTableHandle:
		v.selectHandle(h)
		return true
	}
	v.sel = Selection{}
	switch h.Kind {
	case HitCell:
		v.edit(h.Row, h.Col)
		v.cursor = v.cursorAt(x, h.Row, h.Col)
	case HitAddColumn:
		v.Table.AddColumn()
		v.changed()
		v.edit(0, v.Table.Cols()-1)
	case HitAddRow:
		v.Table.AddRow()
		v.changed()
		v.edit(v.Table.Rows()-1, 0)
	default:
		v.editing = false
	}
	return true
}

// cursorAt returns the rune offset in cell (row, col) closest to x.
func (v *TableView) cursorAt(x float64, row, col int) int {
	g := v.layout()
	rel := x - (g.colX[col] + v.Style.PadX)
	runes := []rune(v.Table.Cell(row, col))
	v.Painter.SetFont(v.font(row))
	prev := 0.0
	for i := 1; i <= len(runes); i++ {
		w := v.Painter.MeasureTextWidth(string(runes[:i]))
		if rel < (prev+w)/2 {
			return i - 1
		}
		prev = w
	}
	return len(runes)
}

func (v *TableView) edit(row, col int) {
	v.editing, v.row, v.col = true, row, col
	v.cursor = len([]rune(v.Table.Cell(row, col)))
	v.caretOn = true
}

func (v *TableView) changed() {
	if v.OnChange != nil {
		v.OnChange(v.Table)
	}
}

// InsertText inserts s at the caret of the active cell. Line breaks are
// dropped (cells are single-line). No-op if no cell is being edited.
func (v *TableView) InsertText(s string) bool {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	if v.mode != ModeEdit || !v.editing || s == "" {
		return false
	}
	runes := []rune(v.Table.Cell(v.row, v.col))
	ins := []rune(s)
	out := append(append(append([]rune{}, runes[:v.cursor]...), ins...), runes[v.cursor:]...)
	v.Table.SetCell(v.row, v.col, string(out))
	v.cursor += len(ins)
	v.caretOn = true
	v.changed()
	return true
}

// HandleKey handles a key press (using DOM KeyboardEvent.key names) and
// reports whether it was consumed, in which case the caller should
// suppress the browser's default action and redraw. Plain printable
// characters are not handled here -- deliver those via InsertText.
//
//	Tab / Shift+Tab   next / previous cell; Tab in the last cell adds a row
//	Enter             cell below; adds a row from the last row
//	ArrowUp/Down      cell above / below
//	ArrowLeft/Right   move the caret, crossing into the adjacent cell at the edge
//	Home / End        caret to start / end of the cell
//	Backspace/Delete  delete before / after the caret
//	Escape            stop editing (or clear the selection)
//
// While a context menu is open it gets every key instead: ArrowUp/Down,
// Enter and Escape.
func (v *TableView) HandleKey(key string, shift bool) bool {
	if v.mode != ModeEdit {
		return false
	}
	if v.menu.IsOpen() {
		return v.menu.HandleKey(key)
	}
	if !v.editing {
		if key == "Escape" && v.sel.Kind != SelNone {
			v.sel = Selection{}
			return true
		}
		return false
	}
	t := v.Table
	switch key {
	case "Tab":
		if shift {
			v.step(-1)
		} else {
			v.step(1)
		}
	case "Enter":
		if v.row == t.Rows()-1 {
			t.AddRow()
			v.changed()
		}
		v.edit(v.row+1, v.col)
	case "ArrowDown":
		if v.row+1 < t.Rows() {
			v.moveTo(v.row+1, v.col)
		}
	case "ArrowUp":
		if v.row > 0 {
			v.moveTo(v.row-1, v.col)
		}
	case "ArrowLeft":
		if v.cursor > 0 {
			v.cursor--
		} else {
			v.step(-1)
		}
	case "ArrowRight":
		if v.cursor < len([]rune(t.Cell(v.row, v.col))) {
			v.cursor++
		} else {
			v.stepNoGrow(1)
		}
	case "Home":
		v.cursor = 0
	case "End":
		v.cursor = len([]rune(t.Cell(v.row, v.col)))
	case "Backspace":
		if v.cursor > 0 {
			runes := []rune(t.Cell(v.row, v.col))
			t.SetCell(v.row, v.col, string(append(runes[:v.cursor-1], runes[v.cursor:]...)))
			v.cursor--
			v.changed()
		}
	case "Delete":
		runes := []rune(t.Cell(v.row, v.col))
		if v.cursor < len(runes) {
			t.SetCell(v.row, v.col, string(append(runes[:v.cursor], runes[v.cursor+1:]...)))
			v.changed()
		}
	case "Escape":
		v.editing = false
	default:
		return false
	}
	v.caretOn = true
	return true
}

// moveTo activates (row, col), keeping the caret's column where it can.
func (v *TableView) moveTo(row, col int) {
	old := v.cursor
	v.edit(row, col)
	v.cursor = min(old, len([]rune(v.Table.Cell(row, col))))
}

// step moves delta cells in reading order, landing the caret at the
// end of the cell's text. Stepping forward past the last cell adds a
// row; stepping back from the first cell stays put.
func (v *TableView) step(delta int) {
	t := v.Table
	i := v.row*t.Cols() + v.col + delta
	if i < 0 {
		return
	}
	if i >= t.Rows()*t.Cols() {
		t.AddRow()
		v.changed()
	}
	v.edit(i/t.Cols(), i%t.Cols())
	if delta < 0 {
		return
	}
	v.cursor = 0
}

// stepNoGrow is ArrowRight at the end of a cell: go to the next cell
// (caret at its start) but, unlike Tab, never add a row.
func (v *TableView) stepNoGrow(delta int) {
	t := v.Table
	i := v.row*t.Cols() + v.col + delta
	if i < 0 || i >= t.Rows()*t.Cols() {
		return
	}
	v.edit(i/t.Cols(), i%t.Cols())
	v.cursor = 0
}

// Draw paints the table (and, in ModeEdit while hovered or editing, the
// "+" bars)
// onto the Painter. It does not clear the canvas first.
func (v *TableView) Draw() {
	p, s, t := v.Painter, v.Style, v.Table
	g := v.layout()

	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	p.SetLineWidth(1)
	for r := range t.Cells {
		for c := range t.Cells[r] {
			x, y, w, h := g.colX[c], g.rowY[r], g.colX[c+1]-g.colX[c], g.rowY[r+1]-g.rowY[r]
			bg := s.CellBG
			if r == 0 {
				bg = s.HeaderBG
			}
			p.SetFillStyle(bg)
			p.FillRect(x, y, w, h)
			p.SetStrokeStyle(s.GridColor)
			p.StrokeRect(x+0.5, y+0.5, w-1, h-1)
			p.SetFont(v.font(r))
			p.SetFillStyle(s.TextColor)
			p.FillText(t.Cells[r][c], x+s.PadX, y+h/2)
		}
	}

	if v.mode == ModeEdit {
		v.drawSelection(g)
	}

	if v.mode == ModeEdit && v.editing {
		x, y := g.colX[v.col], g.rowY[v.row]
		w, h := g.colX[v.col+1]-x, g.rowY[v.row+1]-y
		p.SetStrokeStyle(s.ActiveColor)
		p.SetLineWidth(2)
		p.StrokeRect(x+1, y+1, w-2, h-2)
		if v.caretOn {
			p.SetFont(v.font(v.row))
			runes := []rune(t.Cell(v.row, v.col))
			cx := x + s.PadX + p.MeasureTextWidth(string(runes[:min(v.cursor, len(runes))]))
			p.BeginPath()
			p.MoveTo(cx+0.5, y+6)
			p.LineTo(cx+0.5, y+h-6)
			p.SetLineWidth(1)
			p.Stroke()
		}
	}

	if v.mode == ModeEdit && (v.inRegion || v.editing || v.sel.Kind != SelNone) {
		colBar, rowBar := v.barRects(g)
		v.drawBar(colBar, v.hover.Kind == HitAddColumn)
		v.drawBar(rowBar, v.hover.Kind == HitAddRow)
		v.drawHandles(g)
	}
	v.menu.Draw()
}

func (v *TableView) drawBar(r [4]float64, hovered bool) {
	p, s := v.Painter, v.Style
	if hovered {
		p.SetFillStyle(s.BarHoverColor)
	} else {
		p.SetFillStyle(s.BarColor)
	}
	p.FillRect(r[0], r[1], r[2], r[3])
	p.SetFont(s.BarFont)
	p.SetTextAlign("center")
	p.SetFillStyle(s.BarTextColor)
	p.FillText("+", r[0]+r[2]/2, r[1]+r[3]/2)
	p.SetTextAlign("left")
}
