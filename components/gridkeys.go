package components

import "github.com/elaous/xango"

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

func rovingTab(i, entry int) xango.Attr {
	if i == entry {
		return xango.A("tabindex", "0")
	}
	return xango.A("tabindex", "-1")
}
