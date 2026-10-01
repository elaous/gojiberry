package goxcanvas

import (
	"fmt"
	"reflect"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

// Root renders gox trees onto a stage. Render may be called as often as
// the state changes: the first call creates the canvas components, later
// calls update them in place, matching by position, tag and Key.
//
// Props describe the document -- text, pages, columns -- and are applied
// only when they differ from what the last render applied, so the
// document edits the canvas makes itself (a header edited on a page, a
// column dragged) are not undone by a re-render that passes the same
// props; change a prop to push a new value down. Interaction state
// (caret, selection, drag, popups) is never a prop.
type Root struct {
	Stage *canvas.Stage
	// Redraw, if set, is called at the end of every Render that changed
	// something (wire it to the redraw function Stage.Bind returns).
	Redraw func()

	root *canvasInst
}

// NewRoot returns a Root drawing into stage.
func NewRoot(stage *canvas.Stage) *Root { return &Root{Stage: stage} }

// Find returns the live component created for the n-th (from 0) element
// with tag, for reading its state or wiring callbacks: a *canvas.TextFlow
// for TagTextFlow, a *canvas.PageSet for TagPage.
func (r *Root) Find(tag string, n int) any {
	if r.root == nil {
		return nil
	}
	for _, k := range r.root.kids {
		if ki, ok := k.(*flowInst); ok && (tag == TagTextFlow || tag == TagPage) {
			if n == 0 {
				if tag == TagPage {
					return ki.ps
				}
				return ki.flow
			}
			n--
		}
	}
	return nil
}

// Render reconciles the stage with n, which must be a Canvas node.
func (r *Root) Render(n gox.Node) error {
	props, ok := n.Props.(CanvasProps)
	if n.Kind != gox.KindHost || !ok {
		return fmt.Errorf("goxcanvas: root node must be a Canvas element, got %q", n.Tag)
	}
	if r.root == nil {
		r.root = &canvasInst{}
	}
	changed, err := r.root.update(r, props, n.Children)
	if err != nil {
		return err
	}
	if changed && r.Redraw != nil {
		r.Redraw()
	}
	return nil
}

type inst interface {
	tag() string
	key() string
	drawers() []canvas.Drawer
	// update applies props and reports whether anything changed.
	update(r *Root, props any) bool
}

type canvasInst struct {
	props CanvasProps
	has   bool
	kids  []inst
}

func (c *canvasInst) update(r *Root, p CanvasProps, children []gox.Node) (bool, error) {
	changed := false
	st := r.Stage
	if !c.has || p.Width != c.props.Width || p.Height != c.props.Height {
		st.Width, st.Height = p.Width, p.Height
		changed = true
	}
	modeChanged := !c.has || p.Mode != c.props.Mode
	if p.Features != nil && !sameFeatures(p.Features, c.props.Features) {
		st.Features = canvas.NewRegistry(p.Features...)
	}
	c.props, c.has = p, true

	var kids []inst
	created := false
	for i, ch := range children {
		if ch.Kind == gox.KindFragment {
			return false, fmt.Errorf("goxcanvas: fragments are not supported inside Canvas yet")
		}
		var old inst
		if i < len(c.kids) && c.kids[i].tag() == ch.Tag && c.kids[i].key() == ch.Key {
			old = c.kids[i]
		}
		if old == nil {
			ni, err := newInst(ch)
			if err != nil {
				return false, err
			}
			old, created, changed = ni, true, true
		}
		if old.update(r, ch.Props) {
			changed = true
		}
		kids = append(kids, old)
	}
	if len(kids) != len(c.kids) {
		changed = true
	}
	c.kids = kids

	if changed {
		st.Components = nil
		for _, k := range kids {
			st.Add(k.drawers()...)
		}
	}
	if modeChanged || created {
		st.SetMode(p.Mode)
		changed = true
	}
	return changed, nil
}

func newInst(n gox.Node) (inst, error) {
	switch n.Tag {
	case TagTextFlow:
		return &flowInst{k: n.Key, ps: canvas.NewPageSet(), flow: canvas.NewTextFlow("", 0, 0, 0, 0)}, nil
	}
	return nil, fmt.Errorf("goxcanvas: unsupported element %q inside Canvas (supported: %s)", n.Tag, TagTextFlow)
}

// flowInst is a TextFlow element: a PageSet drawn under a TextFlow.
type flowInst struct {
	k     string
	ps    *canvas.PageSet
	flow  *canvas.TextFlow
	last  TextFlowProps
	pages []canvas.Page // as last computed from the template
	has   bool
}

func (f *flowInst) tag() string              { return TagTextFlow }
func (f *flowInst) key() string              { return f.k }
func (f *flowInst) drawers() []canvas.Drawer { return []canvas.Drawer{f.ps, f.flow} }

func (f *flowInst) update(_ *Root, props any) bool {
	p := props.(TextFlowProps)
	first := !f.has
	changed := first
	old := f.last
	f.last, f.has = p, true

	if f.ps.X != p.X || f.ps.Y != p.Y || f.ps.Width != p.Width {
		f.ps.X, f.ps.Y, f.ps.Width = p.X, p.Y, p.Width
		changed = true
	}

	pages := buildPages(p)
	if !reflect.DeepEqual(pages, f.pages) {
		f.pages = pages
		f.ps.Pages = clonePages(pages)
		changed = true
	}

	fl := f.flow
	fl.Pages = f.ps
	fl.OnChange = p.OnChange
	if p.Style != nil && (old.Style == nil || *old.Style != *p.Style) {
		fl.Style = *p.Style
		changed = true
	}
	if p.Source != old.Source || p.Text != old.Text || first {
		fl.Source, fl.Text = p.Source, p.Text
		fl.InvalidateTransform()
		changed = true
	}
	fl.Transform = p.Transform
	if !reflect.DeepEqual(p.Items, old.Items) {
		fl.Items = p.Items
		changed = true
	}
	if !reflect.DeepEqual(p.Columns, old.Columns) || p.Gap != old.Gap {
		fl.Columns, fl.Gap = append([]float64(nil), p.Columns...), p.Gap
		changed = true
	}
	return changed
}

// clonePages copies pages so editing a header on the canvas does not
// write through to the comparison copy.
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

func sameFeatures(a, b []canvas.Feature) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name() != b[i].Name() {
			return false
		}
	}
	return true
}
