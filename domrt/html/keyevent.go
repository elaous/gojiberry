//go:build js && wasm

package html

import "syscall/js"

// KeyEvent wraps a keydown/keyup DOM event with the fields a text
// editor or shortcut handler actually needs -- xango.Event carries none
// of these (see its doc comment: Value is only ever a simple
// string/bool payload), so, like DragEvent and SubmitEvent, this is a
// typed escape hatch for the parts a plain xango.EventHandler can't
// express.
type KeyEvent struct {
	Value    js.Value
	Key      string // e.g. "a", "Enter", "ArrowLeft", "Backspace"
	Code     string // physical key, e.g. "KeyA", "Digit1", independent of layout/Shift
	CtrlKey  bool
	ShiftKey bool
	AltKey   bool
	MetaKey  bool
}

func newKeyEvent(v js.Value) KeyEvent {
	return KeyEvent{
		Value:    v,
		Key:      v.Get("key").String(),
		Code:     v.Get("code").String(),
		CtrlKey:  v.Get("ctrlKey").Bool(),
		ShiftKey: v.Get("shiftKey").Bool(),
		AltKey:   v.Get("altKey").Bool(),
		MetaKey:  v.Get("metaKey").Bool(),
	}
}

// PreventDefault suppresses the browser's default handling of the
// event (e.g. stopping ArrowUp/ArrowDown from scrolling the page).
func (e KeyEvent) PreventDefault() { e.Value.Call("preventDefault") }

// StopPropagation stops the event from bubbling further.
func (e KeyEvent) StopPropagation() { e.Value.Call("stopPropagation") }

// OnKeyDown registers fn for the keydown event -- the one that fires
// for non-printable keys (arrows, Backspace, Enter, Ctrl/Cmd
// shortcuts), unlike "input" which only fires once a character
// actually changes the value.
func (e *Element) OnKeyDown(fn func(KeyEvent)) js.Func {
	return e.onKeyEvent("keydown", fn)
}

// OnKeyUp registers fn for the keyup event.
func (e *Element) OnKeyUp(fn func(KeyEvent)) js.Func {
	return e.onKeyEvent("keyup", fn)
}

func (e *Element) onKeyEvent(event string, fn func(KeyEvent)) js.Func {
	cb := js.FuncOf(func(this js.Value, args []js.Value) any {
		fn(newKeyEvent(args[0]))
		return nil
	})
	e.AddEventListener(event, cb)
	return cb
}
