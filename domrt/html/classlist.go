//go:build js && wasm

package html

// ClassListAdd adds one or more classes to e.
func (e *Element) ClassListAdd(class ...string) {
	e.Element.Get("classList").Call("add", toAnySlice(class)...)
}

// ClassListRemove removes one or more classes from e.
func (e *Element) ClassListRemove(class ...string) {
	e.Element.Get("classList").Call("remove", toAnySlice(class)...)
}

// ClassListToggle adds or removes class; reports presence.
func (e *Element) ClassListToggle(class string) bool {
	return e.Element.Get("classList").Call("toggle", class).Bool()
}

// ClassListContains reports whether e has the given class.
func (e *Element) ClassListContains(class string) bool {
	return e.Element.Get("classList").Call("contains", class).Bool()
}

func toAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}
