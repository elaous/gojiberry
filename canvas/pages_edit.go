package canvas

import (
	"strings"
)

// textBox is a block of header/footer/page-number text laid out in a page.
type textBox struct {
	lines       []string
	orig        TextStyle // as configured; its Color overrides the page style's
	style       TextStyle // with defaults filled in
	align       Align
	left, right float64
	top         float64 // top of the first line
}

func (b *textBox) lineH() float64 { return b.style.Leading() }

type pageBoxes struct{ header, footer, number *textBox }

func (ps *PageSet) mkBox(hf *HeaderFooter, text string, left, right, top float64) *textBox {
	return &textBox{
		lines: strings.Split(text, "\n"), orig: hf.Style, style: footerStyle(hf.Style),
		align: hf.Align, left: left, right: right, top: top,
	}
}

// lineX returns the x a line of the box starts at.
func (ps *PageSet) lineX(b *textBox, line string) float64 {
	ps.Painter.SetFont(b.style.Font())
	switch b.align {
	case AlignCenter:
		return (b.left+b.right)/2 - ps.Painter.MeasureTextWidth(line)/2
	case AlignRight:
		return b.right - ps.Painter.MeasureTextWidth(line)
	}
	return b.left
}

// boxes lays out page i's header, footer and page number.
func (ps *PageSet) boxes(i int) pageBoxes {
	px, py, pw, ph := ps.PageRect(i)
	pg := ps.Pages[i]
	left, right := px+pg.Margin.Left, px+pw-pg.Margin.Right
	var out pageBoxes

	hd := ps.effHeader(i)
	if hd == nil && ps.editing(i, false) {
		hd = &HeaderFooter{} // an empty header being typed into
	}
	if hd != nil {
		out.header = ps.mkBox(hd, hd.Text, left, right, py+ps.headerOffset(hd, pg.Margin.Top))
	}

	ft := ps.effFooter(i)
	if ft == nil && ps.editing(i, true) {
		ft = &HeaderFooter{}
	}
	num, _ := ps.ResolvedPageNumber(i)
	footH := 0.0
	if ft != nil {
		footH = textBlockHeight(ft.Style, strings.Count(ft.Text, "\n")+1)
	}
	if num != nil {
		footH = max(footH, textBlockHeight(num.TextStyle, 1))
	}
	if footH == 0 {
		return out
	}
	bottom := py + ph - ps.footerOffset(footH, pg.Margin.Bottom, ft)
	if ft != nil {
		out.footer = ps.mkBox(ft, ft.Text, left, right, bottom-textBlockHeight(ft.Style, strings.Count(ft.Text, "\n")+1))
	}
	if num != nil {
		nb := &HeaderFooter{Style: num.TextStyle, Align: num.Align}
		out.number = ps.mkBox(nb, ps.PageNumberText(i), left, right, bottom-textBlockHeight(num.TextStyle, 1))
	}
	return out
}

func (ps *PageSet) drawBox(b *textBox) {
	p := ps.Painter
	p.SetFont(b.style.Font())
	if b.orig.Color != "" {
		p.SetFillStyle(b.orig.Color)
	} else {
		p.SetFillStyle(ps.Style.TextColor)
	}
	p.SetTextBaseline("middle")
	p.SetTextAlign("left")
	lh := b.lineH()
	for k, line := range b.lines {
		p.FillText(line, ps.lineX(b, line), b.top+float64(k)*lh+lh/2)
	}
}

// numberRect returns the on-page rectangle of page i's number text
// (x, y, w, h), and whether it has one.
func (ps *PageSet) numberRect(i int) ([4]float64, bool) {
	b := ps.boxes(i).number
	if b == nil {
		return [4]float64{}, false
	}
	text := b.lines[0]
	ps.Painter.SetFont(b.style.Font())
	return [4]float64{ps.lineX(b, text), b.top, ps.Painter.MeasureTextWidth(text), b.lineH()}, true
}

// PageNumberRect returns where page i's number text is drawn (x, y, w,
// h in the PageSet's coordinates), and whether the page has one.
func (ps *PageSet) PageNumberRect(i int) (x, y, w, h float64, ok bool) {
	r, ok := ps.numberRect(i)
	return r[0], r[1], r[2], r[3], ok
}

