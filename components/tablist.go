package components

import "github.com/elaous/xango"

func TabIndex(selected bool) xango.Attr {
	if selected {
		return xango.A("tabindex", "0")
	}
	return xango.A("tabindex", "-1")
}

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
