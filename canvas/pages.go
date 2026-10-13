package canvas

import (
	"slices"
	"strconv"
	"strings"
)

// Align is horizontal text alignment within a header, footer or page
// number box.
type Align int

const (
	AlignLeft Align = iota
	AlignCenter
	AlignRight
)

// PageNumberStyle is how page numbers are written.
type PageNumberStyle int

const (
	NumArabic     PageNumberStyle = iota // 1, 2, 3
	NumLowerRoman                        // i, ii, iii
	NumUpperRoman                        // I, II, III
	NumLowerAlpha                        // a, b, c ... z, aa, ab
	NumUpperAlpha                        // A, B, C
)

// HeaderFooter is the text in a page's header or footer; '\n' starts a
// new line.
type HeaderFooter struct {
	Text  string
	Style TextStyle // zero value: DefaultTextStyle at 12px
	Align Align
	// Offset is the distance from the page edge to the text; zero
	// centres the text in the margin.
	Offset float64
	// Parts, when any is set, are the left, centre and right parts of a single line, drawn across
	// the width of the text area (a running header or footer such as "Report <tab> Draft <tab>
	// Page 3"); Text and Align are then not used.
	Parts [3]string
}

// HasParts says the header or footer is made of Parts.
func (h *HeaderFooter) HasParts() bool { return h.Parts != [3]string{} }

// PageNumberConfig turns on and formats page numbers, drawn in the
// footer area. A Page that sets one starts (or restarts) numbering with
// Start on that page; later pages that don't set their own continue
// from it, counting up by one.
type PageNumberConfig struct {
	Show   bool
	Format string // "{n}" is the number, "{total}" the page count; empty means "{n}"
	Style  PageNumberStyle
	Start  int
	Align  Align
	// TextStyle is the font; zero value: DefaultTextStyle at 12px.
	TextStyle TextStyle
}

// Page is one sheet: its size and margins, and its header, footer and
// page-number settings.
//
// Header, Footer and PageNumber are per page with inheritance: nil
// means "same as the previous page" (the first page's nil means none).
// To drop an inherited header or footer, set a HeaderFooter with empty
// Text; to stop numbering, set a PageNumberConfig with Show false.
type Page struct {
	Width, Height float64
	Margin        Insets
	Header        *HeaderFooter
	Footer        *HeaderFooter
	PageNumber    *PageNumberConfig
	// Watermark and Background inherit like the header: nil means the
	// previous page's, and an empty Watermark{} / Background{} means none
	// (from this page on).
	Watermark  *Watermark
	Background *Background
}

// PageStyle is how a PageSet draws its sheets, Google Docs-style: white
// sheets with a border and a soft shadow on a grey desk.
type PageStyle struct {
	Desk        string // fills the whole container; empty to leave transparent
	Sheet       string
	Border      string
	Shadow      string
	GuideColor  string  // margin guides, edit mode only
	TextColor   string  // header/footer text
	ShadowDelta float64 // shadow offset
	Pad         float64 // space around the sheets inside the container
}

// DefaultPageStyle is a light, Google Docs-like look.
func DefaultPageStyle() PageStyle {
	return PageStyle{
		Desk: "#e8eaed", Sheet: "#ffffff", Border: "#c4c7cd", Shadow: "rgba(0, 0, 0, 0.18)",
		GuideColor: "#b9a9ff", TextColor: "#5b616b", ShadowDelta: 3, Pad: 24,
	}
}

// PageSet is a container that lays Pages out as sheets stacked
// vertically (with Gap between them) the way a word processor shows a
// document, and draws them with their headers, footers and page
// numbers. It hands the TextFlow each page's content rectangle. In edit
// mode it also draws the margin guides.
//
// Coordinates are those of the container: sheets are centered within
// Width (or left-aligned at X+Pad if Width is zero), starting at Y+Pad.
type PageSet struct {
	Pages []Page
	X, Y  float64
	Width float64
	Gap   float64 // vertical space between sheets (NewPageSet sets 24)

	// Spread lays the sheets out two to a row, as facing pages: sheets
	// 0 and 1, 2 and 3... With SpreadBook the first sheet stands alone on
	// the right, as the first page of a book does, and the facing pairs
	// are 1 and 2, 3 and 4.... Gutter is the space between facing sheets
	// (none by default); an image anchored near the inner edge of a sheet
	// can run across the gutter onto its neighbour.
	Spread, SpreadBook bool
	Gutter             float64

	Style   PageStyle
	Painter Painter
	mode    Mode

	// OnChange is called when a header, footer or page-number setting is
	// changed by editing in edit mode.
	OnChange func(*PageSet)

	viewport [4]float64 // visible part, from SetViewport
	edit     *hfEdit    // header/footer being edited
	menu     *Menu      // page-number settings menu
	selNum   int        // page whose number is selected, when hasSel
	hasSel   bool
	caretOn  bool
	hover    string
}