// zone returns the part of page i where its header (above the text
// area) or footer (below it) lives: x, y, w, h.
func (ps *PageSet) zone(i int, footer bool) [4]float64 {
	px, py, pw, ph := ps.PageRect(i)
	pg := ps.Pages[i]
	_, cy, _, ch := ps.ContentRect(i)
	left, w := px+pg.Margin.Left, pw-pg.Margin.Left-pg.Margin.Right
	if footer {
		return [4]float64{left, cy + ch, w, py + ph - (cy + ch)}
	}
	return [4]float64{left, py, w, cy - py}
}

// --- header/footer editing -------------------------------------------

// hfEdit is a header or footer being edited. The page's own
// Header/Footer is only created at the first change, so just looking at
// an inherited one doesn't break the inheritance.
type hfEdit struct {
	page   int
	footer bool
	buf    []rune
	caret  int
	base   HeaderFooter // style, alignment and offset it started from
}

func (ps *PageSet) editing(page int, footer bool) bool {
	return ps.edit != nil && ps.edit.page == page && ps.edit.footer == footer
}

// effHeader is page i's header as drawn: while it is being edited, the
// edit buffer.
func (ps *PageSet) effHeader(i int) *HeaderFooter {
	if ps.editing(i, false) {
		return ps.edit.current()
	}
	return ps.ResolvedHeader(i)
}

func (ps *PageSet) effFooter(i int) *HeaderFooter {
	if ps.editing(i, true) {
		return ps.edit.current()
	}
	return ps.ResolvedFooter(i)
}

// current returns the header/footer the buffer represents (nil if empty).
func (e *hfEdit) current() *HeaderFooter {
	if len(e.buf) == 0 {
		return nil
	}
	h := e.base
	h.Text = string(e.buf)
	return &h
}

// BeginEdit starts editing page i's header (or footer) as a double
// click would.
func (ps *PageSet) BeginEdit(page int, footer bool) {
	var base HeaderFooter
	if footer {
		if f := ps.ResolvedFooter(page); f != nil {
			base = *f
		}
	} else if h := ps.ResolvedHeader(page); h != nil {
		base = *h
	}
	ps.edit = &hfEdit{page: page, footer: footer, buf: []rune(base.Text), caret: len([]rune(base.Text)), base: base}
	ps.hasSel = false
	ps.caretOn = true
}

// EndEdit stops editing the header/footer.
func (ps *PageSet) EndEdit() { ps.edit = nil }

// Editing returns the page and kind of the header/footer being edited.
func (ps *PageSet) Editing() (page int, footer, ok bool) {
	if ps.edit == nil {
		return 0, false, false
	}
	return ps.edit.page, ps.edit.footer, true
}

func (ps *PageSet) changed() {
	if ps.OnChange != nil {
		ps.OnChange(ps)
	}
}

// commit gives the edited page its own header/footer holding the buffer
// (an empty one removes the header/footer from this page on).
func (ps *PageSet) commit() {
	e := ps.edit
	h := e.base
	h.Text = string(e.buf)
	if e.footer {
		ps.Pages[e.page].Footer = &h
	} else {
		ps.Pages[e.page].Header = &h
	}
	ps.changed()
}

// ownHF makes the edited page's header/footer its own (not inherited)
// and returns it.
func (ps *PageSet) ownHF(page int, footer bool) *HeaderFooter {
	slot := &ps.Pages[page].Header
	resolved := ps.ResolvedHeader(page)
	if footer {
		slot, resolved = &ps.Pages[page].Footer, ps.ResolvedFooter(page)
	}
	if *slot == nil {
		h := HeaderFooter{}
		if resolved != nil {
			h = *resolved
		}
		*slot = &h
	}
	return *slot
}

func (ps *PageSet) insertRunes(rs []rune) {
	e := ps.edit
	e.buf = append(e.buf[:e.caret], append(append([]rune{}, rs...), e.buf[e.caret:]...)...)
	e.caret += len(rs)
	ps.caretOn = true
	ps.commit()
}

