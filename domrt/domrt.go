//go:build js && wasm

// Package domrt is the client-side runtime: it mounts a xango.Node tree
// into a real DOM element and, on each Update, diffs the new tree
// against what's currently mounted, patching only what changed (text
// content, attributes, event listeners, children) instead of tearing
// down and rebuilding the DOM. Only builds under GOOS=js GOARCH=wasm;
// xango itself has no browser dependency so the same components render
// both here and via xango.Render on the server.
package domrt

import (
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/xango"
)

func document() js.Value { return js.Global().Get("document") }

// Root is a live mount point: the result of Mount, and the handle
// Update is called on for every subsequent render.
type Root struct {
	container js.Value
	tree      *renderedNode
}

// Mount installs the registered stylesheets (see InstallStyles), creates
// DOM nodes for initial and appends them to container, returning a Root whose Update re-renders in place on future calls.
func Mount(container js.Value, initial xango.Node) *Root {
	InstallStyles()
	rn := create(initial)
	for _, n := range rn.ownedNodes() {
		container.Call("appendChild", n)
	}
	return &Root{container: container, tree: rn}
}

// Update diffs next against the currently mounted tree and patches the
// real DOM to match, preserving node identity (and therefore focus,
// scroll position, input state, etc.) wherever the shape is unchanged.
func (r *Root) Update(next xango.Node) {
	r.tree = patch(r.container, r.tree, next)
}

// FirstNode returns the first real DOM node this Root's mounted tree
// owns -- the element itself for a simple single-element component
// (e.g. Button). Useful for a caller that mounts a small, static
// component once, reparents the resulting node into its own DOM
// structure, and never calls Update again -- driving all further
// state changes (a pressed/disabled toggle, a label swap) directly on
// the returned node the same way it already would for any other
// element it built by hand.
func (r *Root) FirstNode() js.Value {
	if r.tree == nil {
		return js.Undefined()
	}
	return r.tree.firstNode()
}

// Listener returns the js.Func trampoline domrt created for the given
// event name on this Root's own top-level node, or the zero js.Func
// if none is bound. For a caller that -- like FirstNode's own use
// case -- never calls Update, Update's own patch/replace machinery
// never runs to release this js.Func on the caller's behalf; Listener
// lets that caller track and release it through whatever mechanism it
// already uses for js.Funcs it creates directly.
func (r *Root) Listener(event string) js.Func {
	if r.tree == nil {
		return js.Func{}
	}
	if b, ok := r.tree.listeners[event]; ok {
		return b.jsFunc
	}
	return js.Func{}
}

// renderedNode pairs a previously-rendered xango.Node with the live DOM
// node(s) it produced, so the next patch can diff against it.
type renderedNode struct {
	kind      xango.Kind
	tag       string
	k         string   // the xango.Node.Key this was created from, for keyed diffing
	dom       js.Value // the single owned node, for Element/Text
	rawNodes  []js.Value
	children  []*renderedNode
	attrs     []xango.Attr
	text      string
	listeners map[string]*eventBinding
}

type eventBinding struct {
	jsFunc  js.Func
	handler xango.EventHandler
}

// ownedNodes returns every top-level DOM node this renderedNode is
// responsible for -- exactly one for Element/Text, the parsed set for
// Raw, and the flattened union of children's owned nodes for Fragment
// (a fragment has no wrapper node of its own).
func (rn *renderedNode) ownedNodes() []js.Value {
	switch rn.kind {
	case xango.KindFragment:
		var out []js.Value
		for _, c := range rn.children {
			out = append(out, c.ownedNodes()...)
		}
		return out
	case xango.KindRaw:
		return rn.rawNodes
	default:
		return []js.Value{rn.dom}
	}
}

func (rn *renderedNode) firstNode() js.Value {
	nodes := rn.ownedNodes()
	if len(nodes) == 0 {
		return js.Undefined()
	}
	return nodes[0]
}

func create(n xango.Node) *renderedNode {
	rn := createInner(n)
	rn.k = n.Key
	return rn
}

