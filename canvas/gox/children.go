package gox

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
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
func (c *Children) Update(ctx *Context, nodes []gox.Node) (changed, created bool, err error) {
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