// InsertText types into the header/footer being edited.
func (ps *PageSet) InsertText(s string) bool {
	s = strings.ReplaceAll(s, "\r", "")
	if ps.mode != ModeEdit || ps.edit == nil || s == "" {
		return false
	}
	ps.insertRunes([]rune(s))
	return true
}

// lineStart returns the offset in e.buf where the line containing off starts.
func (e *hfEdit) lineStart(off int) int {
	for off > 0 && e.buf[off-1] != '\n' {
		off--
	}
	return off
}

func (e *hfEdit) lineEnd(off int) int {
	for off < len(e.buf) && e.buf[off] != '\n' {
		off++
	}
	return off
}

// HandleKey handles keys while a header/footer is being edited or the
// page-number menu is open.
func (ps *PageSet) HandleKey(key string, shift bool) bool {
	if ps.mode != ModeEdit {
		return false
	}
	if ps.menu != nil && ps.menu.IsOpen() {
		return ps.menu.HandleKey(key)
	}
	e := ps.edit
	if e == nil {
		if key == "Escape" && ps.hasSel {
			ps.hasSel = false
			return true
		}
		return false
	}
	switch key {
	case "ArrowLeft":
		e.caret = max(e.caret-1, 0)
	case "ArrowRight":
		e.caret = min(e.caret+1, len(e.buf))
	case "Home":
		e.caret = e.lineStart(e.caret)
	case "End":
		e.caret = e.lineEnd(e.caret)
	case "ArrowUp":
		if ls := e.lineStart(e.caret); ls > 0 {
			col := e.caret - ls
			prev := e.lineStart(ls - 1)
			e.caret = min(prev+col, ls-1)
		}
	case "ArrowDown":
		if le := e.lineEnd(e.caret); le < len(e.buf) {
			col := e.caret - e.lineStart(e.caret)
			next := le + 1
			e.caret = min(next+col, e.lineEnd(next))
		}
	case "Backspace":
		if e.caret > 0 {
			e.buf = append(e.buf[:e.caret-1], e.buf[e.caret:]...)
			e.caret--
			ps.commit()
		}
	case "Delete":
		if e.caret < len(e.buf) {
			e.buf = append(e.buf[:e.caret], e.buf[e.caret+1:]...)
			ps.commit()
		}
	case "Enter":
		ps.insertRunes([]rune{'\n'})
	case "Escape":
		ps.edit = nil
	default:
		return false
	}
	ps.caretOn = true
	return true
}

// WantsFocus implements Focuser.
func (ps *PageSet) WantsFocus() bool {
	return ps.mode == ModeEdit && (ps.edit != nil || (ps.menu != nil && ps.menu.IsOpen()))
}

// SetCaretVisible implements Blinker.
func (ps *PageSet) SetCaretVisible(on bool) bool {
	changed := ps.caretOn != on && ps.edit != nil
	ps.caretOn = on
	return changed
}

// --- badge ------------------------------------------------------------

type badgeButton struct {
	label string
	rect  [4]float64
	do    func()
}

const (
	badgeH    = 18
	badgeFont = "11px sans-serif"
)

// badge lays out the strip shown next to the header/footer being edited:
// a status label and its buttons.
func (ps *PageSet) badge() (label string, labelRect [4]float64, buttons []badgeButton) {
	e := ps.edit
	px, py, _, ph := ps.PageRect(e.page)
	pg := ps.Pages[e.page]
	x := px + pg.Margin.Left
	y := py - badgeH - 3
	name, own := "Header", pg.Header != nil
	if e.footer {
		y = py + ph + 3
		name, own = "Footer", pg.Footer != nil
	}
	status := "this page"
	if !own {
		status = "same as previous"
		if e.page == 0 {
			status = "all pages"
		}
	}
	label = name + " · " + status
	ps.Painter.SetFont(badgeFont)
	lw := ps.Painter.MeasureTextWidth(label) + 14
	labelRect = [4]float64{x, y, lw, badgeH}
	x += lw + 4

	add := func(text string, do func()) {
		w := ps.Painter.MeasureTextWidth(text) + 14
		buttons = append(buttons, badgeButton{text, [4]float64{x, y, w, badgeH}, do})
		x += w + 4
	}
	page, footer := e.page, e.footer
	if own && page > 0 {
		add("Use previous", func() {
			if footer {
				ps.Pages[page].Footer = nil
			} else {
				ps.Pages[page].Header = nil
			}
			ps.edit = nil
			ps.changed()
		})
	}
	for _, a := range []struct {
		l string
		a Align
	}{{"Left", AlignLeft}, {"Center", AlignCenter}, {"Right", AlignRight}} {
		a := a
		add(a.l, func() {
			ps.ownHF(page, footer).Align = a.a
			ps.edit.base.Align = a.a
			ps.changed()
		})
	}
	if footer {
		text := "# Page number"
		if cfg, _ := ps.ResolvedPageNumber(page); cfg != nil {
			text = "Hide page number"
		}
		add(text, func() { ps.updateNumber(page, func(c *PageNumberConfig) { c.Show = !c.Show }) })
	}
	return label, labelRect, buttons
}

