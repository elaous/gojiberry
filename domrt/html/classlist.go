//go:build js && wasm

package html

// ClassListAdd adds one or more classes to e. Prefer this (and
// ClassListRemove/ClassListToggle/ClassListContains) over
// GetClassName/SetClassName on SVG elements (see domrt/svg):
// className there is an SVGAnimatedString object, not a plain string,
// so e.Get("className").String() and e.Set("className", ...) don't
// work as expected -- classList, unlike className, behaves the same
// for both HTML and SVG elements.
func (e *Element) ClassListAdd(class ...string) {
	e.Element.Get("classList").Call("add", toAnySlice(class)...)
}

// ClassListRemove removes one or more classes from e.
func (e *Element) ClassListRemove(class ...string) {
	e.Element.Get("classList").Call("remove", toAnySlice(class)...)
}

// ClassListToggle adds class if e doesn't have it, or removes it if e
// does, returning whether it's present afterward.
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
