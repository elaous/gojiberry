package canvas

import "math"

// Watermark is text or a picture drawn faintly across a sheet.
type Watermark struct {
	// Text is the words drawn in Style, such as DRAFT.
	Text  string
	Style TextStyle
	Color string
	// Image is a picture drawn instead of text.
	Image ImageSource
	Scale float64
	// Opacity is 0..1; zero means 0.15.
	Opacity float64
	// Angle turns the watermark, in degrees clockwise; zero means -45.
	Angle    float64
	Straight bool
}

// None reports whether the watermark shows nothing.
func (w *Watermark) None() bool { return w == nil || (w.Text == "" && w.Image == nil) }

// Background is a picture filling a sheet, behind everything.
type Background struct {
	Image ImageSource
	// Opacity is 0..1; zero means fully opaque.
	Opacity float64
	// Fit is how the picture fills the sheet.
	Fit BackgroundFit
}

// BackgroundFit says how a Background picture fills a sheet.
type BackgroundFit int

const (
	FitCover   BackgroundFit = iota // scaled to cover the sheet, cropped to fit
	FitContain                      // scaled to fit inside it
	FitStretch                      // stretched to the sheet's shape
)

// None reports whether the background shows nothing.
func (b *Background) None() bool { return b == nil || b.Image == nil }

// ResolvedWatermark returns page i's effective watermark.
func (ps *PageSet) ResolvedWatermark(i int) *Watermark {
	for j := min(i, len(ps.Pages)-1); j >= 0; j-- {
		if w := ps.Pages[j].Watermark; w != nil {
			if w.None() {
				return nil
			}
			return w
		}
	}
	return nil
}

// ResolvedBackground is ResolvedWatermark for background pictures.
func (ps *PageSet) ResolvedBackground(i int) *Background {
	for j := min(i, len(ps.Pages)-1); j >= 0; j-- {
		if b := ps.Pages[j].Background; b != nil {
			if b.None() {
				return nil
			}
			return b
		}
	}
	return nil
}

// drawBackdrop paints sheet i's background picture and watermark.
func (ps *PageSet) drawBackdrop(i int) {
	ip, ok := ps.Painter.(ImagePainter)
	if !ok {
		return
	}
	x, y, w, h := ps.PageRect(i)
	if b := ps.ResolvedBackground(i); b != nil {
		iw, ih := b.Image.Size()
		if iw > 0 && ih > 0 {
			ip.Save()
			ip.ClipRect(x, y, w, h)
			ip.SetGlobalAlpha(orF(b.Opacity, 1))
			dw, dh := w, h
			switch b.Fit {
			case FitCover:
				s := math.Max(w/iw, h/ih)
				dw, dh = iw*s, ih*s
			case FitContain:
				s := math.Min(w/iw, h/ih)
				dw, dh = iw*s, ih*s
			}
			ip.DrawImage(b.Image, 0, 0, iw, ih, x+(w-dw)/2, y+(h-dh)/2, dw, dh)
			ip.Restore()
		}
	}
	wm := ps.ResolvedWatermark(i)
	if wm == nil {
		return
	}
	ip.Save()
	ip.ClipRect(x, y, w, h)
	ip.Translate(x+w/2, y+h/2)
	ip.SetGlobalAlpha(orF(wm.Opacity, 0.15))
	if wm.Image != nil {
		iw, ih := wm.Image.Size()
		if iw > 0 && ih > 0 {
			ip.Rotate(wm.Angle * math.Pi / 180)
			dw := w * orF(wm.Scale, 0.5)
			dh := dw * ih / iw
			ip.DrawImage(wm.Image, 0, 0, iw, ih, -dw/2, -dh/2, dw, dh)
		}
	} else {
		angle := wm.Angle
		if angle == 0 && !wm.Straight {
			angle = -45
		}
		ip.Rotate(angle * math.Pi / 180)
		st := wm.Style
		if st.FontSize <= 0 {
			st.FontSize = 72
		}
		if st.FontWeight == 0 {
			st.FontWeight = 700
		}
		ip.SetFont(st.Font())
		ip.SetFillStyle(orStr(wm.Color, "#808080"))
		ip.SetTextAlign("center")
		ip.SetTextBaseline("middle")
		ip.FillText(wm.Text, 0, 0)
	}
	ip.Restore()
}