// --- page-number settings ----------------------------------------------

// updateNumber changes page i's page-number settings. The page gets a
// config of its own (a copy of the one it inherited, numbered so this
// page's number doesn't change), which later pages then inherit.
func (ps *PageSet) updateNumber(i int, fn func(*PageNumberConfig)) {
	var c PageNumberConfig
	cfg, n := ps.ResolvedPageNumber(i)
	switch prev := ps.latestNumberConfig(i); {
	case cfg != nil: // numbering is on: copy it, numbered so this page keeps its number
		c = *cfg
		c.Start = n
	case prev != nil: // switched off earlier: turning it on again reuses that look
		c = *prev
		c.Start, c.Show = i+1, false
	default: // never configured
		c = PageNumberConfig{Format: "{n}", Align: AlignCenter, Start: i + 1}
	}
	fn(&c)
	ps.Pages[i].PageNumber = &c
	ps.changed()
}

// latestNumberConfig is the most recent explicit config at or before page i.
func (ps *PageSet) latestNumberConfig(i int) *PageNumberConfig {
	for j := i; j >= 0; j-- {
		if c := ps.Pages[j].PageNumber; c != nil {
			return c
		}
	}
	return nil
}

// SelectedPageNumber returns the page whose number is selected.
func (ps *PageSet) SelectedPageNumber() (page int, ok bool) { return ps.selNum, ps.hasSel }

// Menu returns the page-number settings menu (open while it is showing).
func (ps *PageSet) Menu() *Menu {
	if ps.menu == nil {
		ps.menu = NewMenu(ps.Painter)
	}
	ps.menu.Painter = ps.Painter
	return ps.menu
}

func check(on bool, label string) string {
	if on {
		return "✓ " + label
	}
	return "   " + label
}

// numberMenu builds the page-number settings menu for page i.
func (ps *PageSet) numberMenu(i int) []MenuItem {
	cfg, _ := ps.ResolvedPageNumber(i)
	if cfg == nil {
		cfg = &PageNumberConfig{}
	}
	set := func(fn func(*PageNumberConfig)) func() { return func() { ps.updateNumber(i, fn) } }
	format := cfg.Format
	if format == "" {
		format = "{n}"
	}
	items := []MenuItem{
		{Label: "Hide page number", Action: set(func(c *PageNumberConfig) { c.Show = false })},
		{Separator: true},
	}
	for _, a := range []struct {
		l string
		a Align
	}{{"Align left", AlignLeft}, {"Align center", AlignCenter}, {"Align right", AlignRight}} {
		a := a
		items = append(items, MenuItem{Label: check(cfg.Align == a.a, a.l), Action: set(func(c *PageNumberConfig) { c.Align = a.a })})
	}
	items = append(items, MenuItem{Separator: true})
	for _, st := range []struct {
		l string
		s PageNumberStyle
	}{{"1, 2, 3", NumArabic}, {"i, ii, iii", NumLowerRoman}, {"I, II, III", NumUpperRoman}, {"a, b, c", NumLowerAlpha}, {"A, B, C", NumUpperAlpha}} {
		st := st
		items = append(items, MenuItem{Label: check(cfg.Style == st.s, st.l), Action: set(func(c *PageNumberConfig) { c.Style = st.s })})
	}
	items = append(items, MenuItem{Separator: true})
	for _, f := range []string{"{n}", "Page {n}", "Page {n} of {total}"} {
		f := f
		items = append(items, MenuItem{Label: check(format == f, f), Action: set(func(c *PageNumberConfig) { c.Format = f })})
	}
	own := ps.Pages[i].PageNumber != nil
	items = append(items,
		MenuItem{Separator: true},
		MenuItem{Label: "Restart numbering at 1 here", Action: set(func(c *PageNumberConfig) { c.Start = 1 })},
		MenuItem{Label: "Same as previous page", Disabled: i == 0 || !own, Action: func() {
			ps.Pages[i].PageNumber = nil
			ps.changed()
		}},
	)
	return items
}

