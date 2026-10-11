//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"
)

// Display preferences. They are kept in this browser (localStorage) and set as data-gj-* attributes
// on the root element, which components/a11y.css turns into styles. A person who sets none gets the
// operating system's own settings (reduced motion, more contrast) through the media queries there.

const prefsKey = "showcase:a11y"

// prefs are the display settings; an empty field means "as the system has it".
type prefs struct {
	Motion   string `json:"motion,omitempty"`   // "reduce"
	Contrast string `json:"contrast,omitempty"` // "more"
	Text     string `json:"text,omitempty"`     // "large", "larger", "largest"
	Spacing  string `json:"spacing,omitempty"`  // "wide"
}

// attrs are the values the root element carries.
func (p prefs) attrs() map[string]string {
	return map[string]string{"data-gj-motion": p.Motion, "data-gj-contrast": p.Contrast, "data-gj-text": p.Text, "data-gj-spacing": p.Spacing}
}

// loadPrefs reads the kept preferences; unknown or unreadable ones are as if unset.
func loadPrefs() prefs {
	var p prefs
	_ = json.Unmarshal([]byte(lsGet(prefsKey)), &p)
	if p.Motion != "reduce" {
		p.Motion = ""
	}
	if p.Contrast != "more" {
		p.Contrast = ""
	}
	if p.Text != "large" && p.Text != "larger" && p.Text != "largest" {
		p.Text = ""
	}
	if p.Spacing != "wide" {
		p.Spacing = ""
	}
	return p
}

// applyPrefs sets the preferences on the root element (an empty one removes its attribute).
func applyPrefs(p prefs) {
	root := js.Global().Get("document").Get("documentElement")
	for k, v := range p.attrs() {
		if v == "" {
			root.Call("removeAttribute", k)
		} else {
			root.Call("setAttribute", k, v)
		}
	}
}

// setPrefs applies the preferences and keeps them in this browser.
func setPrefs(p prefs) {
	applyPrefs(p)
	b, _ := json.Marshal(p)
	lsSet(prefsKey, string(b))
}
