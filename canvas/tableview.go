package canvas

import "strings"

// HitKind says what a point on a TableView is over.
type HitKind int

const (
	HitNone          HitKind = iota
	HitCell                  // a table cell; Hit.Row/Col say which
	HitAddColumn             // the "+" bar on the right edge
	HitAddRow                // the "+" bar along the bottom edge
	HitColumnHandle          // the grip above a column; Hit.Col says which
	HitRowHandle             // the grip left of a row; Hit.Row says which
	HitTableHandle           // the 2x2-boxes icon at the top-left corner
	HitFormulaSwitch         // the eye / f(x) switch at the top-right corner
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

	// OnFieldFill and OnFieldEdit are called when a press lands on a cell that is a form field: in the calculated view
	// to fill it in (a checkbox is ticked by the press itself and OnFieldFill is not called), in the formula view to
	// change what the field is (not called for a locked field). The host changes the field, then calls Table.SyncFields.
	OnFieldFill, OnFieldEdit func(row, col int, f *FormFieldObject)

	// RenderCell, if set, is asked to draw the content of each cell: its row and column, its text and the cell's
	// rectangle (the background and the grid are already drawn, and the font and colour are those of the cell).
	// Returning true says it drew the content; false lets the table write the text as usual. A cell being edited is
	// always drawn by the table.
	// ShowFormulas shows the text of formula cells (=A1*B2) instead of their values; a view setting, like the
	// edit mode. See formula.go.
	ShowFormulas bool

	RenderCell func(p Painter, row, col int, text string, x, y, w, h float64) bool

	// OnDeleteTable, if set, adds a "Delete table" item to the table
	// handle's menu and is called when it's chosen. The view can't
	// delete itself, so removing it from the page is up to the caller.
	OnDeleteTable func()

	// Anchor, if set, pins the table to a page: X, Y are derived from it
	// (relative to that page's sheet) using Pages or, failing that, the
	// Flow's pages. Moving the table re-anchors it to the page it is over.
	Anchor *Anchor
	// Pages is the PageSet Anchor refers to; if nil the flow's is used.
	Pages *PageSet
	// Wrap makes text flow around the table once a Flow is set (SetFlow).
	Wrap       bool
	WrapMargin float64 // clear space around the table (NewTableView sets 8)
	// Snap is the move granularity in px (NewTableView sets 5).
	Snap float64
	// OnMove is called as the table is dragged to a new position.
	OnMove func(*TableView)

	// CanvasW/CanvasH, if non-zero, keep context menus inside the
	// canvas (Bind sets them from the canvas element).
	CanvasW, CanvasH float64

	menu     *Menu
	press    *tablePress
	flow     *TextFlow
	viewport [4]float64
	sel      Selection
	mode     Mode
	refresh  func() // set by Bind
	editing  bool
	row, col int
	cursor   int       // rune index into the active cell's text
	rng      cellRange // the block of cells selected with Shift (see SelectRange)
	// active is true once a cell has been edited or selected, so cell 0,0 is not
	// "active" just because row and col start at zero.
	everActive bool
	// locked says the active cell holds a formula that was opened in the calculated view: it can be moved through but
	// not changed until the formulas are shown (a cell that became a formula while it was typed stays editable).
	locked bool
	// editedAt orders the tables by when a cell of each was last entered (0: never), so a host can tell which table the
	// person was working in after the focus has moved to a panel.
	editedAt uint64
	// tip is the formula cell the pointer is over in the calculated view, for the tooltip showing its formula.
	tip      Hit
	tipSet   bool
	hover    Hit
	inRegion bool // pointer is over the table or its "+" bars (or their gap)
	caretOn  bool
}

