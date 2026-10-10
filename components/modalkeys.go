package components

import "github.com/elaous/gojiberry/xango"

// modalFocusable selects what the Tab key can reach inside a modal.
const modalFocusable = `a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])`

// ModalKeys is the keydown handler of a modal box: Tab and Shift+Tab move the focus round the
// focusable elements inside it, wrapping at the ends (and from the box itself, which has the focus when
// it opens, to the first or the last), so the focus cannot leave it for the page behind; Escape calls
// onClose, if there is one (the same as a click on the backdrop). Keys with Ctrl, Alt or Meta are left
// alone.
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
			// If nothing can be focused, the focus stays where it is: still not out.
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