func orF(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}

// RemoveScope is how far removing a header, footer, page number or
// watermark reaches.
type RemoveScope int

const (
	// RemoveThisPage removes it from one page; the pages after keep it.
	RemoveThisPage RemoveScope = iota
	// RemoveForward removes it from this page and every later one.
	RemoveForward
	// RemoveAll removes it from every page.
	RemoveAll
)

// RemoveHeader removes the header of the given page as scope says (see
// the "none" setting of Page.Header).
func (ps *PageSet) RemoveHeader(page int, scope RemoveScope) {
	if page < 0 || page >= len(ps.Pages) {
		return
	}
	eff := ps.ResolvedHeader(page)
	removeInherited(ps, page, scope,
		func(p *Page) **HeaderFooter { return &p.Header },
		func() *HeaderFooter { return &HeaderFooter{} },
		func() *HeaderFooter {
			if eff == nil {
				return nil
			}
			c := *eff
			return &c
		})
	ps.changed()
}

// RemoveFooter is RemoveHeader for footers.
func (ps *PageSet) RemoveFooter(page int, scope RemoveScope) {
	if page < 0 || page >= len(ps.Pages) {
		return
	}
	eff := ps.ResolvedFooter(page)
	removeInherited(ps, page, scope,
		func(p *Page) **HeaderFooter { return &p.Footer },
		func() *HeaderFooter { return &HeaderFooter{} },
		func() *HeaderFooter {
			if eff == nil {
				return nil
			}
			c := *eff
			return &c
		})
	ps.changed()
}

// RemovePageNumber removes the page number of the given page as scope
// says. Removing one page's number keeps the count: the next page shows
// its own number, not the removed one's.
func (ps *PageSet) RemovePageNumber(page int, scope RemoveScope) {
	if page < 0 || page >= len(ps.Pages) {
		return
	}
	cfg, n := ps.ResolvedPageNumber(page)
	removeInherited(ps, page, scope,
		func(p *Page) **PageNumberConfig { return &p.PageNumber },
		func() *PageNumberConfig { return &PageNumberConfig{Show: false} },
		func() *PageNumberConfig {
			if cfg == nil {
				return nil
			}
			c := *cfg
			c.Start = n + 1
			return &c
		})
	ps.changed()
}

// RemoveWatermark removes the watermark of the given page as scope says.
func (ps *PageSet) RemoveWatermark(page int, scope RemoveScope) {
	if page < 0 || page >= len(ps.Pages) {
		return
	}
	eff := ps.ResolvedWatermark(page)
	removeInherited(ps, page, scope,
		func(p *Page) **Watermark { return &p.Watermark },
		func() *Watermark { return &Watermark{} },
		func() *Watermark {
			if eff == nil {
				return nil
			}
			c := *eff
			return &c
		})
	ps.changed()
}

// RemoveBackground removes the background picture of the given page as
// scope says.
func (ps *PageSet) RemoveBackground(page int, scope RemoveScope) {
	if page < 0 || page >= len(ps.Pages) {
		return
	}
	eff := ps.ResolvedBackground(page)
	removeInherited(ps, page, scope,
		func(p *Page) **Background { return &p.Background },
		func() *Background { return &Background{} },
		func() *Background {
			if eff == nil {
				return nil
			}
			c := *eff
			return &c
		})
	ps.changed()
}

// removeInherited does the inheritance bookkeeping of a removal for any
// of the per-page settings: field picks the setting out of a Page, none
// makes the "switched off" value, and keep makes a copy of what the page
// showed before (nil if nothing), to carry on after a single-page removal.
func removeInherited[T any](ps *PageSet, page int, scope RemoveScope, field func(*Page) **T, none func() *T, keep func() *T) {
	switch scope {
	case RemoveAll:
		for i := range ps.Pages {
			*field(&ps.Pages[i]) = nil
		}
		*field(&ps.Pages[0]) = none()
	case RemoveForward:
		*field(&ps.Pages[page]) = none()
	default:
		next := page + 1
		var carry *T
		if next < len(ps.Pages) && *field(&ps.Pages[next]) == nil {
			carry = keep()
		}
		*field(&ps.Pages[page]) = none()
		if carry != nil {
			*field(&ps.Pages[next]) = carry
		}
	}
}