// NewPageSet returns a PageSet of pages at (0, 0) in ModeView.
func NewPageSet(pages ...Page) *PageSet {
	return &PageSet{Pages: pages, Gap: 24, Style: DefaultPageStyle()}
}

// SetPainter implements PainterSetter.
func (ps *PageSet) SetPainter(p ImagePainter) { ps.Painter = p }

// SetViewport implements ViewportSetter: popups stay inside the visible part.
func (ps *PageSet) SetViewport(x, y, w, h float64) { ps.viewport = [4]float64{x, y, w, h} }

// Mode returns the current mode.
func (ps *PageSet) Mode() Mode { return ps.mode }

// SetMode switches mode; it reports whether a redraw is needed.
func (ps *PageSet) SetMode(m Mode) bool {
	if m == ps.mode {
		return false
	}
	ps.mode = m
	if m == ModeView {
		ps.edit, ps.hasSel, ps.hover = nil, false, ""
		if ps.menu != nil {
			ps.menu.Close()
		}
	}
	return true
}

// rows groups the sheets into the rows they are laid out in: one sheet
// each, or two facing sheets in a spread. A -1 is the empty left slot of
// a book's first page.
func (ps *PageSet) rows() [][]int {
	var rows [][]int
	n := len(ps.Pages)
	switch {
	case !ps.Spread:
		for i := range n {
			rows = append(rows, []int{i})
		}
	case ps.SpreadBook:
		if n > 0 {
			rows = append(rows, []int{-1, 0})
		}
		for i := 1; i < n; i += 2 {
			if i+1 < n {
				rows = append(rows, []int{i, i + 1})
			} else {
				rows = append(rows, []int{i})
			}
		}
	default:
		for i := 0; i < n; i += 2 {
			if i+1 < n {
				rows = append(rows, []int{i, i + 1})
			} else {
				rows = append(rows, []int{i})
			}
		}
	}
	return rows
}

// rowSize returns the width and height of a row: the sheets side by side
// (an empty slot as wide as its neighbour), the tallest one's height.
func (ps *PageSet) rowSize(row []int) (w, h float64) {
	for k, i := range row {
		if i < 0 {
			continue
		}
		if k > 0 {
			w += ps.Gutter
		}
		w += ps.Pages[i].Width
		h = max(h, ps.Pages[i].Height)
	}
	if len(row) == 2 && row[0] < 0 {
		w += ps.Pages[row[1]].Width + ps.Gutter
	}
	return w, h
}

// Size returns the width and height needed to show every page.
func (ps *PageSet) Size() (w, h float64) {
	pad := ps.Style.Pad
	for r, row := range ps.rows() {
		rw, rh := ps.rowSize(row)
		w = max(w, rw+2*pad)
		h += rh
		if r > 0 {
			h += ps.Gap
		}
	}
	return max(w, ps.Width), h + 2*pad
}

// PageRect returns sheet i's rectangle: x, y, w, h.
func (ps *PageSet) PageRect(i int) (x, y, w, h float64) {
	pg := ps.Pages[i]
	y = ps.Y + ps.Style.Pad
	for _, row := range ps.rows() {
		rw, rh := ps.rowSize(row)
		if k := slices.Index(row, i); k >= 0 {
			x = ps.X + ps.Style.Pad
			if ps.Width > 0 {
				x = ps.X + (ps.Width-rw)/2
			}
			for _, j := range row[:k] {
				if j >= 0 {
					x += ps.Pages[j].Width + ps.Gutter
				} else {
					x += ps.Pages[row[1]].Width + ps.Gutter
				}
			}
			return x, y, pg.Width, pg.Height
		}
		y += rh + ps.Gap
	}
	return x, y, pg.Width, pg.Height
}

