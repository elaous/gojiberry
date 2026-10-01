package canvas

import (
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
}

// PageNumberConfig turns on and formats page numbers, drawn in the
// footer area. A Page that sets one starts (or restarts) numbering with
// Start on that page; later pages that don't set their own continue
// from it, counting up by one.
type PageNumberConfig struct {
	Show   bool
	Format string // "{n}" is replaced by the number; empty means "{n}"
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

	Style   PageStyle
	Painter Painter
	mode    Mode
}

// NewPageSet returns a PageSet of pages at (0, 0) in ModeView.
func NewPageSet(pages ...Page) *PageSet {
	return &PageSet{Pages: pages, Gap: 24, Style: DefaultPageStyle()}
}

// SetPainter implements PainterSetter.
func (ps *PageSet) SetPainter(p ImagePainter) { ps.Painter = p }

// Mode returns the current mode.
func (ps *PageSet) Mode() Mode { return ps.mode }

// SetMode switches mode; it reports whether a redraw is needed.
func (ps *PageSet) SetMode(m Mode) bool {
	if m == ps.mode {
		return false
	}
	ps.mode = m
	return true
}

// Size returns the width and height needed to show every page.
func (ps *PageSet) Size() (w, h float64) {
	pad := ps.Style.Pad
	for i, pg := range ps.Pages {
		w = max(w, pg.Width+2*pad)
		h += pg.Height
		if i > 0 {
			h += ps.Gap
		}
	}
	return max(w, ps.Width), h + 2*pad
}

// PageRect returns sheet i's rectangle: x, y, w, h.
func (ps *PageSet) PageRect(i int) (x, y, w, h float64) {
	pg := ps.Pages[i]
	y = ps.Y + ps.Style.Pad
	for j := range i {
		y += ps.Pages[j].Height + ps.Gap
	}
	x = ps.X + ps.Style.Pad
	if ps.Width > 0 {
		x = ps.X + (ps.Width-pg.Width)/2
	}
	return x, y, pg.Width, pg.Height
}

// PageAt returns the index of the sheet containing y, or -1 (y in a
// gap or outside the pages).
func (ps *PageSet) PageAt(y float64) int {
	for i := range ps.Pages {
		_, py, _, ph := ps.PageRect(i)
		if y >= py && y < py+ph {
			return i
		}
	}
	return -1
}

// ResolvedHeader returns page i's effective header (its own, else the
// nearest earlier page's), or nil if there is none or it is empty.
func (ps *PageSet) ResolvedHeader(i int) *HeaderFooter {
	for j := i; j >= 0; j-- {
		if h := ps.Pages[j].Header; h != nil {
			if h.Text == "" {
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
			if f.Text == "" {
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
	if hd := ps.ResolvedHeader(i); hd != nil {
		off := ps.headerOffset(hd, pg.Margin.Top)
		top = max(top, off+textBlockHeight(hd.Style, strings.Count(hd.Text, "\n")+1)+6)
	}
	footH := 0.0
	if ft := ps.ResolvedFooter(i); ft != nil {
		footH = textBlockHeight(ft.Style, strings.Count(ft.Text, "\n")+1)
	}
	if cfg, _ := ps.ResolvedPageNumber(i); cfg != nil {
		footH = max(footH, textBlockHeight(cfg.TextStyle, 1))
	}
	if footH > 0 {
		off := ps.footerOffset(footH, pg.Margin.Bottom, ps.ResolvedFooter(i))
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
	p := ps.Painter
	px, py, pw, ph := ps.PageRect(i)
	pg := ps.Pages[i]
	left, right := px+pg.Margin.Left, px+pw-pg.Margin.Right

	drawLines := func(text string, orig TextStyle, align Align, top float64) {
		style := footerStyle(orig)
		p.SetFont(style.Font())
		if orig.Color != "" {
			p.SetFillStyle(orig.Color)
		} else {
			p.SetFillStyle(ps.Style.TextColor)
		}
		p.SetTextBaseline("middle")
		lh := style.Leading()
		for k, line := range strings.Split(text, "\n") {
			x := left
			switch align {
			case AlignCenter:
				x = (left+right)/2 - p.MeasureTextWidth(line)/2
			case AlignRight:
				x = right - p.MeasureTextWidth(line)
			}
			p.SetTextAlign("left")
			p.FillText(line, x, top+float64(k)*lh+lh/2)
		}
	}

	if hd := ps.ResolvedHeader(i); hd != nil {
		drawLines(hd.Text, hd.Style, hd.Align, py+ps.headerOffset(hd, pg.Margin.Top))
	}
	ft := ps.ResolvedFooter(i)
	num, _ := ps.ResolvedPageNumber(i)
	footH := 0.0
	if ft != nil {
		footH = textBlockHeight(ft.Style, strings.Count(ft.Text, "\n")+1)
	}
	if num != nil {
		footH = max(footH, textBlockHeight(num.TextStyle, 1))
	}
	if footH == 0 {
		return
	}
	bottom := py + ph - ps.footerOffset(footH, pg.Margin.Bottom, ft)
	if ft != nil {
		h := textBlockHeight(ft.Style, strings.Count(ft.Text, "\n")+1)
		drawLines(ft.Text, ft.Style, ft.Align, bottom-h)
	}
	if num != nil {
		drawLines(ps.PageNumberText(i), num.TextStyle, num.Align, bottom-textBlockHeight(num.TextStyle, 1))
	}
}

var _ Component = (*PageSet)(nil)
