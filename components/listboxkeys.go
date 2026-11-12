package components

import "github.com/elaous/xango"

type listbox struct {
	root    string // the widget's class selector
	control string // the button, as ":scope > ..."
	options string // the options, as ":scope ..."
	first   string
}

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
			if setOpen != nil {
				setOpen(false)
			}
		}
	})
}

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