// NewTableView returns a view of t drawn with DefaultTableStyle, in
// ModeView; call SetMode(ModeEdit) to make it editable.
func NewTableView(t *Table, p Painter) *TableView {
	return &TableView{Table: t, Style: DefaultTableStyle(), Painter: p, X: 36, Y: 36, caretOn: true, menu: NewMenu(p), WrapMargin: 8, Snap: 5}
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
	case HitAddColumn, HitAddRow, HitColumnHandle, HitRowHandle, HitTableHandle, HitFormulaSwitch:
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
	v.resolve()
	t, s := v.Table, v.Style
	g := geometry{colX: make([]float64, t.Cols()+1), rowY: make([]float64, t.Rows()+1)}
	widths := make([]float64, t.Cols())
	for c := range widths {
		widths[c] = s.MinColWidth
	}
	for r, row := range t.Cells {
		for c, text := range row {
			if rs, cs := t.Span(r, c); rs == 0 || cs > 1 {
				continue // covered, or wider than a column: see below
			}
			v.Painter.SetFont(v.cellFont(r, t.StyleAt(r, c)))
			widths[c] = max(widths[c], v.Painter.MeasureTextWidth(text)+2*s.PadX)
		}
	}
	for _, m := range t.Merges { // a merged cell wider than its columns widens the last of them
		if m.Cols < 2 {
			continue
		}
		v.Painter.SetFont(v.cellFont(m.Row, t.StyleAt(m.Row, m.Col)))
		need := v.Painter.MeasureTextWidth(t.Cell(m.Row, m.Col)) + 2*s.PadX
		have := 0.0
		for c := m.Col; c < m.Col+m.Cols; c++ {
			have += widths[c]
		}
		if need > have {
			widths[m.Col+m.Cols-1] += need - have
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

// cellFont is the font of a cell in row, with its own style on top.
func (v *TableView) cellFont(row int, st CellStyle) string {
	f := v.font(row)
	if st.Italic && !strings.Contains(f, "italic") {
		f = "italic " + f
	}
	if st.Bold && !strings.Contains(f, "bold") {
		f = "bold " + f
	}
	return f
}

// cellRect is where the cell at (row, col) is drawn: a merged cell covers the
// block, and a covered cell the block's too.
func (g geometry) cellRect(t *Table, row, col int) (x, y, w, h float64) {
	r, c := t.Owner(row, col)
	rs, cs := t.Span(r, c)
	return g.colX[c], g.rowY[r], g.colX[c+cs] - g.colX[c], g.rowY[r+rs] - g.rowY[r]
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
	case inRect(x, y, [4]float64{g.right() + gap, v.Y - gap - hs, hs, hs}):
		return Hit{Kind: HitFormulaSwitch}
	case inRect(x, y, [4]float64{v.X, v.Y - gap - hs, g.right() - v.X, hs}):
		return Hit{Kind: HitColumnHandle, Col: indexAt(g.colX, x)}
	case inRect(x, y, [4]float64{v.X - gap - hs, v.Y, hs, g.bottom() - v.Y}):
		return Hit{Kind: HitRowHandle, Row: indexAt(g.rowY, y)}
	case x < v.X || x >= g.right() || y < v.Y || y >= g.bottom():
		return Hit{}
	}
	r, c := v.Table.Owner(indexAt(g.rowY, y), indexAt(g.colX, x))
	return Hit{Kind: HitCell, Row: r, Col: c}
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
	tipChanged := v.trackTip(x, y)
	if v.mode != ModeEdit {
		return tipChanged
	}
	if v.press != nil {
		return v.dragTable(x, y)
	}
	menuChanged := v.menu.MouseMove(x, y)
	g := v.layout()
	s := v.Style
	h := v.HitTest(x, y)
	region := x >= v.X-s.BarGap-s.HandleSize && y >= v.Y-s.BarGap-s.HandleSize && x < g.right()+s.BarGap+s.BarSize && y < g.bottom()+s.BarGap+s.BarSize
	changed := h != v.hover || region != v.inRegion || menuChanged || tipChanged
	v.hover, v.inRegion = h, region
	return changed
}

// MouseLeave clears hover state, hiding the "+" bars (unless a cell is
// being edited). Returns true if a redraw is needed.
func (v *TableView) MouseLeave() bool {
	changed := v.hover != (Hit{}) || v.inRegion || v.tipSet
	v.tipSet = false
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
	case HitTableHandle:
		// Pressing the table icon either selects the table (a click) or
		// moves it (a drag); which, is decided as the pointer moves.
		v.press = &tablePress{x: x, y: y, startX: v.X, startY: v.Y, hit: h}
		return true
	case HitColumnHandle, HitRowHandle:
		v.selectHandle(h)
		return true
	case HitFormulaSwitch:
		v.editing = false
		v.sel = Selection{}
		v.SetShowFormulas(!v.ShowFormulas)
		return true
	}
	v.sel = Selection{}
	switch h.Kind {
	case HitCell:
		v.edit(h.Row, h.Col)
		v.cursor = v.cursorAt(x, h.Row, h.Col)
		if f := v.Table.FieldAt(h.Row, h.Col); f != nil {
			v.pressField(h.Row, h.Col, f)
		}
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

// pressField is what a press on a field cell does: tick a checkbox, ask the host to fill the field in (calculated view) or
// to change it (formula view, unless it is locked).
func (v *TableView) pressField(row, col int, f *FormFieldObject) {
	switch {
	case v.ShowFormulas:
		if !f.Locked && v.OnFieldEdit != nil {
			v.OnFieldEdit(row, col, f)
		}
	case f.Kind == FormCheckbox:
		if f.Value == "true" {
			f.Value = ""
		} else {
			f.Value = "true"
		}
		v.Table.SyncFields()
		v.changed()
	case v.OnFieldFill != nil:
		v.OnFieldFill(row, col, f)
	}
}

// fieldText is what a field cell shows, and whether it is shown as a problem or as the field's own definition.
func (v *TableView) fieldText(f *FormFieldObject) (text string, ink string) {
	if v.ShowFormulas {
		text = "[" + string(f.Kind) + "] " + f.Label
		if f.Locked {
			text += " (locked)"
		}
		return text, orStr(v.Style.FormulaColor, "#c01c28")
	}
	if f.Kind != FormCheckbox && f.Empty() && f.Placeholder != "" {
		return f.Label + ": " + f.Placeholder, "#8a8f98"
	}
	if f.Problem() != "" {
		return f.Words(), "#c01c28"
	}
	return f.Words(), orStr(v.Style.ValueColor, "#1c5fb4")
}

// cursorAt returns the rune offset in cell (row, col) closest to x.
func (v *TableView) cursorAt(x float64, row, col int) int {
	g := v.layout()
	cx, _, cw, _ := g.cellRect(v.Table, row, col)
	st := v.Table.StyleAt(row, col)
	runes := []rune(v.Table.Cell(row, col))
	v.Painter.SetFont(v.cellFont(row, st))
	rel := x - (cx + v.Style.PadX)
	switch st.Align {
	case AlignCenter:
		rel = x - (cx + (cw-v.Painter.MeasureTextWidth(string(runes)))/2)
	case AlignRight:
		rel = x - (cx + cw - v.Style.PadX - v.Painter.MeasureTextWidth(string(runes)))
	}
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
	row, col = v.Table.Owner(row, col)
	v.editing, v.row, v.col, v.everActive = true, row, col, true
	v.locked = v.Table.FieldAt(row, col) != nil || (!v.ShowFormulas && isFormula(v.Table.Cell(row, col)))
	editSeq++
	v.editedAt = editSeq
	v.rng = cellRange{}
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
	if v.mode != ModeEdit || !v.editing || s == "" || v.locked {
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
		rs, _ := t.Span(v.row, v.col)
		if v.row+rs >= t.Rows() {
			t.AddRow()
			v.changed()
		}
		v.edit(v.row+rs, v.col)
	case "ArrowDown":
		if shift {
			v.extendRange(1, 0)
			break
		}
		if rs, _ := t.Span(v.row, v.col); v.row+rs < t.Rows() {
			v.moveTo(v.row+rs, v.col)
		}
	case "ArrowUp":
		if shift {
			v.extendRange(-1, 0)
			break
		}
		if v.row > 0 {
			v.moveTo(v.row-1, v.col)
		}
	case "ArrowLeft":
		if shift {
			v.extendRange(0, -1)
			break
		}
		if v.cursor > 0 {
			v.cursor--
		} else {
			v.step(-1)
		}
	case "ArrowRight":
		if shift {
			v.extendRange(0, 1)
			break
		}
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
		if v.cursor > 0 && !v.locked {
			runes := []rune(t.Cell(v.row, v.col))
			t.SetCell(v.row, v.col, string(append(runes[:v.cursor-1], runes[v.cursor:]...)))
			v.cursor--
			v.changed()
		}
	case "Delete":
		runes := []rune(t.Cell(v.row, v.col))
		if v.cursor < len(runes) && !v.locked {
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
	i := v.nextCell(v.row*t.Cols()+v.col, delta)
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

// nextCell is the index (row*cols+col) delta cells on in reading order,
// skipping cells a merge covers; it is negative before the first cell and may
// be past the last.
func (v *TableView) nextCell(i, delta int) int {
	t := v.Table
	for {
		i += delta
		if i < 0 {
			return -1
		}
		if i >= t.Rows()*t.Cols() || !t.Covered(i/t.Cols(), i%t.Cols()) {
			return i
		}
	}
}

// stepNoGrow is ArrowRight at the end of a cell: go to the next cell
// (caret at its start) but, unlike Tab, never add a row.
func (v *TableView) stepNoGrow(delta int) {
	t := v.Table
	i := v.nextCell(v.row*t.Cols()+v.col, delta)
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
	t.SyncFields() // the text of a field cell is its field's words, whoever changed the field
	g := v.layout()

	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	p.SetLineWidth(1)
	shown := t.Values() // formulas as their values; the cell being edited shows its own text
	if v.ShowFormulas {
		shown = t.Cells
	}
	for r := range t.Cells {
		for c := range t.Cells[r] {
			if t.Covered(r, c) {
				continue
			}
			x, y, w, h := g.cellRect(t, r, c)
			st := t.StyleAt(r, c)
			bg := s.CellBG
			if r == 0 {
				bg = s.HeaderBG
			}
			if st.Background != "" {
				bg = st.Background
			}
			p.SetFillStyle(bg)
			p.FillRect(x, y, w, h)
			p.SetStrokeStyle(s.GridColor)
			p.StrokeRect(x+0.5, y+0.5, w-1, h-1)
			p.SetFont(v.cellFont(r, st))
			ink := orStr(st.Color, s.TextColor)
			if isFormula(t.Cells[r][c]) && st.Color == "" { // a formula in red, what it computed in blue
				ink = orStr(s.ValueColor, "#1c5fb4")
				if v.ShowFormulas || (v.mode == ModeEdit && v.editing && !v.locked && v.row == r && v.col == c) {
					ink = orStr(s.FormulaColor, "#c01c28")
				}
			}
			text := shown[r][c]
			if f := t.Fields[CellPos{r, c}]; f != nil && st.Color == "" {
				text, ink = v.fieldText(f)
			}
			p.SetFillStyle(ink)
			if v.mode == ModeEdit && v.editing && !v.locked && v.row == r && v.col == c {
				text = t.Cells[r][c]
			}
			if v.RenderCell != nil && (v.mode != ModeEdit || !v.editing || v.row != r || v.col != c) && v.RenderCell(p, r, c, text, x, y, w, h) {
				p.SetTextAlign("left")
				continue
			}
			switch st.Align {
			case AlignCenter:
				p.SetTextAlign("center")
				p.FillText(text, x+w/2, y+h/2)
			case AlignRight:
				p.SetTextAlign("right")
				p.FillText(text, x+w-s.PadX, y+h/2)
			default:
				p.SetTextAlign("left")
				p.FillText(text, x+s.PadX, y+h/2)
			}
			p.SetTextAlign("left")
		}
	}

	if v.mode == ModeEdit {
		v.drawRange(g)
		v.drawSelection(g)
	}

	if v.mode == ModeEdit && v.editing {
		x, y, w, h := g.cellRect(t, v.row, v.col)
		p.SetStrokeStyle(s.ActiveColor)
		p.SetLineWidth(2)
		p.StrokeRect(x+1, y+1, w-2, h-2)
		if v.caretOn && !v.locked {
			st := t.StyleAt(v.row, v.col)
			p.SetFont(v.cellFont(v.row, st))
			runes := []rune(t.Cell(v.row, v.col))
			cx := x + s.PadX + p.MeasureTextWidth(string(runes[:min(v.cursor, len(runes))]))
			switch st.Align {
			case AlignCenter:
				cx = x + (w-p.MeasureTextWidth(string(runes)))/2 + p.MeasureTextWidth(string(runes[:min(v.cursor, len(runes))]))
			case AlignRight:
				cx = x + w - s.PadX - p.MeasureTextWidth(string(runes)) + p.MeasureTextWidth(string(runes[:min(v.cursor, len(runes))]))
			}
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
	v.drawFormulaTip(g)
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

// SetShowFormulas switches the table between the calculated view (the default: a document always opens on it, and the
// formula cells cannot be changed there) and the formula view. It is a view setting and is not saved.
func (v *TableView) SetShowFormulas(on bool) {
	v.ShowFormulas = on
	v.locked = v.editing && (v.Table.FieldAt(v.row, v.col) != nil || (!on && isFormula(v.Table.Cell(v.row, v.col))))
	v.tipSet = false
	v.changed()
}

// tipText is the formula to show in the tooltip of a calculated cell: the one under the pointer, or the active one.
func (v *TableView) tipText() (row, col int, text string, ok bool) {
	if v.ShowFormulas {
		return 0, 0, "", false
	}
	switch {
	case v.tipSet:
		row, col = v.tip.Row, v.tip.Col
	case v.editing && v.locked:
		row, col = v.row, v.col
	default:
		return 0, 0, "", false
	}
	t := strings.TrimSpace(v.Table.Cell(row, col))
	if !isFormula(t) {
		return 0, 0, "", false
	}
	return row, col, t, true
}

// drawFormulaTip paints the tooltip of a calculated cell: its formula, above the cell (below it at the top edge).
func (v *TableView) drawFormulaTip(g geometry) {
	row, col, text, ok := v.tipText()
	if !ok || !v.Table.InRange(row, col) || v.Painter == nil {
		return
	}
	p := v.Painter
	x, y, w, ch := g.cellRect(v.Table, row, col)
	p.SetFont("12px monospace")
	tw := p.MeasureTextWidth(text) + 16
	h := 24.0
	ty := y - h - 4
	if ty < 0 {
		ty = y + ch + 4
	}
	tx := min(x, max(x+w-tw, x)) // start at the cell's left edge
	p.SetFillStyle("#1f2328")
	p.FillRect(tx, ty, tw, h)
	p.SetFillStyle("#ffffff")
	p.SetTextAlign("left")
	p.SetTextBaseline("middle")
	p.FillText(text, tx+8, ty+h/2)
}

// trackTip notes the formula cell under the pointer (calculated view only) and reports whether that changed.
func (v *TableView) trackTip(x, y float64) bool {
	was, wasAt := v.tipSet, v.tip
	v.tipSet = false
	if !v.ShowFormulas && v.Painter != nil {
		if h := v.HitTest(x, y); h.Kind == HitCell && isFormula(v.Table.Cell(h.Row, h.Col)) {
			v.tipSet, v.tip = true, h
		}
	}
	return v.tipSet != was || (v.tipSet && v.tip != wasAt)
}

// PasteText (a Paster) pastes clipboard text into the table. A Markdown pipe table or cells separated by tabs (what a
// spreadsheet copies) fill the cells from the active one, to the right and down, adding the rows and columns the text
// needs; a cell that starts with = is a formula. Any other text goes into the active cell at the caret, on one line.
// It does nothing when no cell is being edited or the active cell is a formula held in the calculated view.
func (v *TableView) PasteText(text string) bool {
	if v.mode != ModeEdit || !v.editing || v.locked || text == "" {
		return false
	}
	var rows [][]string
	if t, ok := TableFromMarkdown(text); ok {
		rows = t.Cells
	} else if strings.Contains(text, "\t") {
		for _, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n") {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	if len(rows) == 0 {
		return v.InsertText(text)
	}
	t := v.Table
	for r, row := range rows {
		for t.Rows() <= v.row+r {
			t.AddRow()
		}
		for c, cell := range row {
			for t.Cols() <= v.col+c {
				t.AddColumn()
			}
			if t.Covered(v.row+r, v.col+c) {
				continue // a cell covered by a merge holds nothing
			}
			t.SetCell(v.row+r, v.col+c, strings.NewReplacer("\r", "", "\n", " ").Replace(strings.TrimSpace(cell)))
		}
	}
	v.cursor = len([]rune(t.Cell(v.row, v.col)))
	v.caretOn = true
	v.changed()
	return true
}

var _ Paster = (*TableView)(nil)

// Dress dresses the whole table in a style (see Table.ApplyScheme), as the table menu does, and tells the host.
func (v *TableView) Dress(s TableScheme) {
	v.Table.ApplyScheme(s)
	v.changed()
}

var editSeq uint64

// EditedAt says how recently a cell of the table was entered: a larger number is later, 0 means never. Of several tables
// the one with the largest is the one the person was last working in.
func (v *TableView) EditedAt() uint64 { return v.editedAt }
