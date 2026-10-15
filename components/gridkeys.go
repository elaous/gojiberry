package components

import "github.com/elaous/gojiberry/xango"

// The keyboard model of a set of choices laid out in a grid or a row or a column (the symbol picker, its
// category chips, the style gallery): one of them is in the tab order, and the arrow keys move among them
// by what is seen: left and right to the neighbour in the row, up and down to the one above or below, Home and
// End to the first and the last. Tab leaves the set after one stop, so a hundred symbols do not take a hundred
// presses. A key with Ctrl, Alt or Meta is left alone. (The colour and date pickers are the browser's own
// inputs, with the browser's own keys.)

// gridKeys is the keydown handler for a widget whose choices are the elements items selects (from the element
// root selects): put it on the widget's container.
func gridKeys(root, items string) xango.Attr {
	return xango.On("keydown", func(e xango.Event) {
		k, ok := e.Value.(xango.KeyData)
		if !ok || k.Ctrl || k.Alt || k.Meta {
			return
		}
		step := xango.FocusNext
		switch k.Key {
		case "ArrowRight":
		case "ArrowLeft":
			step = xango.FocusPrev
		case "ArrowDown":
			step = xango.FocusDown
		case "ArrowUp":
			step = xango.FocusUp
		case "Home":
			step = xango.FocusFirst
		case "End":
			step = xango.FocusLast
		default:
			return
		}
		e.PreventDefault()
		e.MoveFocus(root, items, step)
	})
}

// rovingTab is the tabindex of the i-th choice of a set: only the one that is the way in (the current one, or
// the first) can be reached with Tab.
func rovingTab(i, entry int) xango.Attr {
	if i == entry {
		return xango.A("tabindex", "0")
	}
	return xango.A("tabindex", "-1")
}