func createInner(n xango.Node) *renderedNode {
	switch n.Kind {
	case xango.KindText:
		d := document().Call("createTextNode", n.Text)
		return &renderedNode{kind: xango.KindText, dom: d, text: n.Text}
	case xango.KindRaw:
		return &renderedNode{kind: xango.KindRaw, rawNodes: parseHTMLFragment(n.Text), text: n.Text}
	case xango.KindHost:
		// Host nodes belong to another renderer; leave a hole.
		return &renderedNode{kind: xango.KindFragment}
	case xango.KindFragment:
		rn := &renderedNode{kind: xango.KindFragment}
		for _, c := range n.Children {
			rn.children = append(rn.children, create(c))
		}
		return rn
	case xango.KindElement:
		el := document().Call("createElement", n.Tag)
		rn := &renderedNode{kind: xango.KindElement, tag: n.Tag, dom: el, attrs: n.Attrs, listeners: map[string]*eventBinding{}}
		for _, a := range n.Attrs {
			applyAttr(rn, a)
		}
		for _, c := range n.Children {
			crn := create(c)
			rn.children = append(rn.children, crn)
			for _, dn := range crn.ownedNodes() {
				el.Call("appendChild", dn)
			}
		}
		return rn
	default:
		panic("domrt: unknown node kind")
	}
}

// parseHTMLFragment parses an HTML string into real DOM nodes via a
// <template> element, the standard trick for turning an HTML string
// into a live NodeList without an extra wrapper element.
func parseHTMLFragment(html string) []js.Value {
	tmpl := document().Call("createElement", "template")
	tmpl.Set("innerHTML", html)
	content := tmpl.Get("content")
	childNodes := content.Get("childNodes")
	n := childNodes.Get("length").Int()
	nodes := make([]js.Value, 0, n)
	for i := 0; i < n; i++ {
		nodes = append(nodes, childNodes.Call("item", i))
	}
	return nodes
}

// propAttrs are set as live DOM properties rather than HTML attributes,
// since for form controls the attribute only sets the initial value --
// after user interaction the live property is what actually reflects
// state, and what a controlled component needs to keep in sync.
var propAttrs = map[string]bool{"value": true, "checked": true, "disabled": true, "selected": true}

func truthyAttr(s string) bool {
	return s != "" && s != "false" && s != "0"
}

func applyAttr(rn *renderedNode, a xango.Attr) {
	if a.Handler != nil {
		setListener(rn, a.Name, a.Handler)
		return
	}
	if propAttrs[a.Name] {
		if a.Name == "checked" || a.Name == "disabled" || a.Name == "selected" {
			rn.dom.Set(a.Name, truthyAttr(a.Value))
		} else {
			rn.dom.Set(a.Name, a.Value)
		}
		return
	}
	rn.dom.Call("setAttribute", a.Name, a.Value)
	if a.Name == "autofocus" {
		// The attribute only works on page load: focus the element once it
		// is in the document.
		el := rn.dom
		var focus js.Func
		focus = js.FuncOf(func(js.Value, []js.Value) any {
			el.Call("focus")
			focus.Release()
			return nil
		})
		js.Global().Call("requestAnimationFrame", focus)
	}
}

func removeAttr(rn *renderedNode, a xango.Attr) {
	if a.Handler != nil {
		clearListener(rn, a.Name)
		return
	}
	if propAttrs[a.Name] {
		if a.Name == "checked" || a.Name == "disabled" || a.Name == "selected" {
			rn.dom.Set(a.Name, false)
		} else {
			rn.dom.Set(a.Name, "")
		}
		return
	}
	rn.dom.Call("removeAttribute", a.Name)
}

// setListener registers a real addEventListener at most once per
// (element, event name); re-renders that keep binding the same event
// just swap the Go closure a stable JS trampoline calls through,
// avoiding duplicate listeners and js.Func churn.
func setListener(rn *renderedNode, name string, handler xango.EventHandler) {
	if b, ok := rn.listeners[name]; ok {
		b.handler = handler
		return
	}
	b := &eventBinding{handler: handler}
	b.jsFunc = js.FuncOf(func(this js.Value, args []js.Value) any {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		b.handler(domEventToXangoEvent(ev))
		return nil
	})
	rn.listeners[name] = b
	rn.dom.Call("addEventListener", name, b.jsFunc)
}

func clearListener(rn *renderedNode, name string) {
	b, ok := rn.listeners[name]
	if !ok {
		return
	}
	rn.dom.Call("removeEventListener", name, b.jsFunc)
	b.jsFunc.Release()
	delete(rn.listeners, name)
}

