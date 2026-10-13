//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
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
	Suggest  string `json:"suggest,omitempty"`  // word suggestions: "auto" opens the list while typing
	Ghost    string `json:"ghost,omitempty"`    // "on": the best word is shown faintly after the caret, Tab accepts it
	Speed    string `json:"speed,omitempty"`    // reading aloud: "slow", "fast"
	Typing   string `json:"typing,omitempty"`   // what is said of typed text: "words", "characters"; "" nothing
	TypeFmt  string `json:"typefmt,omitempty"`  // "on": formatting changes at the caret are said
	TypeRead string `json:"typeread,omitempty"` // "on": the line is read when the caret moves
	Collab   string `json:"collab,omitempty"`   // what is announced of a shared session: "" everything, "important", "off"
	Memory   string `json:"memory,omitempty"`   // "low": fewer undo steps kept; "normal": the person turned it off; "" automatic (low on a device that reports 2 GB or less)
	Where    string `json:"where,omitempty"`    // "on": the presence list has a "Where is <name>?" button for each collaborator
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
	if p.Suggest != "auto" {
		p.Suggest = ""
	}
	if p.Ghost != "on" {
		p.Ghost = ""
	}
	if p.Speed != "slow" && p.Speed != "fast" {
		p.Speed = ""
	}
	if p.Typing != components.TypingWords && p.Typing != components.TypingCharacters {
		p.Typing = ""
	}
	if p.TypeFmt != "on" {
		p.TypeFmt = ""
	}
	if p.TypeRead != "on" {
		p.TypeRead = ""
	}
	if p.Collab != components.CollabImportant && p.Collab != components.CollabOff {
		p.Collab = ""
	}
	if p.Where != "on" {
		p.Where = ""
	}
	if p.Memory != "low" && p.Memory != "normal" {
		p.Memory = ""
	}
	return p
}

// lowMemory says whether undo history is kept short: the person asked for it, or has not chosen and the
// browser reports a device with 2 GB of memory or less (navigator.deviceMemory, where it exists).
func lowMemory(p prefs) bool {
	switch p.Memory {
	case "low":
		return true
	case "normal":
		return false
	}
	dm := js.Global().Get("navigator").Get("deviceMemory")
	return dm.Type() == js.TypeNumber && dm.Float() > 0 && dm.Float() <= 2
}

// lowMemorySteps is how many undo steps are kept in low-memory mode.
const lowMemorySteps = 20

// applyPrefs sets the preferences on the root element (an empty one removes its attribute) and the
// length of the undo history.
func applyPrefs(p prefs) {
	if lowMemory(p) {
		canvas.SetHistoryLimit(lowMemorySteps)
	} else {
		canvas.SetHistoryLimit(canvas.DefaultHistoryLimit)
	}
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
	typing := func(label, level string) components.MenuItem {
		return components.MenuItem{Label: label, Checked: curPrefs.Typing == level,
			OnSelect: change(label, func(p *prefs) { p.Typing = level })}
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
		toggle(T("prefs.collabWhere", "Shared editing: a \"Where is\" button for each person"), curPrefs.Where == "on", func(p *prefs, on bool) { p.Where = map[bool]string{true: "on"}[on] }),
		{Separator: true},
		typing(T("prefs.typingOff", "Typing: say nothing"), components.TypingOff),
		typing(T("prefs.typingWords", "Typing: say each word"), components.TypingWords),
		typing(T("prefs.typingChars", "Typing: say each character"), components.TypingCharacters),
		toggle(T("prefs.typingFormat", "Say formatting changes at the caret"), curPrefs.TypeFmt == "on", func(p *prefs, on bool) { p.TypeFmt = map[bool]string{true: "on"}[on] }),
		toggle(T("prefs.typingRead", "Read the line when the caret moves"), curPrefs.TypeRead == "on", func(p *prefs, on bool) { p.TypeRead = map[bool]string{true: "on"}[on] }),
		{Separator: true},
		toggle(T("prefs.suggestAuto", "Suggest words as I type"), curPrefs.Suggest == "auto", func(p *prefs, on bool) { p.Suggest = map[bool]string{true: "auto"}[on] }),
		toggle(T("prefs.ghost", "Show a suggestion after the caret (Tab accepts)"), curPrefs.Ghost == "on", func(p *prefs, on bool) { p.Ghost = map[bool]string{true: "on"}[on] }),
		toggle(T("prefs.targets", "Large targets"), curPrefs.Targets == "large", func(p *prefs, on bool) { p.Targets = map[bool]string{true: "large"}[on] }),
		toggle(T("prefs.memory", "Save memory (keep fewer undo steps)"), lowMemory(curPrefs), func(p *prefs, on bool) { p.Memory = map[bool]string{true: "low", false: "normal"}[on] }),
		toggle(T("prefs.focus", "Distraction-free mode"), curPrefs.Focus == "on", func(p *prefs, on bool) { p.Focus = map[bool]string{true: "on"}[on] }),
		{Separator: true},
		{Label: T("prefs.privacy", "Privacy settings…"), OnSelect: func() { u.tab = "privacy"; renderUI() }},
		{Separator: true},
		{Label: T("prefs.reset", "Reset to defaults"), OnSelect: change(T("a11y.prefsReset", "Display preferences reset"), func(p *prefs) { *p = prefs{} })},
	}
}
