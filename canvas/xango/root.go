package xango

import (
	"fmt"
	"sort"
	"sync"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango"
)

// Root renders xango trees onto a stage.
type Root struct {
	Stage  *canvas.Stage
	Redraw func()

	root *canvasElem
}

// NewRoot returns a Root drawing into stage.
func NewRoot(stage *canvas.Stage) *Root { return &Root{Stage: stage} }

func (r *Root) Find(tag string, n int) any {
	if r.root == nil {
		return nil
	}
	return findIn(r.root.kids.elems(), tag, n)
}

func findIn(es []Element, tag string, n int) any {
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
	walk(es)
	return found
}

func (r *Root) redraw() {
	if r.Redraw != nil {
		r.Redraw()
	}
}

func (r *Root) Render(n xango.Node) error {
	props, ok := n.Props.(CanvasProps)
	if n.Kind != xango.KindHost || !ok {
		return fmt.Errorf("canvas/xango: root node must be a Canvas element, got %q", n.Tag)
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

type Element interface {
	Drawers() []canvas.Drawer
	Update(c *Context, props any, children []xango.Node) (changed bool, err error)
}

type Finder interface{ Find(tag string) any }

type Closer interface{ Close() }

type Holder interface{ Elements() []Element }

// Context is what an element sees while it updates.
type Context struct {
	Stage *canvas.Stage
	// Mode is the Canvas's mode, for elements that create children.
	Mode   canvas.Mode
	Flow   *canvas.TextFlow
	Pages  *canvas.PageSet
	Redraw func()
}

var (
	regMu     sync.RWMutex
	factories = map[string]func() Element{}
)

// panics on a duplicate tag
func Register(tag string, factory func() Element) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := factories[tag]; dup {
		panic(fmt.Sprintf("canvas/xango: element %q registered twice", tag))
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
		return nil, fmt.Errorf("canvas/xango: unsupported element %q (registered: %v)", tag, Elements())
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

type slot struct {
	tag, key string
	elem     Element
}

type childList struct{ slots []slot }

func (l *childList) elems() []Element {
	var out []Element
	for _, s := range l.slots {
		out = append(out, s.elem)
	}
	return out
}

func flattenHosts(nodes []xango.Node) []xango.Node {
	var out []xango.Node
	for _, n := range nodes {
		if n.Kind == xango.KindFragment {
			out = append(out, flattenHosts(n.Children)...)
			continue
		}
		out = append(out, n)
	}
	return out
}

func (l *childList) update(parent *Context, nodes []xango.Node) (ds []canvas.Drawer, changed, created bool, err error) {
	nodes = flattenHosts(nodes)
	var next []slot
	used := make([]bool, len(l.slots))
	ctx := &Context{Stage: parent.Stage, Mode: parent.Mode, Redraw: parent.Redraw}
	for _, n := range nodes {
		if n.Kind != xango.KindHost {
			return nil, false, false, fmt.Errorf("canvas/xango: only canvas elements can be children of Canvas or ScrollView, got a %v node", n.Kind)
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

type canvasElem struct {
	props CanvasProps
	has   bool
	kids  childList
}

func (c *canvasElem) update(r *Root, p CanvasProps, children []xango.Node) (bool, error) {
	changed := false
	st := r.Stage
	if !c.has || p.Width != c.props.Width || p.Height != c.props.Height {
		st.Width, st.Height = p.Width, p.Height
		changed = true
	}
	modeChanged := !c.has || p.Mode != c.props.Mode || p.Role != c.props.Role
	if !c.has || p.Role != c.props.Role {
		st.Role = p.Role
	}
	mode := p.Role.Mode(p.Mode)
	if p.Features != nil && !sameFeatures(p.Features, c.props.Features) {
		st.Features = canvas.NewRegistry(p.Features...)
	}
	c.props, c.has = p, true

	ds, ch, created, err := c.kids.update(&Context{Stage: st, Mode: mode, Redraw: r.redraw}, children)
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
