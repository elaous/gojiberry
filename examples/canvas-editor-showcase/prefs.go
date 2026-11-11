//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
)

const (
	prefsKey  = "showcase:a11y"
	keymapKey = "showcase:keys" // the person's own key bindings (see keymapkeep.go)
)

func (p prefs) attrs() map[string]string {
	return map[string]string{"data-gj-motion": p.Motion, "data-gj-contrast": p.Contrast, "data-gj-text": p.Text, "data-gj-spacing": p.Spacing, "data-gj-focus": p.Focus, "data-gj-targets": p.Targets, "data-gj-theme": p.Theme}
}

func loadPrefs() prefs {
	p, _ := parsePrefs([]byte(lsGet(prefsKey)))
	return p
}

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

const (
	lowMemorySteps = 20
	manySteps      = 1000
)

func historySteps(p prefs) int {
	switch {
	case p.Memory == "high":
		return manySteps
	case lowMemory(p):
		return lowMemorySteps
	}
	return canvas.DefaultHistoryLimit
}

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

var onPrefs func()

var onPrefsSaved func(prefs)

var curPrefs = loadPrefs()

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
	if onPrefsSaved != nil {
		onPrefsSaved(p)
	}
}