func domEventToXangoEvent(ev js.Value) xango.Event {
	if ev.IsUndefined() || ev.IsNull() {
		return xango.NewEvent("", nil, false, nil, nil)
	}
	typ := ev.Get("type").String()
	var value any
	var checked bool
	if target := ev.Get("target"); !target.IsUndefined() && !target.IsNull() {
		if v := target.Get("value"); v.Type() == js.TypeString {
			value = v.String()
		}
		if c := target.Get("checked"); c.Type() == js.TypeBoolean {
			checked = c.Bool()
		}
	}
	if strings.HasPrefix(typ, "key") {
		value = xango.KeyData{
			Key: ev.Get("key").String(), Code: ev.Get("code").String(),
			Ctrl: ev.Get("ctrlKey").Bool(), Shift: ev.Get("shiftKey").Bool(),
			Alt: ev.Get("altKey").Bool(), Meta: ev.Get("metaKey").Bool(),
		}
	}
	preventDefault := func() { ev.Call("preventDefault") }
	stopProp := func() { ev.Call("stopPropagation") }
	return xango.NewEvent(typ, value, checked, preventDefault, stopProp).WithFocus(func(root, items string, step xango.FocusStep) bool {
		return moveFocus(ev, root, items, step)
	})
}

// moveFocus implements xango.Event.MoveFocus over the DOM event ev.
func moveFocus(ev js.Value, root, items string, step xango.FocusStep) bool {
	target := ev.Get("target")
	if target.IsUndefined() || target.IsNull() || target.Get("closest").IsUndefined() {
		return false
	}
	rootEl := target.Call("closest", root)
	if rootEl.IsNull() || rootEl.IsUndefined() {
		return false
	}
	// ":scope" alone names the widget element itself (querySelectorAll only finds descendants).
	var all []js.Value
	if strings.TrimSpace(items) == ":scope" {
		all = append(all, rootEl)
	} else {
		list := rootEl.Call("querySelectorAll", items)
		for i := 0; i < list.Length(); i++ {
			all = append(all, list.Index(i))
		}
	}
	active := js.Global().Get("document").Get("activeElement")
	var cands []js.Value
	cur := -1
	for _, el := range all {
		if el.Get("disabled").Truthy() || el.Call("getAttribute", "aria-disabled").String() == "true" {
			continue
		}
		if el.Get("offsetParent").IsNull() && el.Get("getClientRects").Invoke().Length() == 0 {
			continue // not shown
		}
		if el.Equal(active) {
			cur = len(cands)
		}
		cands = append(cands, el)
	}
	if len(cands) == 0 {
		return false
	}
	var next int
	switch step {
	case xango.FocusFirst:
		next = 0
	case xango.FocusLast:
		next = len(cands) - 1
	case xango.FocusPrev:
		next = (cur - 1 + len(cands)) % len(cands)
		if cur < 0 {
			next = len(cands) - 1
		}
	default:
		next = (cur + 1) % len(cands)
	}
	cands[next].Call("focus")
	return true
}

// patch diffs newNode against the tree rooted at old (whose last
// rendered value was oldNode), mutating the live DOM to match and
// returning the renderedNode to track for the next patch. parent is
// used only when old must be replaced wholesale (to know where in the
// DOM to splice the replacement in).
func patch(parent js.Value, old *renderedNode, next xango.Node) *renderedNode {
	if old == nil {
		rn := create(next)
		for _, n := range rn.ownedNodes() {
			parent.Call("appendChild", n)
		}
		return rn
	}
	if old.kind != next.Kind || (next.Kind == xango.KindElement && old.tag != next.Tag) {
		return replace(parent, old, next)
	}
	old.k = next.Key
	switch next.Kind {
	case xango.KindText:
		if old.text != next.Text {
			old.dom.Set("data", next.Text)
			old.text = next.Text
		}
		return old
	case xango.KindRaw:
		if old.text != next.Text {
			return replace(parent, old, next)
		}
		return old
	case xango.KindFragment:
		old.children = patchChildren(parent, anchorAfter(parent, old), old.children, next.Children)
		return old
	case xango.KindElement:
		patchAttrs(old, next.Attrs)
		old.attrs = next.Attrs
		old.children = patchChildren(old.dom, js.Undefined(), old.children, next.Children)
		return old
	}
	return old
}

