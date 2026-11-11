package xango

import "fmt"

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