// --- pointer -----------------------------------------------------------

func (ps *PageSet) pageAtPoint(x, y float64) int {
	for i := range ps.Pages {
		px, py, pw, ph := ps.PageRect(i)
		if inRect(x, y, [4]float64{px, py, pw, ph}) {
			return i
		}
	}
	return -1
}

// DoubleClick starts editing the header or footer of the page whose
// header/footer area was double-clicked (edit mode).
func (ps *PageSet) DoubleClick(x, y float64) bool {
	if ps.mode != ModeEdit || (ps.menu != nil && ps.menu.IsOpen()) {
		return false
	}
	i := ps.pageAtPoint(x, y)
	if i < 0 {
		return false
	}
	if r, ok := ps.numberRect(i); ok && inRect(x, y, r) {
		ps.selectNumber(i, r)
		return true
	}
	switch {
	case inRect(x, y, ps.zone(i, false)):
		ps.BeginEdit(i, false)
	case inRect(x, y, ps.zone(i, true)):
		ps.BeginEdit(i, true)
	default:
		return false
	}
	return true
}

func (ps *PageSet) selectNumber(i int, r [4]float64) {
	ps.edit = nil
	ps.selNum, ps.hasSel = i, true
	ps.Menu().OpenWithin(ps.numberMenu(i), r[0], r[1]+r[3]+4, ps.viewport)
}

// MouseDown handles the page-number menu, the edit badge, caret
// placement in a header/footer being edited, and selecting a page number.
func (ps *PageSet) MouseDown(x, y float64) bool {
	if ps.mode != ModeEdit {
		return false
	}
	if ps.menu != nil && ps.menu.MouseDown(x, y) {
		return true
	}
	if ps.edit != nil {
		_, lr, buttons := ps.badge()
		for _, b := range buttons {
			if inRect(x, y, b.rect) {
				b.do()
				return true
			}
		}
		if inRect(x, y, lr) {
			return true
		}
		if inRect(x, y, ps.zone(ps.edit.page, ps.edit.footer)) {
			ps.edit.caret = ps.caretAt(x, y)
			ps.caretOn = true
			return true
		}
		ps.edit = nil // a click elsewhere ends the edit, then acts normally
		changed := true
		if i := ps.pageAtPoint(x, y); i >= 0 {
			if r, ok := ps.numberRect(i); ok && inRect(x, y, r) {
				ps.selectNumber(i, r)
			}
		}
		return changed
	}
	if i := ps.pageAtPoint(x, y); i >= 0 {
		if r, ok := ps.numberRect(i); ok && inRect(x, y, r) {
			ps.selectNumber(i, r)
			return true
		}
	}
	was := ps.hasSel
	ps.hasSel = false
	return was
}

// caretAt returns the buffer offset nearest (x, y) in the edited text.
func (ps *PageSet) caretAt(x, y float64) int {
	e := ps.edit
	b := ps.editBox()
	if b == nil {
		return len(e.buf)
	}
	k := int((y - b.top) / b.lineH())
	k = min(max(k, 0), len(b.lines)-1)
	off := 0
	for j := 0; j < k; j++ {
		off += len([]rune(b.lines[j])) + 1
	}
	line := []rune(b.lines[k])
	lx := ps.lineX(b, b.lines[k])
	ps.Painter.SetFont(b.style.Font())
	prev := 0.0
	for c := 1; c <= len(line); c++ {
		w := ps.Painter.MeasureTextWidth(string(line[:c]))
		if x < lx+(prev+w)/2 {
			return off + c - 1
		}
		prev = w
	}
	return off + len(line)
}

