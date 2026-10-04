package xango

import (
	"reflect"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// furniture is what one sheet's template result asked for.
type furniture struct {
	header     *HeaderProps
	footer     *FooterProps
	number     *PageNumberProps
	watermark  *WatermarkProps
	background *BackgroundProps
}

// collect walks a template result for the furniture nodes.
func collect(n xango.Node, f *furniture) {
	if n.Kind == xango.KindHost {
		switch p := n.Props.(type) {
		case HeaderProps:
			f.header = &p
			return
		case FooterProps:
			f.footer = &p
			for _, c := range n.Children {
				collect(c, f)
			}
			return
		case PageNumberProps:
			f.number = &p
			return
		case WatermarkProps:
			f.watermark = &p
			return
		case BackgroundProps:
			f.background = &p
			return
		}
	}
	for _, c := range n.Children {
		collect(c, f)
	}
}

func textOf(p string, children []xango.Node) string {
	if p != "" {
		return p
	}
	var sb strings.Builder
	var walk func(xango.Node)
	walk = func(n xango.Node) {
		if n.Kind == xango.KindText {
			sb.WriteString(n.Text)
		}
		if n.Kind != xango.KindHost {
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	for _, c := range children {
		walk(c)
	}
	return strings.TrimSpace(sb.String())
}

func hfOf(text string, style *canvas.TextStyle, al canvas.Align, off float64) *canvas.HeaderFooter {
	hf := &canvas.HeaderFooter{Text: text, Align: al, Offset: off}
	if style != nil {
		hf.Style = *style
	}
	return hf
}

func numOf(p PageNumberProps) *canvas.PageNumberConfig {
	c := &canvas.PageNumberConfig{Show: true, Format: p.Format, Style: p.Style, Align: p.Align}
	if p.TextStyle != nil {
		c.TextStyle = *p.TextStyle
	}
	return c
}

// sameNumber compares two configs ignoring Start.
func sameNumber(a, b *canvas.PageNumberConfig) bool {
	x, y := *a, *b
	x.Start, y.Start = 0, 0
	return reflect.DeepEqual(x, y)
}

// insets reads CSS-style margins.
func insets(m []float64) canvas.Insets {
	switch len(m) {
	case 1:
		return canvas.Insets{Top: m[0], Right: m[0], Bottom: m[0], Left: m[0]}
	case 2:
		return canvas.Insets{Top: m[0], Bottom: m[0], Left: m[1], Right: m[1]}
	case 4:
		return canvas.Insets{Top: m[0], Right: m[1], Bottom: m[2], Left: m[3]}
	}
	return canvas.Insets{Top: 50, Right: 50, Bottom: 50, Left: 50}
}

// buildPages evaluates the Pages template for every sheet and turns the
// answers into canvas.Pages with inheritance: a sheet whose header,
// footer or number equals the previous sheet's leaves it nil (so the
// canvas inherits it), a changed one is set, a dropped one becomes an
// empty header/footer or a hidden number.
func buildPages(p TextFlowProps) []canvas.Page {
	count := max(p.PageCount, 1)
	w, h := p.PageWidth, p.PageHeight
	if w == 0 {
		w = 595
	}
	if h == 0 {
		h = 842
	}
	margin := insets(p.Margins)

	pages := make([]canvas.Page, count)
	var curHeader, curFooter *canvas.HeaderFooter // effective, nil = none
	var curNum *canvas.PageNumberConfig
	var curWatermark *canvas.Watermark
	var curBackground *canvas.Background
	next := 1 // number the next sheet would print while numbering is on
	for i := range pages {
		pg := canvas.Page{Width: w, Height: h, Margin: margin}
		var f furniture
		if p.Pages != nil {
			collect(p.Pages(Page(PageProps{Index: i, Count: count}), i), &f)
		}

		var wantH *canvas.HeaderFooter
		if f.header != nil && !f.header.None {
			wantH = hfOf(textOf(f.header.Text, f.header.Children), f.header.Style, f.header.Align, f.header.Offset)
			if wantH.Text == "" {
				wantH = nil
			}
		}
		pg.Header, curHeader = diffHF(curHeader, wantH)

		var wantF *canvas.HeaderFooter
		if f.footer != nil && !f.footer.None {
			wantF = hfOf(textOf(f.footer.Text, nil), f.footer.Style, f.footer.Align, f.footer.Offset)
			if wantF.Text == "" {
				wantF = nil
			}
		}
		pg.Footer, curFooter = diffHF(curFooter, wantF)

		var wantN *canvas.PageNumberConfig
		var start int
		if f.number != nil && !f.number.None {
			wantN, start = numOf(*f.number), f.number.Start
		}
		switch {
		case wantN == nil && curNum != nil:
			pg.PageNumber, curNum = &canvas.PageNumberConfig{Show: false}, nil
		case wantN != nil && (curNum == nil || !sameNumber(curNum, wantN) || start > 0):
			if start == 0 {
				// Continue the count; when numbering begins here, the
				// number is the sheet's position.
				start = i + 1
				if curNum != nil {
					start = next
				}
			}
			wantN.Start = start
			next = start
			pg.PageNumber, curNum = wantN, wantN
		}
		if curNum != nil {
			next++
		}

		var wantW *canvas.Watermark
		if f.watermark != nil && !f.watermark.None {
			w := f.watermark
			wantW = &canvas.Watermark{Text: w.Text, Color: w.Color, Image: w.Image, Scale: w.Scale, Opacity: w.Opacity, Angle: w.Angle, Straight: w.Straight}
			if w.Style != nil {
				wantW.Style = *w.Style
			}
			if wantW.None() {
				wantW = nil
			}
		}
		pg.Watermark, curWatermark = diffPtr(curWatermark, wantW, func() *canvas.Watermark { return &canvas.Watermark{} })

		var wantB *canvas.Background
		if f.background != nil && !f.background.None && f.background.Image != nil {
			wantB = &canvas.Background{Image: f.background.Image, Opacity: f.background.Opacity, Fit: f.background.Fit}
		}
		pg.Background, curBackground = diffPtr(curBackground, wantB, func() *canvas.Background { return &canvas.Background{} })
		pages[i] = pg
	}
	return pages
}

// diffHF returns what to store on the Page (nil = inherit) and the new
// effective value.
func diffHF(cur, want *canvas.HeaderFooter) (store, eff *canvas.HeaderFooter) {
	return diffPtr(cur, want, func() *canvas.HeaderFooter { return &canvas.HeaderFooter{} })
}

// diffPtr is diffHF for any per-page setting: none makes the "switched
// off" value.
func diffPtr[T any](cur, want *T, none func() *T) (store, eff *T) {
	switch {
	case want == nil && cur == nil:
		return nil, nil
	case want == nil:
		return none(), nil // dropped
	case cur != nil && reflect.DeepEqual(*cur, *want):
		return nil, cur
	}
	return want, want
}
