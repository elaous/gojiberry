package xango

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango"
)

type Children struct {
	list childList
	ds   []canvas.Drawer
}

func (c *Children) Update(ctx *Context, nodes []xango.Node) (changed, created bool, err error) {
	ds, changed, created, err := c.list.update(ctx, nodes)
	if err != nil {
		return false, false, err
	}
	c.ds = ds
	return changed, created, nil
}

func (c *Children) Drawers() []canvas.Drawer { return c.ds }

// Elements implements Holder.
func (c *Children) Elements() []Element { return c.list.elems() }

func (c *Children) Find(tag string, n int) any { return findIn(c.list.elems(), tag, n) }

type Keyed interface{ Key() string }

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
