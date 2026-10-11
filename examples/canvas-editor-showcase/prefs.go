//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/components"
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
	Focus    string `json:"focus,omitempty"`    // "on": distraction-free
	Targets  string `json:"targets,omitempty"`  // "large"
	Collab   string `json:"collab,omitempty"`   // what is announced of a shared session: "" everything, "important", "off"
}

// attrs are the values the root element carries.
func (p prefs) attrs() map[string]string {
	return map[string]string{"data-gj-motion": p.Motion, "data-gj-contrast": p.Contrast, "data-gj-text": p.Text, "data-gj-spacing": p.Spacing, "data-gj-focus": p.Focus, "data-gj-targets": p.Targets}
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
	if p.Focus != "on" {
		p.Focus = ""
	}
	if p.Targets != "large" {
		p.Targets = ""
	}
	if p.Collab != components.CollabImportant && p.Collab != components.CollabOff {
		p.Collab = ""
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

// curPrefs are the preferences in force.
var curPrefs = loadPrefs()

// setPrefs applies the preferences and keeps them in this browser.
func setPrefs(p prefs) {
	curPrefs = p
	applyPrefs(p)
	b, _ := json.Marshal(p)
	lsSet(prefsKey, string(b))
}

// prefsMenu is the display preferences: text size, spacing, contrast and motion, each ticked when
// set by the person (when not, the system's own setting applies), and a reset.
func prefsMenu(u *ui, renderUI func()) []components.MenuItem {
	change := func(msg string, f func(*prefs)) func() {
		return func() {
			p := curPrefs
			f(&p)
			setPrefs(p)
			u.announce(msg)
			renderUI()
		}
	}
	size := func(label, value string) components.MenuItem {
		return components.MenuItem{Label: label, Checked: curPrefs.Text == value,
			OnSelect: change(T("a11y.textSize", "Text size: %s", label), func(p *prefs) { p.Text = value })}
	}
	collab := func(label, level string) components.MenuItem {
		return components.MenuItem{Label: label, Checked: curPrefs.Collab == level,
			OnSelect: change(label, func(p *prefs) { p.Collab = level })}
	}
	toggle := func(label string, on bool, set func(*prefs, bool)) components.MenuItem {
		return components.MenuItem{Label: label, Checked: on, OnSelect: func() {
			state := T("a11y.off", "off")
			if !on {
				state = T("a11y.on", "on")
			}
			change(label+": "+state, func(p *prefs) { set(p, !on) })()
		}}
	}
	return []components.MenuItem{
		size(T("prefs.textDefault", "Text size: default"), ""),
		size(T("prefs.textLarge", "Text size: 125%"), "large"),
		size(T("prefs.textLarger", "Text size: 150%"), "larger"),
		size(T("prefs.textLargest", "Text size: 200%"), "largest"),
		{Separator: true},
		toggle(T("prefs.spacing", "Wide text spacing"), curPrefs.Spacing == "wide", func(p *prefs, on bool) { p.Spacing = map[bool]string{true: "wide"}[on] }),
		toggle(T("prefs.contrast", "More contrast"), curPrefs.Contrast == "more", func(p *prefs, on bool) { p.Contrast = map[bool]string{true: "more"}[on] }),
		toggle(T("prefs.motion", "Reduce motion"), curPrefs.Motion == "reduce", func(p *prefs, on bool) { p.Motion = map[bool]string{true: "reduce"}[on] }),
		{Separator: true},
		collab(T("prefs.collabAll", "Shared editing: announce everything"), components.CollabAll),
		collab(T("prefs.collabImportant", "Shared editing: announce only the connection"), components.CollabImportant),
		collab(T("prefs.collabOff", "Shared editing: announce nothing"), components.CollabOff),
		{Separator: true},
		toggle(T("prefs.targets", "Large targets"), curPrefs.Targets == "large", func(p *prefs, on bool) { p.Targets = map[bool]string{true: "large"}[on] }),
		toggle(T("prefs.focus", "Distraction-free mode"), curPrefs.Focus == "on", func(p *prefs, on bool) { p.Focus = map[bool]string{true: "on"}[on] }),
		{Separator: true},
		{Label: T("prefs.reset", "Reset to defaults"), OnSelect: change(T("a11y.prefsReset", "Display preferences reset"), func(p *prefs) { *p = prefs{} })},
	}
}