// replace tears down old's DOM nodes and mounts next fresh in their
// place, preserving position via insertBefore against old's first node.
func replace(parent js.Value, old *renderedNode, next xango.Node) *renderedNode {
	oldNodes := old.ownedNodes()
	rn := create(next)
	if len(oldNodes) > 0 {
		ref := oldNodes[0]
		for _, n := range rn.ownedNodes() {
			parent.Call("insertBefore", n, ref)
		}
		for _, n := range oldNodes {
			parent.Call("removeChild", n)
		}
	} else {
		for _, n := range rn.ownedNodes() {
			parent.Call("appendChild", n)
		}
	}
	return rn
}

// anchorAfter returns the DOM node immediately following rn's owned
// nodes in parent, used as the insertBefore reference when patching a
// fragment's children (a fragment has no wrapper node to anchor off of).
func anchorAfter(parent js.Value, rn *renderedNode) js.Value {
	nodes := rn.ownedNodes()
	if len(nodes) == 0 {
		return js.Undefined()
	}
	return nodes[len(nodes)-1].Get("nextSibling")
}

func patchAttrs(old *renderedNode, next []xango.Attr) {
	nextByName := make(map[string]xango.Attr, len(next))
	for _, a := range next {
		nextByName[a.Name] = a
	}
	for _, a := range old.attrs {
		if _, ok := nextByName[a.Name]; !ok {
			removeAttr(old, a)
		}
	}
	for _, a := range next {
		applyAttr(old, a)
	}
}

// patchChildren reconciles an old list of rendered children against a
// new list of xango.Node children. Children carrying a Key are matched by
// key (so reorders move existing DOM nodes instead of recreating them);
// unkeyed children are matched positionally.
func patchChildren(parent js.Value, endAnchor js.Value, old []*renderedNode, next []xango.Node) []*renderedNode {
	oldByKey := make(map[string]*renderedNode)
	oldKeys := make([]string, len(old))
	for i, o := range old {
		oldKeys[i] = o.key()
		if oldKeys[i] != "" {
			oldByKey[oldKeys[i]] = o
		}
	}
	used := make(map[*renderedNode]bool, len(old))

	result := make([]*renderedNode, len(next))
	for i, n := range next {
		var match *renderedNode
		if n.Key != "" {
			if o, ok := oldByKey[n.Key]; ok && !used[o] {
				match = o
			}
		} else if i < len(old) && !used[old[i]] && old[i].key() == "" {
			match = old[i]
		}
		if match != nil {
			used[match] = true
			result[i] = patch(parent, match, n)
		} else {
			result[i] = create(n)
			for _, dn := range result[i].ownedNodes() {
				parent.Call("appendChild", dn)
			}
		}
	}

	// Remove anything from old that wasn't reused.
	for _, o := range old {
		if !used[o] {
			for _, dn := range o.ownedNodes() {
				parent.Call("removeChild", dn)
			}
		}
	}

	// Ensure DOM order matches result order (moves reused-but-shifted
	// nodes into place); appends before endAnchor when set (fragment
	// case) or at the natural end otherwise.
	for i := len(result) - 1; i >= 0; i-- {
		var ref js.Value
		if i+1 < len(result) {
			ref = result[i+1].firstNode()
		} else {
			ref = endAnchor
		}
		for _, dn := range reverse(result[i].ownedNodes()) {
			// A node already in place is left alone: re-inserting it would
			// drop the focus (and the selection) from anything inside it.
			if inPlace(parent, dn, ref) {
				ref = dn
				continue
			}
			if ref.IsUndefined() {
				parent.Call("appendChild", dn)
			} else {
				parent.Call("insertBefore", dn, ref)
			}
			ref = dn
		}
	}
	return result
}

func reverse(vs []js.Value) []js.Value {
	out := make([]js.Value, len(vs))
	for i, v := range vs {
		out[len(vs)-1-i] = v
	}
	return out
}

// key exists only because renderedNode doesn't otherwise retain the
// xango.Node.Key it was created from; store it alongside text/attrs.
func (rn *renderedNode) key() string { return rn.k }

// inPlace reports whether dn is already a child of parent directly before
// ref (the last child when ref is undefined).
func inPlace(parent, dn, ref js.Value) bool {
	if !dn.Get("parentNode").Equal(parent) {
		return false
	}
	next := dn.Get("nextSibling")
	if ref.IsUndefined() {
		return next.IsNull()
	}
	return next.Equal(ref)
}
