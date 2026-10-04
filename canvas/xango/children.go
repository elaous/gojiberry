package xango

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// Children lets an element made outside this package wrap other canvas
// elements (collab.Collab wraps a canvas and makes it shared): it
// reconciles a list of child nodes the way Canvas and ScrollView do -- the
// same components survive a re-render, new ones are created, gone ones are
// closed.
type Children struct {
	list childList
	ds   []canvas.Drawer
}

// Update matches nodes to the elements made for them last time and updates
// them. It returns whether anything changed and whether an element was
// created. Drawers has the result.
func (c *Children) Update(ctx *Context, nodes []xango.Node) (changed, created bool, err error) {
	ds, changed, created, err := c.list.update(ctx, nodes)
	if err != nil {
		return false, false, err
	}
	c.ds = ds
	return changed, created, nil
}

// Drawers is what the children put on the stage, in order.
func (c *Children) Drawers() []canvas.Drawer { return c.ds }

// Elements implements Holder.
func (c *Children) Elements() []Element { return c.list.elems() }

// Find returns the n-th (from 0, in document order) live component for tag
// among the children, containers included: see Root.Find.
func (c *Children) Find(tag string, n int) any { return findIn(c.list.elems(), tag, n) }

// Keyed is implemented by elements that have a Key prop (Image,
// FloatingText, Table), so that what is shared about them can follow the key
// rather than their place among the others.
type Keyed interface{ Key() string }

// Each calls f for every live component for tag among the children, in
// document order, containers included, with its Key ("" when it has none) and
// its place n among those of its kind that have no key.
func (c *Children) Each(tag string, f func(key string, n int, v any)) {
	unkeyed := 0
	var walk func(es []Element)
	walk = func(es []Element) {
		for _, e := range es {
			if fd, ok := e.(Finder); ok {
				if v := fd.Find(tag); v != nil {
					key := ""
					if k, ok := e.(Keyed); ok {
						key = k.Key()
					}
					n := -1
					if key == "" {
						n = unkeyed
						unkeyed++
					}
					f(key, n, v)
				}
			}
			if h, ok := e.(Holder); ok {
				walk(h.Elements())
			}
		}
	}
	walk(c.list.elems())
}

// Close closes every child element that needs it.
func (c *Children) Close() {
	for _, e := range c.list.elems() {
		if cl, ok := e.(Closer); ok {
			cl.Close()
		}
	}
	c.list = childList{}
	c.ds = nil
}
