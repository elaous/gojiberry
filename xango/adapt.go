package xango

import "fmt"

// Child adapts the value of a {expr} child hole into a Node, mirroring
// how JSX treats expression children: a Node is used as-is, a []Node
// becomes a Fragment, nil renders nothing, and everything else is
// stringified as text.
func Child(v any) Node {
	switch x := v.(type) {
	case Node:
		return x
	case []Node:
		return Frag(x...)
	case nil:
		return Frag()
	case string:
		return Text(x)
	case fmt.Stringer:
		return Text(x.String())
	default:
		return Text(fmt.Sprint(x))
	}
}

// AttrVal adapts the value of an attribute {expr} hole into a string,
// for plain (non-event) attributes such as class={someExpr}.
func AttrVal(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}
