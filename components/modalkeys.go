package components

import "github.com/elaous/xango"

const modalFocusable = `a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])`

func ModalKeys(onClose func()) xango.Attr {
	return xango.On("keydown", func(e xango.Event) {
		k, ok := e.Value.(xango.KeyData)
		if !ok || k.Ctrl || k.Alt || k.Meta {
			return
		}
		switch k.Key {
		case "Tab":
			step := xango.FocusNext
			if k.Shift {
				step = xango.FocusPrev
			}
			e.PreventDefault()
			e.MoveFocus(".gj-modal", modalFocusable, step)
		case "Escape":
			if onClose != nil {
				e.PreventDefault()
				e.StopPropagation()
				onClose()
			}
		}
	})
}