// PageAt returns the index of the first sheet of the row containing y, or
// -1 (y in a gap or outside the pages).
func (ps *PageSet) PageAt(y float64) int {
	for _, row := range ps.rows() {
		first := row[len(row)-1]
		if row[0] >= 0 {
			first = row[0]
		}
		_, py, _, _ := ps.PageRect(first)
		_, rh := ps.rowSize(row)
		if y >= py && y < py+rh {
			return first
		}
	}
	return -1
}

// ResolvedHeader returns page i's effective header (its own, else the
// nearest earlier page's), or nil if there is none or it is empty.
func (ps *PageSet) ResolvedHeader(i int) *HeaderFooter {
	for j := i; j >= 0; j-- {
		if h := ps.Pages[j].Header; h != nil {
			if h.Text == "" && !h.HasParts() {
				return nil
			}
			return h
		}
	}
	return nil
}

// ResolvedFooter is ResolvedHeader for footers.
func (ps *PageSet) ResolvedFooter(i int) *HeaderFooter {
	for j := i; j >= 0; j-- {
		if f := ps.Pages[j].Footer; f != nil {
			if f.Text == "" && !f.HasParts() {
				return nil
			}
			return f
		}
	}
	return nil
}

// ResolvedPageNumber returns page i's effective page-number config and
// the number to print on it; cfg is nil if numbering was never
// configured or is switched off.
func (ps *PageSet) ResolvedPageNumber(i int) (cfg *PageNumberConfig, n int) {
	for j := i; j >= 0; j-- {
		if c := ps.Pages[j].PageNumber; c != nil {
			if !c.Show {
				return nil, 0
			}
			return c, c.Start + (i - j)
		}
	}
	return nil, 0
}

// PageNumberText returns the text of page i's number ("Page 3", "iv",
// ...), or "" if it has none.
func (ps *PageSet) PageNumberText(i int) string {
	cfg, n := ps.ResolvedPageNumber(i)
	if cfg == nil {
		return ""
	}
	format := cfg.Format
	if format == "" {
		format = "{n}"
	}
	format = strings.ReplaceAll(format, "{total}", strconv.Itoa(len(ps.Pages)))
	return strings.ReplaceAll(format, "{n}", formatPageNumber(n, cfg.Style))
}

func formatPageNumber(n int, style PageNumberStyle) string {
	switch style {
	case NumLowerRoman:
		return strings.ToLower(roman(n))
	case NumUpperRoman:
		return roman(n)
	case NumLowerAlpha:
		return alpha(n, 'a')
	case NumUpperAlpha:
		return alpha(n, 'A')
	}
	return strconv.Itoa(n)
}

// roman writes n (>= 1) in upper-case Roman numerals; other values fall
// back to Arabic.
func roman(n int) string {
	if n < 1 || n >= 4000 {
		return strconv.Itoa(n)
	}
	var b strings.Builder
	for _, s := range [...]struct {
		v int
		s string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
		for n >= s.v {
			b.WriteString(s.s)
			n -= s.v
		}
	}
	return b.String()
}

// alpha writes n (>= 1) as a, b, ... z, aa, ab, ...; other values fall
// back to Arabic.
func alpha(n int, base rune) string {
	if n < 1 {
		return strconv.Itoa(n)
	}
	var out []rune
	for n > 0 {
		n--
		out = append([]rune{base + rune(n%26)}, out...)
		n /= 26
	}
	return string(out)
}

// footerStyle is the font used for text drawn in headers/footers/page
// numbers when the style is unset.
func footerStyle(s TextStyle) TextStyle {
	if s == (TextStyle{}) {
		s = DefaultTextStyle()
		s.FontSize, s.LineHeight = 12, 16
	}
	return s
}

// textBlockHeight returns the height of text with n lines in style s.
func textBlockHeight(s TextStyle, n int) float64 { return float64(n) * footerStyle(s).Leading() }

