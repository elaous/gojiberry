//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
)

// Display preferences. They are kept in this browser (localStorage) and set as data-gj-* attributes
// on the root element, which components/a11y.css turns into styles. A person who sets none gets the
// operating system's own settings (reduced motion, more contrast) through the media queries there.

const (
	prefsKey  = "showcase:a11y"
	keymapKey = "showcase:keys" // the person's own key bindings (see keymapkeep.go)
)

// attrs are the values the root element carries.
func (p prefs) attrs() map[string]string {
	return map[string]string{"data-gj-motion": p.Motion, "data-gj-contrast": p.Contrast, "data-gj-text": p.Text, "data-gj-spacing": p.Spacing, "data-gj-focus": p.Focus, "data-gj-targets": p.Targets, "data-gj-theme": p.Theme}
}

// loadPrefs reads the kept preferences; unknown or unreadable ones are as if unset.
func loadPrefs() prefs {
	p, _ := parsePrefs([]byte(lsGet(prefsKey)))
	return p
}

// lowMemory says whether undo history is kept short: the person asked for it, or has not chosen and the
// browser reports a device with 2 GB of memory or less (navigator.deviceMemory, where it exists).
func lowMemory(p prefs) bool {
	switch p.Memory {
	case "low":
		return true
	case "normal", "high":
		return false
	}
	dm := js.Global().Get("navigator").Get("deviceMemory")
	return dm.Type() == js.TypeNumber && dm.Float() > 0 && dm.Float() <= 2
}

// lowMemorySteps is how many undo steps are kept in low-memory mode, and manySteps for a person who asked
// for a long history.
const (
	lowMemorySteps = 20
	manySteps      = 1000
)

// historySteps is how many undo steps the preferences ask for.
func historySteps(p prefs) int {
	switch {
	case p.Memory == "high":
		return manySteps
	case lowMemory(p):
		return lowMemorySteps
	}
	return canvas.DefaultHistoryLimit
}

// applyPrefs sets the preferences on the root element (an empty one removes its attribute) and the
// length of the undo history.
func applyPrefs(p prefs) {
	canvas.SetHistoryLimit(historySteps(p))
	setAccess(p)
	root := js.Global().Get("document").Get("documentElement")
	for k, v := range p.attrs() {
		if v == "" {
			root.Call("removeAttribute", k)
		} else {
			root.Call("setAttribute", k, v)
		}
	}
}

// pickPrefsFile opens the browser's file chooser and hands the chosen file's bytes to done.
func pickPrefsFile(done func([]byte)) {
	doc := js.Global().Get("document")
	in := doc.Call("createElement", "input")
	in.Set("type", "file")
	in.Set("accept", "application/json,.json")
	in.Get("style").Set("display", "none")
	var onChange js.Func
	onChange = js.FuncOf(func(js.Value, []js.Value) any {
		files := in.Get("files")
		if files.Length() == 0 {
			return nil
		}
		var onText js.Func
		onText = js.FuncOf(func(_ js.Value, args []js.Value) any {
			done([]byte(args[0].String()))
			onText.Release()
			return nil
		})
		files.Index(0).Call("text").Call("then", onText)
		in.Call("remove")
		onChange.Release()
		return nil
	})
	in.Call("addEventListener", "change", onChange)
	doc.Get("body").Call("appendChild", in)
	in.Call("click")
}

// onPrefs, when set, is called after the preferences change, for what has to be drawn again (the canvas).
var onPrefs func()

// curPrefs are the preferences in force.
var curPrefs = loadPrefs()

// setPrefs applies the preferences and keeps them in this browser.
func setPrefs(p prefs) {
	curPrefs = p
	applyPrefs(p)
	if p.Lang != "" {
		useLocale(p.Lang)
	}
	if onPrefs != nil {
		onPrefs()
	}
	b, _ := json.Marshal(p)
	lsSet(prefsKey, string(b))
}
