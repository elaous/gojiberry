package docfile

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// The Meta keys the print layout of a document is kept under.
const (
	PageHeaderKey   = "page.header"
	PageFooterKey   = "page.footer"
	PageTOCKey      = "page.toc"
	PageTOCTitle    = "page.toctitle"
	PageFirstKey    = "page.first"     // "1": the first page has a header and footer of its own
	PageFirstHead   = "page.firsthead" // the first page's header
	PageFirstFoot   = "page.firstfoot" // the first page's footer
	PageEvenKey     = "page.even"      // "1": the even pages have a header and footer of their own
	PageEvenHead    = "page.evenhead"  // the even pages' header
	PageEvenFoot    = "page.evenfoot"  // the even pages' footer
	PageSizeKey     = "page.size"      // "595x842": the sheet in points
	PageMarginsKey  = "page.margins"   // "72,72,72,72": top, right, bottom, left in points
	PageVerticalKey = "page.vertical"  // "1": vertical text, the lines top to bottom and from right to left
)

// PageLayout is how a document is laid out on paper when it is exported to a paged format (PDF, Word,
// RTF) or printed from the HTML export: the running header and footer, whether a table of contents
// starts the document, and the sheet and its margins.
//
// Header and Footer are text with fields: {page} is the number of the page, {pages} the page count and
// {title} the document's title. A tab separates the parts: one part is centred, two are left and right,
// three are left, centre and right ("{title}\tPage {page} of {pages}"). Empty means none.
//
// A zero Width or Height means the exporter's default sheet (US Letter), and a Margins of all zeros
// means its default margins.
type PageLayout struct {
	Header, Footer string
	// FirstDiffers gives the first page a header and footer of its own (FirstHeader, FirstFooter, same
	// fields); empty means none, as on a title page.
	FirstDiffers             bool
	FirstHeader, FirstFooter string
	// EvenDiffers gives the even pages (2, 4, ...) a header and footer of their own (EvenHeader, EvenFooter); Header
	// and Footer then belong to the odd pages. The first page, when it has its own, is neither.
	EvenDiffers            bool
	EvenHeader, EvenFooter string
	// Vertical sets the text in vertical lines, top to bottom, the lines following one another from right to
	// left (vertical-rl, as in Chinese, Japanese and Korean books).
	Vertical      bool
	TOC           bool
	TOCTitle      string // the table of contents' heading; empty means "Contents"
	Width, Height float64
	Margins       [4]float64
}

// IsZero says nothing is set.
func (l PageLayout) IsZero() bool { return l == PageLayout{} }

// Normalized drops what cannot be laid out: a sheet outside 100 to 5000 points, a margin that is not a
// finite number from 0 up to half the sheet, and control characters other than the tab in a header or
// footer.
func (l PageLayout) Normalized() PageLayout {
	l.Header, l.Footer = cleanDecor(l.Header), cleanDecor(l.Footer)
	l.FirstHeader, l.FirstFooter = cleanDecor(l.FirstHeader), cleanDecor(l.FirstFooter)
	if !l.FirstDiffers {
		l.FirstHeader, l.FirstFooter = "", ""
	}
	l.EvenHeader, l.EvenFooter = cleanDecor(l.EvenHeader), cleanDecor(l.EvenFooter)
	if !l.EvenDiffers {
		l.EvenHeader, l.EvenFooter = "", ""
	}
	l.TOCTitle = strings.TrimSpace(strings.ReplaceAll(cleanDecor(l.TOCTitle), "\t", " "))
	ok := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 100 && v <= 5000 }
	if !ok(l.Width) || !ok(l.Height) {
		l.Width, l.Height = 0, 0
	}
	for i, m := range l.Margins {
		if math.IsNaN(m) || math.IsInf(m, 0) || m < 0 {
			l.Margins = [4]float64{}
			break
		}
		l.Margins[i] = math.Round(m)
	}
	if l.Width > 0 && (l.Margins[1]+l.Margins[3] >= l.Width || l.Margins[0]+l.Margins[2] >= l.Height) {
		l.Margins = [4]float64{}
	}
	return l
}

func cleanDecor(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// PageLayout reads the document's print layout from its Meta; a value that is not valid is ignored.
func (d Document) PageLayout() PageLayout {
	var l PageLayout
	l.Header, _ = d.Get(PageHeaderKey)
	l.Footer, _ = d.Get(PageFooterKey)
	if v, _ := d.Get(PageTOCKey); v == "1" {
		l.TOC = true
	}
	l.TOCTitle, _ = d.Get(PageTOCTitle)
	if v, _ := d.Get(PageVerticalKey); v == "1" {
		l.Vertical = true
	}
	if v, _ := d.Get(PageEvenKey); v == "1" {
		l.EvenDiffers = true
		l.EvenHeader, _ = d.Get(PageEvenHead)
		l.EvenFooter, _ = d.Get(PageEvenFoot)
	}
	if v, _ := d.Get(PageFirstKey); v == "1" {
		l.FirstDiffers = true
		l.FirstHeader, _ = d.Get(PageFirstHead)
		l.FirstFooter, _ = d.Get(PageFirstFoot)
	}
	if v, _ := d.Get(PageSizeKey); v != "" {
		if w, h, ok := strings.Cut(v, "x"); ok {
			l.Width, _ = strconv.ParseFloat(w, 64)
			l.Height, _ = strconv.ParseFloat(h, 64)
		}
	}
	if v, _ := d.Get(PageMarginsKey); v != "" {
		if parts := strings.Split(v, ","); len(parts) == 4 {
			for i, p := range parts {
				l.Margins[i], _ = strconv.ParseFloat(p, 64)
			}
		}
	}
	return l.Normalized()
}

// SetPageLayout stores the print layout in Meta; a part that is empty is removed.
func (d *Document) SetPageLayout(l PageLayout) {
	l = l.Normalized()
	put := func(key, v string) {
		if v == "" {
			d.Meta = slices.DeleteFunc(slices.Clone(d.Meta), func(m MetaEntry) bool { return m.Key == key })
			return
		}
		d.Set(key, v)
	}
	put(PageHeaderKey, l.Header)
	put(PageFooterKey, l.Footer)
	toc := ""
	if l.TOC {
		toc = "1"
	}
	first := ""
	if l.FirstDiffers {
		first = "1"
	}
	even := ""
	if l.EvenDiffers {
		even = "1"
	}
	put(PageEvenKey, even)
	put(PageEvenHead, l.EvenHeader)
	put(PageEvenFoot, l.EvenFooter)
	put(PageFirstKey, first)
	put(PageFirstHead, l.FirstHeader)
	put(PageFirstFoot, l.FirstFooter)
	put(PageTOCKey, toc)
	vertical := ""
	if l.Vertical {
		vertical = "1"
	}
	put(PageVerticalKey, vertical)
	put(PageTOCTitle, l.TOCTitle)
	size := ""
	if l.Width > 0 {
		size = fmt.Sprintf("%gx%g", l.Width, l.Height)
	}
	put(PageSizeKey, size)
	margins := ""
	if l.Margins != ([4]float64{}) {
		margins = fmt.Sprintf("%g,%g,%g,%g", l.Margins[0], l.Margins[1], l.Margins[2], l.Margins[3])
	}
	put(PageMarginsKey, margins)
}
