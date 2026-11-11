package xango

import (
	"fmt"
	"reflect"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

type flowElem struct {
	ps    *canvas.PageSet
	flow  *canvas.TextFlow
	last  TextFlowProps
	pages []canvas.Page // as last computed from the template
	has   bool
}

func (f *flowElem) Drawers() []canvas.Drawer { return []canvas.Drawer{f.ps, f.flow} }

func (f *flowElem) Find(tag string) any {
	switch tag {
	case TagTextFlow:
		return f.flow
	case TagPage:
		return f.ps
	}
	return nil
}

func (f *flowElem) Update(_ *Context, props any, _ []xango.Node) (bool, error) {
	p, ok := props.(TextFlowProps)
	if !ok {
		return false, fmt.Errorf("canvas/xango: TextFlow got props of type %T", props)
	}
	first := !f.has
	changed := first
	old := f.last
	f.last, f.has = p, true

	if f.ps.X != p.X || f.ps.Y != p.Y || f.ps.Width != p.Width {
		f.ps.X, f.ps.Y, f.ps.Width = p.X, p.Y, p.Width
		changed = true
	}
	if f.ps.Spread != p.Spread || f.ps.SpreadBook != p.SpreadBook || f.ps.Gutter != p.Gutter {
		f.ps.Spread, f.ps.SpreadBook, f.ps.Gutter = p.Spread, p.SpreadBook, p.Gutter
		changed = true
	}

	f.ps.OnTurn = p.OnTurn

	pages := buildPages(p)
	if !reflect.DeepEqual(pages, f.pages) {
		f.pages = pages
		f.ps.Pages = clonePages(pages)
		changed = true
	}

	fl := f.flow
	fl.Pages = f.ps
	fl.OnChange = p.OnChange
	fl.OnFootnote = p.OnFootnote
	fl.OnLink, fl.OnHoverLink = p.OnLink, p.OnHoverLink
	if !sameValue(p.Hyphenator, old.Hyphenator) {
		fl.Hyphenator = p.Hyphenator
		changed = true
	}
	if p.Style != nil && (old.Style == nil || *old.Style != *p.Style) {
		fl.Style = *p.Style
		changed = true
	}
	fl.Transform = p.Transform
	if !reflect.DeepEqual(p.Styles, old.Styles) || first {
		fl.Styles = append([]canvas.StyleDef(nil), p.Styles...)
		changed = true
	}
	fl.AutoClose, fl.Complete, fl.Completer = p.AutoClose, p.Complete, p.Completer
	fl.TextScale, fl.PeerPalette, fl.StrongCaret = p.TextScale, p.PeerPalette, p.StrongCaret
	if themeChanged(old.Theme, p.Theme) || first {
		var th canvas.FlowTheme
		if p.Theme != nil {
			th = *p.Theme
		}
		sheet := canvas.DefaultPageStyle().Sheet
		if th.Background != "" {
			sheet = th.Background
		}
		f.ps.Style.Sheet = sheet
		if th.Text != "" || (old.Theme != nil && old.Theme.Text != "") { // not over a colour the page's own Style chose
			fl.Style.Color = th.Text
		}
		fl.LinkColor = th.Link
		fl.InvalidateTransform()
		changed = true
	}
	fl.Comments, fl.Author, fl.OnHoverComment = p.Comments, p.Author, p.OnHoverComment
	if p.Footnotes != fl.Footnotes || p.FootnoteBand != fl.FootnoteBand {
		fl.Footnotes, fl.FootnoteBand = p.Footnotes, p.FootnoteBand
		changed = true
	}
	if first || !reflect.DeepEqual(p.Vocabulary, old.Vocabulary) {
		fl.Vocabulary = append([]string(nil), p.Vocabulary...)
		changed = true
	}
	if first || p.Source != old.Source || p.Text != old.Text || !reflect.DeepEqual(p.Items, old.Items) {
		fl.ResetContent()
		fl.Source, fl.Text, fl.Items = p.Source, p.Text, append([]canvas.Item(nil), p.Items...)
		changed = true
	}
	if !reflect.DeepEqual(p.Columns, old.Columns) || p.Gap != old.Gap {
		fl.Columns, fl.Gap = append([]float64(nil), p.Columns...), p.Gap
		changed = true
	}
	return changed, nil
}

func clonePages(in []canvas.Page) []canvas.Page {
	out := make([]canvas.Page, len(in))
	for i, p := range in {
		if p.Header != nil {
			h := *p.Header
			p.Header = &h
		}
		if p.Footer != nil {
			h := *p.Footer
			p.Footer = &h
		}
		if p.PageNumber != nil {
			n := *p.PageNumber
			p.PageNumber = &n
		}
		out[i] = p
	}
	return out
}

type imageElem struct {
	im   *canvas.Image
	last ImageProps
}

// Key implements Keyed.
func (e *imageElem) Key() string { return e.last.Key }

func (e *imageElem) Drawers() []canvas.Drawer { return []canvas.Drawer{e.im} }

func (e *imageElem) Find(tag string) any {
	if tag == TagImage && e.im != nil {
		return e.im
	}
	return nil
}

func (e *imageElem) Update(c *Context, props any, _ []xango.Node) (bool, error) {
	p, ok := props.(ImageProps)
	if !ok {
		return false, fmt.Errorf("canvas/xango: Image got props of type %T", props)
	}
	if p.Source == nil {
		return false, fmt.Errorf("canvas/xango: Image needs a Source")
	}
	changed := false
	first := e.im == nil
	old := e.last
	if first {
		e.im = canvas.NewImage(p.Source, p.X, p.Y)
		changed = true
	}
	e.last = p
	im := e.im
	im.OnChange = p.OnChange
	if first || p.Source != old.Source {
		im.Src = p.Source
		im.W, im.H = p.Source.Size()
		changed = true
	}
	if first || p.Width != old.Width || p.Height != old.Height {
		if p.Width > 0 {
			im.W = p.Width
		}
		if p.Height > 0 {
			im.H = p.Height
		}
		changed = changed || p.Width > 0 || p.Height > 0
	}
	if !first && (p.X != old.X || p.Y != old.Y) {
		im.X, im.Y = p.X, p.Y
		changed = true
	}
	if first || !sameAnchor(p.Anchor, old.Anchor) {
		im.Anchor = cloneAnchor(p.Anchor)
		changed = true
	}
	if first || p.Wrap != old.Wrap {
		im.SetWrap(p.Wrap)
		changed = true
	}
	if first || p.Inline != old.Inline {
		im.Placement = canvas.PlaceFloating
		if p.Inline {
			im.Placement = canvas.PlaceInline
		}
		changed = true
	}
	if first || p.Opacity != old.Opacity {
		im.Alpha = 1
		if p.Opacity > 0 {
			im.Alpha = p.Opacity
		}
		changed = true
	}
	if first || p.Clip != old.Clip {
		im.Clip = p.Clip
		changed = true
	}
	if first || p.Alt != old.Alt || p.Decorative != old.Decorative {
		im.Alt, im.Decorative = p.Alt, p.Decorative
		changed = true
	}
	if im.Flow() != c.Flow {
		im.SetFlow(c.Flow)
		changed = true
	}
	return changed, nil
}

type floatElem struct {
	ft   *canvas.FloatingText
	last FloatingTextProps
}

// Key implements Keyed.
func (e *floatElem) Key() string { return e.last.Key }

func (e *floatElem) Drawers() []canvas.Drawer { return []canvas.Drawer{e.ft} }

func (e *floatElem) Find(tag string) any {
	if tag == TagFloatingText && e.ft != nil {
		return e.ft
	}
	return nil
}

func (e *floatElem) Update(c *Context, props any, _ []xango.Node) (bool, error) {
	p, ok := props.(FloatingTextProps)
	if !ok {
		return false, fmt.Errorf("canvas/xango: FloatingText got props of type %T", props)
	}
	changed := false
	first := e.ft == nil
	old := e.last
	if first {
		w, h := p.Width, p.Height
		if w == 0 {
			w = 190
		}
		if h == 0 {
			h = 96
		}
		e.ft = canvas.NewFloatingText(p.Text, p.X, p.Y, w, h)
		changed = true
	}
	e.last = p
	ft := e.ft
	ft.OnChange = p.OnChange
	if !first && p.Text != old.Text {
		ft.SetText(p.Text)
		changed = true
	}
	if !first && (p.X != old.X || p.Y != old.Y) {
		ft.X, ft.Y = p.X, p.Y
		changed = true
	}
	if !first && ((p.Width != old.Width && p.Width > 0) || (p.Height != old.Height && p.Height > 0)) {
		if p.Width > 0 {
			ft.Width = p.Width
		}
		if p.Height > 0 {
			ft.Height = p.Height
		}
		changed = true
	}
	if first || !sameAnchor(p.Anchor, old.Anchor) {
		ft.Anchor = cloneAnchor(p.Anchor)
		changed = true
	}
	if first || p.Wrap != old.Wrap {
		ft.Wrap = p.Wrap
		changed = true
	}
	if first || p.Background != old.Background {
		ft.Background = p.Background
		changed = true
	}
	if first || p.Opacity != old.Opacity {
		ft.Opacity = p.Opacity
		changed = true
	}
	if first || !reflect.DeepEqual(p.Vocabulary, old.Vocabulary) {
		ft.Vocabulary = append([]string(nil), p.Vocabulary...)
	}
	if ft.Flow() != c.Flow {
		ft.SetFlow(c.Flow)
		changed = true
	}
	return changed, nil
}

type tableElem struct {
	v    *canvas.TableView
	last TableProps
}

// Key implements Keyed.
func (e *tableElem) Key() string { return e.last.Key }

func (e *tableElem) Drawers() []canvas.Drawer { return []canvas.Drawer{e.v} }

func (e *tableElem) Find(tag string) any {
	if tag == TagTable && e.v != nil {
		return e.v
	}
	return nil
}

func (e *tableElem) Update(c *Context, props any, _ []xango.Node) (bool, error) {
	p, ok := props.(TableProps)
	if !ok {
		return false, fmt.Errorf("canvas/xango: Table got props of type %T", props)
	}
	if len(p.Cells) == 0 || len(p.Cells[0]) == 0 {
		return false, fmt.Errorf("canvas/xango: Table needs at least one cell")
	}
	changed := false
	first := e.v == nil
	old := e.last
	if first {
		e.v = canvas.NewTableView(newTable(p), nil)
		e.v.X, e.v.Y = p.X, p.Y
		changed = true
	}
	e.last = p
	v := e.v
	v.OnChange = p.OnChange
	v.RenderCell = p.RenderCell
	if !first && (!reflect.DeepEqual(p.Cells, old.Cells) || !reflect.DeepEqual(p.Merges, old.Merges) || !reflect.DeepEqual(p.Styles, old.Styles)) {
		v.Table = newTable(p)
		changed = true
	}
	if !first && (p.X != old.X || p.Y != old.Y) {
		v.X, v.Y = p.X, p.Y
		changed = true
	}
	if first || !sameAnchor(p.Anchor, old.Anchor) {
		v.Anchor = cloneAnchor(p.Anchor)
		changed = true
	}
	if first || p.Wrap != old.Wrap {
		v.Wrap = p.Wrap
		changed = true
	}
	if v.Flow() != c.Flow {
		v.SetFlow(c.Flow)
		changed = true
	}
	if first {
		if c.Stage.Painter != nil {
			v.SetPainter(c.Stage.Painter)
		}
	}
	return changed, nil
}

type scrollElem struct {
	sv   *canvas.ScrollView
	kids childList
	has  bool
}

func (e *scrollElem) Drawers() []canvas.Drawer { return []canvas.Drawer{e.sv} }
func (e *scrollElem) Elements() []Element      { return e.kids.elems() }

func (e *scrollElem) Find(tag string) any {
	if tag == TagScrollView {
		return e.sv
	}
	return nil
}

func (e *scrollElem) Update(c *Context, props any, children []xango.Node) (bool, error) {
	p, ok := props.(ScrollViewProps)
	if !ok {
		return false, fmt.Errorf("canvas/xango: ScrollView got props of type %T", props)
	}
	changed := !e.has
	e.has = true
	sv := e.sv
	if sv.X != p.X || sv.Y != p.Y || sv.Width != p.Width || sv.Height != p.Height {
		sv.X, sv.Y, sv.Width, sv.Height = p.X, p.Y, p.Width, p.Height
		changed = true
	}
	if sv.LockH != p.LockH || sv.LockV != p.LockV {
		sv.LockH, sv.LockV = p.LockH, p.LockV
		changed = true
	}
	sv.Typewriter, sv.TypewriterAt, sv.TypewriterX = p.Typewriter, p.TypewriterAt, p.TypewriterX
	ds, ch, created, err := e.kids.update(c, children)
	if err != nil {
		return false, err
	}
	if ch || changed {
		w, h := p.ContentWidth, p.ContentHeight
		for _, d := range ds {
			if s, ok := d.(interface{ Size() (float64, float64) }); ok && (w == 0 || h == 0) {
				dw, dh := s.Size()
				if p.ContentWidth == 0 {
					w = max(w, dw)
				}
				if p.ContentHeight == 0 {
					h = max(h, dh)
				}
			}
		}
		sv.SetContent(ds, w, h)
		if created {
			sv.SetContentMode(c.Mode)
		}
		changed = true
	}
	return changed, nil
}

// Close implements Closer: stop wrapping text around the object.
func (e *imageElem) Close() { e.im.SetFlow(nil) }

// Close implements Closer.
func (e *floatElem) Close() { e.ft.SetFlow(nil) }

// Close implements Closer.
func (e *tableElem) Close() { e.v.SetFlow(nil) }

// uncomparable values report false: a change is never missed
func sameValue(a, b any) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

func newTable(p TableProps) *canvas.Table {
	t := canvas.NewTableFrom(p.Cells)
	for _, m := range p.Merges {
		if m.Row >= 0 && m.Col >= 0 && m.Rows >= 1 && m.Cols >= 1 && m.Row+m.Rows <= t.Rows() && m.Col+m.Cols <= t.Cols() {
			t.MergeRange(m.Row, m.Col, m.Row+m.Rows-1, m.Col+m.Cols-1)
		}
	}
	for pos, st := range p.Styles {
		t.SetCellStyle(pos.Row, pos.Col, st)
	}
	return t
}

func themeChanged(a, b *canvas.FlowTheme) bool {
	switch {
	case a == nil && b == nil:
		return false
	case a == nil || b == nil:
		return true
	}
	return *a != *b
}