// ContentRect returns the rectangle of page i that body text flows
// into: the sheet minus its margins, shrunk further if the header or
// footer (or page number) is too tall to fit in its margin.
func (ps *PageSet) ContentRect(i int) (x, y, w, h float64) {
	px, py, pw, ph := ps.PageRect(i)
	pg := ps.Pages[i]
	top, bottom := pg.Margin.Top, pg.Margin.Bottom
	hd, ft := ps.effHeader(i), ps.effFooter(i)
	if hd != nil {
		off := ps.headerOffset(hd, pg.Margin.Top)
		top = max(top, off+textBlockHeight(hd.Style, strings.Count(hd.Text, "\n")+1)+6)
	}
	footH := 0.0
	if ft != nil {
		footH = textBlockHeight(ft.Style, strings.Count(ft.Text, "\n")+1)
	}
	if cfg, _ := ps.ResolvedPageNumber(i); cfg != nil {
		footH = max(footH, textBlockHeight(cfg.TextStyle, 1))
	}
	if footH > 0 {
		off := ps.footerOffset(footH, pg.Margin.Bottom, ft)
		bottom = max(bottom, off+footH+6)
	}
	return px + pg.Margin.Left, py + top, pw - pg.Margin.Left - pg.Margin.Right, ph - top - bottom
}

// headerOffset is the distance from the top edge to the header text.
func (ps *PageSet) headerOffset(h *HeaderFooter, topMargin float64) float64 {
	if h.Offset > 0 {
		return h.Offset
	}
	return max((topMargin-textBlockHeight(h.Style, strings.Count(h.Text, "\n")+1))/2, 4)
}

// footerOffset is the distance from the bottom edge to the bottom of
// the footer text block of height footH.
func (ps *PageSet) footerOffset(footH, bottomMargin float64, f *HeaderFooter) float64 {
	if f != nil && f.Offset > 0 {
		return f.Offset
	}
	return max((bottomMargin-footH)/2, 4)
}

// Draw paints the desk, then each sheet with its header, footer and
// page number; in edit mode also each sheet's margin guides.
func (ps *PageSet) Draw() {
	p, s := ps.Painter, ps.Style
	w, h := ps.Size()
	if s.Desk != "" {
		p.SetFillStyle(s.Desk)
		p.FillRect(ps.X, ps.Y, w, h)
	}
	for i := range ps.Pages {
		x, y, pw, phh := ps.PageRect(i)
		p.SetFillStyle(s.Shadow)
		p.FillRect(x+s.ShadowDelta, y+s.ShadowDelta, pw, phh)
		p.SetFillStyle(s.Sheet)
		p.FillRect(x, y, pw, phh)
		ps.drawBackdrop(i)
		p.SetStrokeStyle(s.Border)
		p.SetLineWidth(1)
		p.StrokeRect(x+0.5, y+0.5, pw-1, phh-1)
		ps.drawHeaderFooter(i)
		if ps.mode == ModeEdit {
			cx, cy, cw, ch := ps.ContentRect(i)
			p.SetStrokeStyle(s.GuideColor)
			p.StrokeRect(cx+0.5, cy+0.5, cw-1, ch-1)
		}
	}
}

func (ps *PageSet) drawHeaderFooter(i int) {
	bx := ps.boxes(i)
	for _, b := range []*textBox{bx.header, bx.footer, bx.number} {
		if b != nil {
			ps.drawBox(b)
		}
	}
}

var _ Component = (*PageSet)(nil)

// Anchor pins a floating object to a page: (X, Y) is the position of
// the object's top-left corner relative to the top-left of Page's sheet,
// so the object moves with the page.
type Anchor struct {
	Page int
	X, Y float64
}

// Resolve converts a to absolute coordinates (those of the PageSet's
// container). A Page out of range is clamped.
func (ps *PageSet) Resolve(a Anchor) (x, y float64) {
	if len(ps.Pages) == 0 {
		return a.X, a.Y
	}
	px, py, _, _ := ps.PageRect(min(max(a.Page, 0), len(ps.Pages)-1))
	return px + a.X, py + a.Y
}

// AnchorAt anchors the point (x, y) (absolute) to the page it is on, or
// -- if it is in a gap or off the pages -- the nearest one.
func (ps *PageSet) AnchorAt(x, y float64) Anchor {
	if len(ps.Pages) == 0 {
		return Anchor{X: x, Y: y}
	}
	best, bestDist := 0, 1e18
	for i := range ps.Pages {
		px, py, pw, ph := ps.PageRect(i)
		dx, dy := 0.0, 0.0
		switch {
		case y < py:
			dy = py - y
		case y >= py+ph:
			dy = y - (py + ph)
		}
		switch {
		case x < px:
			dx = px - x
		case x >= px+pw:
			dx = x - (px + pw)
		}
		d := dy*dy + dx*dx
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	px, py, _, _ := ps.PageRect(best)
	return Anchor{Page: best, X: x - px, Y: y - py}
}