func (ps *PageSet) editBox() *textBox {
	e := ps.edit
	if e == nil {
		return nil
	}
	if e.footer {
		return ps.boxes(e.page).footer
	}
	return ps.boxes(e.page).header
}

// MouseMove tracks the pointer for the cursor.
func (ps *PageSet) MouseMove(x, y float64) bool {
	if ps.mode != ModeEdit {
		return false
	}
	var next string
	if ps.menu != nil && ps.menu.IsOpen() {
		if ps.menu.MouseMove(x, y) {
			return true
		}
	}
	if i := ps.pageAtPoint(x, y); i >= 0 {
		switch r, ok := ps.numberRect(i); {
		case ok && inRect(x, y, r):
			next = "pointer"
		case inRect(x, y, ps.zone(i, false)) || inRect(x, y, ps.zone(i, true)):
			next = "text"
		}
	}
	changed := next != ps.hover
	ps.hover = next
	return changed
}

// MouseLeave clears the hover cursor.
func (ps *PageSet) MouseLeave() bool {
	changed := ps.hover != ""
	ps.hover = ""
	return changed
}

// CSSCursor implements CursorProvider.
func (ps *PageSet) CSSCursor() string {
	if ps.mode != ModeEdit {
		return ""
	}
	return ps.hover
}

// --- overlay -----------------------------------------------------------

// DrawOverlay paints the editing chrome -- the edited area's outline and
// caret, its badge, the selected page number's outline -- and the
// page-number menu, above everything else a Stage draws.
func (ps *PageSet) DrawOverlay() {
	if ps.mode != ModeEdit {
		return
	}
	p, s := ps.Painter, ps.Style
	accent := "#7c5cff"

	if ps.hasSel {
		if r, ok := ps.numberRect(ps.selNum); ok {
			p.SetStrokeStyle(accent)
			p.SetLineWidth(2)
			p.StrokeRect(r[0]-3, r[1]-1, r[2]+6, r[3]+2)
		}
	}

	if e := ps.edit; e != nil {
		z := ps.zone(e.page, e.footer)
		p.SetStrokeStyle(accent)
		p.SetLineWidth(1)
		p.StrokeRect(z[0]+0.5, z[1]+0.5, z[2]-1, z[3]-1)
		if b := ps.editBox(); b != nil && ps.caretOn {
			off, line := e.caret, 0
			for line < len(b.lines)-1 && off > len([]rune(b.lines[line])) {
				off -= len([]rune(b.lines[line])) + 1
				line++
			}
			text := []rune(b.lines[line])
			p.SetFont(b.style.Font())
			cx := ps.lineX(b, b.lines[line]) + p.MeasureTextWidth(string(text[:min(off, len(text))]))
			p.SetFillStyle(accent)
			p.FillRect(cx, b.top+float64(line)*b.lineH()+1, 1.5, b.lineH()-2)
		}

		label, lr, buttons := ps.badge()
		p.SetFont(badgeFont)
		p.SetTextBaseline("middle")
		p.SetTextAlign("left")
		p.SetFillStyle(accent)
		p.FillRect(lr[0], lr[1], lr[2], lr[3])
		p.SetFillStyle("#ffffff")
		p.FillText(label, lr[0]+7, lr[1]+lr[3]/2)
		for _, b := range buttons {
			p.SetFillStyle("#eceef1")
			p.FillRect(b.rect[0], b.rect[1], b.rect[2], b.rect[3])
			p.SetStrokeStyle(s.Border)
			p.StrokeRect(b.rect[0]+0.5, b.rect[1]+0.5, b.rect[2]-1, b.rect[3]-1)
			p.SetFillStyle("#2e3338")
			p.FillText(b.label, b.rect[0]+7, b.rect[1]+b.rect[3]/2)
		}
	}
	if ps.menu != nil {
		ps.menu.Painter = p.(ImagePainter)
		ps.menu.Draw()
	}
}

var (
	_ Overlayer = (*PageSet)(nil)
)
