package gox

import (
	"fmt"
	"sort"
	"sync"

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
// column dragged, a drag of an image) are not undone by a re-render that
// passes the same props; change a prop to push a new value down.
// Interaction state (caret, selection, drag, popups) is never a prop.
type Root struct {
	Stage *canvas.Stage
	// Redraw, if set, is called at the end of every Render that changed
	// something (wire it to the redraw function Stage.Bind returns).
	Redraw func()

	root *canvasElem
}

// NewRoot returns a Root drawing into stage.
func NewRoot(stage *canvas.Stage) *Root { return &Root{Stage: stage} }

// Find returns the live component created for the n-th (from 0, in
// document order, containers included) element with tag, for reading its
// state or wiring callbacks: a *canvas.TextFlow for TagTextFlow, a
// *canvas.PageSet for TagPage, a *canvas.Image, *canvas.FloatingText,
// *canvas.TableView or *canvas.ScrollView for theirs.
func (r *Root) Find(tag string, n int) any {
	if r.root == nil {
		return nil
	}
	var found any
	var walk func(es []Element) bool
	walk = func(es []Element) bool {
		for _, e := range es {
			if c, ok := e.(Finder); ok {
				if v := c.Find(tag); v != nil {
					if n == 0 {
						found = v
						return true
					}
					n--
				}
			}
			if h, ok := e.(Holder); ok && walk(h.Elements()) {
				return true
			}
		}
		return false
	}
	walk(r.root.kids.elems())
	return found
}

// Render reconciles the stage with n, which must be a Canvas node.
func (r *Root) Render(n gox.Node) error {
	props, ok := n.Props.(CanvasProps)
	if n.Kind != gox.KindHost || !ok {
		return fmt.Errorf("canvas/gox: root node must be a Canvas element, got %q", n.Tag)
	}
	if r.root == nil {
		r.root = &canvasElem{}
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

// --- the element registry ---------------------------------------------

// An Element is the live canvas component(s) behind one element of the
// tree. Register adds new element types; the built-in ones (Canvas
// children: TextFlow, Image, FloatingText, Table, ScrollView) are
// registered the same way.
type Element interface {
	// Drawers returns what goes on the stage (or in the container).
	Drawers() []canvas.Drawer
	// Update applies the node's props -- only those that changed since
	// the last call -- and reports whether anything did.
	Update(c *Context, props any, children []gox.Node) (changed bool, err error)
}

// Finder is implemented by elements Root.Find can look inside: it
// returns the component for tag, or nil.
type Finder interface{ Find(tag string) any }

// Closer is implemented by elements that must undo something when they
// leave the tree (stop obstructing a flow's text, say).
type Closer interface{ Close() }

// Holder is implemented by container elements, so Root.Find can look at
// their children.
type Holder interface{ Elements() []Element }

// Context is what an element sees while it updates.
type Context struct {
	Stage *canvas.Stage
	// Mode is the Canvas's mode, for elements that create children.
	Mode canvas.Mode
	// Flow and Pages are the nearest TextFlow before this element in the
	// same container, and its sheets (nil if there is none): floating
	// objects wrap around it and anchor to its pages.
	Flow  *canvas.TextFlow
	Pages *canvas.PageSet
}

var (
	regMu     sync.RWMutex
	factories = map[string]func() Element{}
)

// Register makes tag a known element: factory is called the first time a
// node with that tag appears at some place in the tree. It panics on a
// duplicate tag.
func Register(tag string, factory func() Element) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := factories[tag]; dup {
		panic(fmt.Sprintf("canvas/gox: element %q registered twice", tag))
	}
	factories[tag] = factory
}

// Elements lists the registered tags.
func Elements() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	var tags []string
	for t := range factories {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags
}

func newElement(tag string) (Element, error) {
	regMu.RLock()
	f := factories[tag]
	regMu.RUnlock()
	if f == nil {
		return nil, fmt.Errorf("canvas/gox: unsupported element %q (registered: %v)", tag, Elements())
	}
	return f(), nil
}

func init() {
	Register(TagTextFlow, func() Element { return &flowElem{ps: canvas.NewPageSet(), flow: canvas.NewTextFlow("", 0, 0, 0, 0)} })
	Register(TagImage, func() Element { return &imageElem{} })
	Register(TagFloatingText, func() Element { return &floatElem{} })
	Register(TagTable, func() Element { return &tableElem{} })
	Register(TagScrollView, func() Element { return &scrollElem{sv: canvas.NewScrollView(0, 0, 0, 0)} })
}

// --- children -------------------------------------------------------------

type slot struct {
	tag, key string
	elem     Element
}

// childList reconciles a list of child nodes against the elements made
// for them last time.
type childList struct{ slots []slot }

func (l *childList) elems() []Element {
	var out []Element
	for _, s := range l.slots {
		out = append(out, s.elem)
	}
	return out
}

// flattenHosts expands fragments (what a {expr} child or <>...</> makes)
// into the host nodes inside them.
func flattenHosts(nodes []gox.Node) []gox.Node {
	var out []gox.Node
	for _, n := range nodes {
		if n.Kind == gox.KindFragment {
			out = append(out, flattenHosts(n.Children)...)
			continue
		}
		out = append(out, n)
	}
	return out
}

// update matches nodes to existing elements -- the first unused one with
// the same tag and key, so inserting or removing an element in the middle
// leaves the others (and their state) alone -- creates what is new, and
// updates all. It returns the drawers (in order), whether anything
// changed, and whether an element was created.
func (l *childList) update(parent *Context, nodes []gox.Node) (ds []canvas.Drawer, changed, created bool, err error) {
	nodes = flattenHosts(nodes)
	var next []slot
	used := make([]bool, len(l.slots))
	ctx := &Context{Stage: parent.Stage, Mode: parent.Mode}
	for _, n := range nodes {
		if n.Kind != gox.KindHost {
			return nil, false, false, fmt.Errorf("canvas/gox: only canvas elements can be children of Canvas or ScrollView, got a %v node", n.Kind)
		}
		var s slot
		found := false
		for i, old := range l.slots {
			if !used[i] && old.tag == n.Tag && old.key == n.Key {
				s, used[i], found = old, true, true
				if i != len(next) {
					changed = true // the order changed
				}
				break
			}
		}
		if !found {
			e, err := newElement(n.Tag)
			if err != nil {
				return nil, false, false, err
			}
			s, created, changed = slot{n.Tag, n.Key, e}, true, true
		}
		ch, err := s.elem.Update(ctx, n.Props, n.Children)
		if err != nil {
			return nil, false, false, err
		}
		changed = changed || ch
		if fe, ok := s.elem.(*flowElem); ok {
			ctx.Flow, ctx.Pages = fe.flow, fe.ps
		}
		next = append(next, s)
		ds = append(ds, s.elem.Drawers()...)
	}
	if len(next) != len(l.slots) {
		changed = true
	}
	for i, old := range l.slots {
		if c, ok := old.elem.(Closer); ok && !used[i] {
			c.Close()
		}
	}
	l.slots = next
	return ds, changed, created, nil
}

// --- Canvas ---------------------------------------------------------------

type canvasElem struct {
	props CanvasProps
	has   bool
	kids  childList
}

func (c *canvasElem) update(r *Root, p CanvasProps, children []gox.Node) (bool, error) {
	changed := false
	st := r.Stage
	if !c.has || p.Width != c.props.Width || p.Height != c.props.Height {
		st.Width, st.Height = p.Width, p.Height
		changed = true
	}
	modeChanged := !c.has || p.Mode != c.props.Mode || p.Role != c.props.Role
	st.Role = p.Role
	mode := p.Role.Mode(p.Mode)
	if p.Features != nil && !sameFeatures(p.Features, c.props.Features) {
		st.Features = canvas.NewRegistry(p.Features...)
	}
	c.props, c.has = p, true

	ds, ch, created, err := c.kids.update(&Context{Stage: st, Mode: mode}, children)
	if err != nil {
		return false, err
	}
	changed = changed || ch
	if changed {
		st.Components = nil
		st.Add(ds...)
	}
	if modeChanged || created {
		st.SetMode(mode)
		changed = true
	}
	return changed, nil
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

func sameAnchor(a, b *canvas.Anchor) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func cloneAnchor(a *canvas.Anchor) *canvas.Anchor {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}
