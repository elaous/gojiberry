package components

import "github.com/elaous/gojiberry/xango"

// The keyboard model of a row of tabs (WAI-ARIA "tabs", automatic activation): only the selected tab
// is in the Tab order (the others are reached with the arrow keys), Left and Right move to the
// previous and next tab, wrapping around, Home and End to the first and last, and the tab that gets
// the focus is selected at once. The row is an element with role="tablist" and each tab an element
// with role="tab" inside it; Tabs uses this, and a page with its own row of tab buttons can too.

// TabIndex is the tabindex of a tab: 0 for the selected one, -1 for the others.
func TabIndex(selected bool) xango.Attr {
	if selected {
		return xango.A("tabindex", "0")
	}
	return xango.A("tabindex", "-1")
}

// TabKeys is the keydown handler of the tab at index among count: it moves the focus along the
// tablist and calls onSelect with the index of the tab that has it now. Keys with a modifier are
// left alone.
func TabKeys(index, count int, onSelect func(index int)) xango.Attr {
	return xango.On("keydown", func(e xango.Event) {
		k, ok := e.Value.(xango.KeyData)
		if !ok || k.Ctrl || k.Alt || k.Meta || count < 2 {
			return
		}
		var step xango.FocusStep
		var to int
		switch k.Key {
		case "ArrowRight":
			step, to = xango.FocusNext, (index+1)%count
		case "ArrowLeft":
			step, to = xango.FocusPrev, (index-1+count)%count
		case "Home":
			step, to = xango.FocusFirst, 0
		case "End":
			step, to = xango.FocusLast, count-1
		default:
			return
		}
		e.PreventDefault()
		if e.MoveFocus(`[role="tablist"]`, `[role="tab"]`, step) && onSelect != nil {
			onSelect(to)
		}
	})
}
