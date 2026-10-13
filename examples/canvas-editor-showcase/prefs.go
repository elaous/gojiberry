//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/transfer"
)

// Display preferences. They are kept in this browser (localStorage) and set as data-gj-* attributes
// on the root element, which components/a11y.css turns into styles. A person who sets none gets the
// operating system's own settings (reduced motion, more contrast) through the media queries there.

const prefsKey = "showcase:a11y"

// attrs are the values the root element carries.
func (p prefs) attrs() map[string]string {
	return map[string]string{"data-gj-motion": p.Motion, "data-gj-contrast": p.Contrast, "data-gj-text": p.Text, "data-gj-spacing": p.Spacing, "data-gj-focus": p.Focus, "data-gj-targets": p.Targets}
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
		toggle(T("prefs.undoMany", "Keep many undo steps (1000)"), curPrefs.Memory == "high", func(p *prefs, on bool) { p.Memory = map[bool]string{true: "high", false: "normal"}[on] }),
		toggle(T("prefs.focus", "Distraction-free mode"), curPrefs.Focus == "on", func(p *prefs, on bool) { p.Focus = map[bool]string{true: "on"}[on] }),
		{Separator: true},
		{Label: T("prefs.export", "Export these preferences…"), OnSelect: func() {
			transfer.Download("display-preferences.json", "application/json", encodePrefs(curPrefs))
			u.announce(T("a11y.prefsExported", "Preferences saved to a file"))
		}},
		{Label: T("prefs.import", "Import preferences from a file…"), OnSelect: func() {
			pickPrefsFile(func(data []byte) {
				p, err := parsePrefs(data)
				if err != nil {
					u.announce(T("a11y.prefsBad", "That file is not a preferences file"))
					renderUI()
					return
				}
				setPrefs(p)
				u.announce(T("a11y.prefsImported", "Preferences imported"))
				renderUI()
			})
		}},
		{Separator: true},
		{Label: T("prefs.privacy", "Privacy settings…"), OnSelect: func() { u.tab = "privacy"; renderUI() }},
		{Separator: true},
		{Label: T("prefs.reset", "Reset to defaults"), OnSelect: change(T("a11y.prefsReset", "Display preferences reset"), func(p *prefs) { *p = prefs{} })},
	}
}
