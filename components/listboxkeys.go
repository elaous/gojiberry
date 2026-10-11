package components

import "github.com/elaous/gojiberry/xango"

// The keyboard model of a list of options opened from a button (Dropdown, FontSelector): the button
// opens it with Down or Up and the focus goes to an option; Up, Down, Home and End move among the
// options, Enter or Space chooses one, Escape closes the list and gives the focus back to the button,
// and Tab closes it and leaves. The options are focusable (tabindex -1) so that Tab does not walk
// through a long list. A key with Ctrl, Alt or Meta is left alone.

// listbox names the parts of one such widget, as selectors from the widget's root element.
type listbox struct {
	root    string // the widget's class selector
	control string // the button, as ":scope > ..."
	options string // the options, as ":scope ..."
	first   string // what the focus goes to when the list opens ("" for the first option)
}

// controlKey is the keydown handler of the button.
func (l listbox) controlKey(open bool, setOpen func(bool)) xango.Attr {
	return xango.On("keydown", func(e xango.Event) {
		k, ok := e.Value.(xango.KeyData)
		if !ok || k.Ctrl || k.Alt || k.Meta || setOpen == nil {
			return
		}
		switch k.Key {
		case "ArrowDown", "ArrowUp":
			e.PreventDefault()
			if !open {
				setOpen(true)
			}
			step := xango.FocusFirst
			if k.Key == "ArrowUp" {
				step = xango.FocusLast
			}
			target := l.options
			if l.first != "" && k.Key == "ArrowDown" {
				target = l.first
			}
			e.MoveFocus(l.root, target, step)
		case "Escape":
			if open {
				e.PreventDefault()
				e.StopPropagation()
				setOpen(false)
			}
		}
	})
}

// optionKey is the keydown handler of an option; choose runs for Enter and Space, and closeAfter says
// the list closes with the choice (the focus then goes to the button first).
func (l listbox) optionKey(choose func(), closeAfter bool, setOpen func(bool)) xango.Attr {
	return xango.On("keydown", func(e xango.Event) {
		k, ok := e.Value.(xango.KeyData)
		if !ok || k.Ctrl || k.Alt || k.Meta {
			return
		}
		move := func(step xango.FocusStep) {
			e.PreventDefault()
			e.MoveFocus(l.root, l.options, step)
		}
		switch k.Key {
		case "ArrowDown":
			move(xango.FocusNext)
		case "ArrowUp":
			move(xango.FocusPrev)
		case "Home":
			move(xango.FocusFirst)
		case "End":
			move(xango.FocusLast)
		case "Enter", " ":
			e.PreventDefault()
			if closeAfter {
				e.MoveFocus(l.root, l.control, xango.FocusFirst)
			}
			if choose != nil {
				choose()
			}
		case "Escape":
			e.PreventDefault()
			e.StopPropagation()
			e.MoveFocus(l.root, l.control, xango.FocusFirst)
			if setOpen != nil {
				setOpen(false)
			}
		case "Tab":
			// the focus leaves, and the list goes with it
			if setOpen != nil {
				setOpen(false)
			}
		}
	})
}

// searchKey is the keydown handler of a search field above the options: Down goes to the first option,
// Escape closes the list with the focus back on the button.
func (l listbox) searchKey(setOpen func(bool)) xango.Attr {
	return xango.On("keydown", func(e xango.Event) {
		k, ok := e.Value.(xango.KeyData)
		if !ok || k.Ctrl || k.Alt || k.Meta {
			return
		}
		switch k.Key {
		case "ArrowDown":
			e.PreventDefault()
			e.MoveFocus(l.root, l.options, xango.FocusFirst)
		case "Escape":
			e.PreventDefault()
			e.StopPropagation()
			e.MoveFocus(l.root, l.control, xango.FocusFirst)
			if setOpen != nil {
				setOpen(false)
			}
		}
	})
}
