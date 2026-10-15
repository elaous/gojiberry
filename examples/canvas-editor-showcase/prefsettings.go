//go:build js && wasm

package main

import (
	"strings"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// The preferences are described once, in prefSettings: the View > Display preferences menu and the Preferences
// tab (with its search) are both made from that list, so they cannot drift apart.

// choice is one value of a choice setting: what is kept, its name in the panel, and its line in the menu.
type choice struct{ value, label, menu string }

// setting is one preference. A toggle has on/setOn; a choice has choices, cur and set.
type setting struct {
	group, id, label string
	toggle           bool
	on               func(prefs) bool
	setOn            func(*prefs, bool)
	choices          []choice
	cur              func(prefs) string
	set              func(*prefs, string)
	noMenu           bool // not in the Display preferences menu (it has a menu of its own)
}

func onOff(on bool, v string) string {
	if on {
		return v
	}
	return ""
}

// prefGroups are the groups of settings in order, by id and name.
func prefGroups() [][2]string {
	return [][2]string{
		{"text", T("prefs.g.text", "Text and display")},
		{"interface", T("prefs.g.interface", "Language and theme")},
		{"shared", T("prefs.g.shared", "Shared editing")},
		{"typing", T("prefs.g.typing", "Typing and reading aloud")},
		{"access", T("prefs.g.access", "Pointer and switch access")},
		{"editor", T("prefs.g.editor", "Editor")},
	}
}

func prefSettings() []setting {
	toggle := func(group, id, label string, get func(prefs) bool, set func(*prefs, bool)) setting {
		return setting{group: group, id: id, label: label, toggle: true, on: get, setOn: set}
	}
	str := func(f func(*prefs) *string) (func(prefs) string, func(*prefs, string)) {
		return func(p prefs) string { return *f(&p) }, func(p *prefs, v string) { *f(p) = v }
	}
	textCur, textSet := str(func(p *prefs) *string { return &p.Text })
	collabCur, collabSet := str(func(p *prefs) *string { return &p.Collab })
	typingCur, typingSet := str(func(p *prefs) *string { return &p.Typing })
	themeCur, themeSet := str(func(p *prefs) *string { return &p.Theme })
	dwellCur, dwellSet := str(func(p *prefs) *string { return &p.Dwell })
	scanCur, scanSet := str(func(p *prefs) *string { return &p.Scan })
	var langs []choice
	langs = append(langs, choice{value: "", label: T("prefs.langAuto", "As the browser has it")})
	for _, c := range langCodes() {
		name := languageNames[c]
		if name == "" {
			name = c
		}
		langs = append(langs, choice{value: c, label: name})
	}
	return []setting{
		{group: "text", id: "text", label: T("prefs.l.text", "Text size"), choices: []choice{
			{"", T("prefs.o.default", "Default"), T("prefs.textDefault", "Text size: default")},
			{"large", "125%", T("prefs.textLarge", "Text size: 125%")},
			{"larger", "150%", T("prefs.textLarger", "Text size: 150%")},
			{"largest", "200%", T("prefs.textLargest", "Text size: 200%")}}, cur: textCur, set: textSet},
		toggle("text", "spacing", T("prefs.spacing", "Wide text spacing"), func(p prefs) bool { return p.Spacing == "wide" }, func(p *prefs, on bool) { p.Spacing = onOff(on, "wide") }),
		toggle("text", "contrast", T("prefs.contrast", "More contrast"), func(p prefs) bool { return p.Contrast == "more" }, func(p *prefs, on bool) { p.Contrast = onOff(on, "more") }),
		toggle("text", "motion", T("prefs.motion", "Reduce motion"), func(p prefs) bool { return p.Motion == "reduce" }, func(p *prefs, on bool) { p.Motion = onOff(on, "reduce") }),
		toggle("text", "targets", T("prefs.targets", "Large targets"), func(p prefs) bool { return p.Targets == "large" }, func(p *prefs, on bool) { p.Targets = onOff(on, "large") }),
		toggle("text", "focus", T("prefs.focus", "Distraction-free mode"), func(p prefs) bool { return p.Focus == "on" }, func(p *prefs, on bool) { p.Focus = onOff(on, "on") }),

		{group: "interface", id: "lang", label: T("prefs.l.lang", "Interface language"), choices: langs, noMenu: true,
			cur: func(p prefs) string {
				if p.Lang == "" {
					return ""
				}
				return locale
			}, set: func(p *prefs, v string) { p.Lang = v }},
		{group: "interface", id: "theme", label: T("prefs.l.theme", "Theme"), choices: []choice{
			{"", T("prefs.o.light", "Light"), T("prefs.themeLight", "Theme: light")},
			{"dark", T("prefs.o.dark", "Dark"), T("prefs.themeDark", "Theme: dark")}}, cur: themeCur, set: themeSet},

		{group: "shared", id: "collab", label: T("prefs.l.collab", "What is announced"), choices: []choice{
			{components.CollabAll, T("prefs.o.collabAll", "Everything"), T("prefs.collabAll", "Shared editing: announce everything")},
			{components.CollabImportant, T("prefs.o.collabImportant", "Only the connection"), T("prefs.collabImportant", "Shared editing: announce only the connection")},
			{components.CollabOff, T("prefs.o.collabOff", "Nothing"), T("prefs.collabOff", "Shared editing: announce nothing")}}, cur: collabCur, set: collabSet},
		toggle("shared", "colours", T("prefs.safeColours", "Colour-blind safe colours for other people's cursors"), func(p prefs) bool { return p.Colours == "safe" }, func(p *prefs, on bool) { p.Colours = onOff(on, "safe") }),
		toggle("shared", "where", T("prefs.collabWhere", "Shared editing: a \"Where is\" button for each person"), func(p prefs) bool { return p.Where == "on" }, func(p *prefs, on bool) { p.Where = onOff(on, "on") }),

		{group: "typing", id: "typing", label: T("prefs.l.typing", "While typing, say"), choices: []choice{
			{components.TypingOff, T("prefs.o.typingOff", "Nothing"), T("prefs.typingOff", "Typing: say nothing")},
			{components.TypingWords, T("prefs.o.typingWords", "Each word"), T("prefs.typingWords", "Typing: say each word")},
			{components.TypingCharacters, T("prefs.o.typingChars", "Each character"), T("prefs.typingChars", "Typing: say each character")}}, cur: typingCur, set: typingSet},
		toggle("typing", "typefmt", T("prefs.typingFormat", "Say formatting changes at the caret"), func(p prefs) bool { return p.TypeFmt == "on" }, func(p *prefs, on bool) { p.TypeFmt = onOff(on, "on") }),
		toggle("typing", "typeread", T("prefs.typingRead", "Read the line when the caret moves"), func(p prefs) bool { return p.TypeRead == "on" }, func(p *prefs, on bool) { p.TypeRead = onOff(on, "on") }),

		{group: "access", id: "dwell", label: T("prefs.l.dwell", "Click by resting the pointer"), choices: []choice{
			{"", T("prefs.o.accessOff", "Off"), T("prefs.dwellOff", "Dwell click: off")},
			{"fast", T("prefs.o.dwellFast", "After 0.7 seconds"), T("prefs.dwellFast", "Dwell click: after 0.7 seconds")},
			{"medium", T("prefs.o.dwellMedium", "After 1.2 seconds"), T("prefs.dwellMedium", "Dwell click: after 1.2 seconds")},
			{"slow", T("prefs.o.dwellSlow", "After 2 seconds"), T("prefs.dwellSlow", "Dwell click: after 2 seconds")}}, cur: dwellCur, set: dwellSet},
		{group: "access", id: "scan", label: T("prefs.l.scan", "Switch scanning (Space or Enter chooses)"), choices: []choice{
			{"", T("prefs.o.accessOff", "Off"), T("prefs.scanOff", "Switch scanning: off")},
			{"fast", T("prefs.o.scanFast", "One step per second"), T("prefs.scanFast", "Switch scanning: one step per second")},
			{"medium", T("prefs.o.scanMedium", "One step per 1.5 seconds"), T("prefs.scanMedium", "Switch scanning: one step per 1.5 seconds")},
			{"slow", T("prefs.o.scanSlow", "One step per 2.5 seconds"), T("prefs.scanSlow", "Switch scanning: one step per 2.5 seconds")}}, cur: scanCur, set: scanSet},

		toggle("editor", "suggest", T("prefs.suggestAuto", "Suggest words as I type"), func(p prefs) bool { return p.Suggest == "auto" }, func(p *prefs, on bool) { p.Suggest = onOff(on, "auto") }),
		toggle("editor", "hint", T("prefs.hint", "Offer a draft description for a picture (made on this device)"), func(p prefs) bool { return p.Hint == "on" }, func(p *prefs, on bool) { p.Hint = onOff(on, "on") }),
		toggle("editor", "ghost", T("prefs.ghost", "Show a suggestion after the caret (Tab accepts)"), func(p prefs) bool { return p.Ghost == "on" }, func(p *prefs, on bool) { p.Ghost = onOff(on, "on") }),
		toggle("editor", "close", T("prefs.autoClose", "Add the closing bracket or quote when I type an opening one"), func(p prefs) bool { return p.Close != "off" }, func(p *prefs, on bool) { p.Close = onOff(!on, "off") }),
		toggle("editor", "memory", T("prefs.memory", "Save memory (keep fewer undo steps)"), lowMemory, func(p *prefs, on bool) { p.Memory = map[bool]string{true: "low", false: "normal"}[on] }),
		toggle("editor", "undoMany", T("prefs.undoMany", "Keep many undo steps (1000)"), func(p prefs) bool { return p.Memory == "high" }, func(p *prefs, on bool) { p.Memory = map[bool]string{true: "high", false: "normal"}[on] }),
	}
}

// change applies f to the preferences in force, keeps them, says msg and draws again.
func changePrefs(u *ui, renderUI func(), msg string, f func(*prefs)) {
	p := curPrefs
	f(&p)
	setPrefs(p)
	u.announce(msg)
	renderUI()
}

// prefsMenu is the display preferences: made from prefSettings, with the file import and export, the privacy
// settings and a reset.
func prefsMenu(u *ui, renderUI func()) []components.MenuItem {
	var items []components.MenuItem
	lastGroup := ""
	for _, s := range prefSettings() {
		if s.noMenu {
			continue
		}
		if lastGroup != "" && s.group != lastGroup {
			items = append(items, components.MenuItem{Separator: true})
		}
		lastGroup = s.group
		if s.toggle {
			on := s.on(curPrefs)
			items = append(items, components.MenuItem{Label: s.label, Checked: on, OnSelect: func() {
				state := T("a11y.off", "off")
				if !on {
					state = T("a11y.on", "on")
				}
				changePrefs(u, renderUI, s.label+": "+state, func(p *prefs) { s.setOn(p, !on) })
			}})
			continue
		}
		cur := s.cur(curPrefs)
		for _, c := range s.choices {
			items = append(items, components.MenuItem{Label: c.menu, Checked: c.value == cur, OnSelect: func() {
				msg := c.menu
				if s.id == "text" {
					msg = T("a11y.textSize", "Text size: %s", c.menu)
				}
				changePrefs(u, renderUI, msg, func(p *prefs) { s.set(p, c.value) })
			}})
		}
	}
	items = append(items,
		components.MenuItem{Separator: true},
		components.MenuItem{Label: T("prefs.panel", "All preferences…"), OnSelect: func() { u.tab = "prefs"; renderUI() }},
		components.MenuItem{Label: T("prefs.export", "Export these preferences…"), OnSelect: func() { exportPrefs(u) }},
		components.MenuItem{Label: T("prefs.import", "Import preferences from a file…"), OnSelect: func() { importPrefs(u, renderUI) }},
		components.MenuItem{Separator: true},
		components.MenuItem{Label: T("prefs.privacy", "Privacy settings…"), OnSelect: func() { u.tab = "privacy"; renderUI() }},
		components.MenuItem{Separator: true},
		components.MenuItem{Label: T("prefs.reset", "Reset to defaults"), OnSelect: func() {
			changePrefs(u, renderUI, T("a11y.prefsReset", "Display preferences reset"), func(p *prefs) { *p = prefs{} })
		}})
	return items
}

func exportPrefs(u *ui) {
	transfer.Download("display-preferences.json", "application/json", encodePrefs(curPrefs))
	u.announce(T("a11y.prefsExported", "Preferences saved to a file"))
}

func importPrefs(u *ui, renderUI func()) {
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
}

// matches says whether a setting (or its group) answers the words typed in the search box: every word must
// be in the setting's name, its group's name or one of its values.
func (s setting) matches(group, query string) bool {
	hay := strings.ToLower(s.label + " " + group)
	for _, c := range s.choices {
		hay += " " + strings.ToLower(c.label+" "+c.menu)
	}
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// prefsPanel is the Preferences tab: every setting, grouped, with a search box that narrows the list.
func prefsPanel(u *ui, renderUI func()) xango.Node {
	kids := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("prefs.title", "Preferences"))),
		xango.El("label", []xango.Attr{xango.A("class", "gj-pagesetup-field")},
			xango.El("span", nil, xango.Text(T("prefs.search", "Search the preferences"))),
			xango.El("input", []xango.Attr{xango.A("type", "search"), xango.A("class", "gj-prefs-search"), xango.A("value", u.prefsQuery), xango.A("autocomplete", "off"),
				xango.On("input", func(e xango.Event) { u.prefsQuery = xango.EventValue[string](e); renderUI() })})),
	}
	settings := prefSettings()
	shown := 0
	for _, g := range prefGroups() {
		var rows []xango.Node
		for _, s := range settings {
			if s.group != g[0] || !s.matches(g[1], u.prefsQuery) {
				continue
			}
			rows = append(rows, prefRow(u, renderUI, s))
		}
		if len(rows) == 0 {
			continue
		}
		shown += len(rows)
		kids = append(kids, xango.El("section", []xango.Attr{xango.A("class", "gj-prefs-group"), xango.A("aria-label", g[1])},
			append([]xango.Node{xango.El("h5", nil, xango.Text(g[1]))}, rows...)...))
	}
	status := T("prefs.none", "No preference matches.")
	if shown > 0 {
		status = Tn("prefs.count", "%d preference", "%d preferences", shown)
	}
	kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-prefs-count"), xango.A("role", "status")}, xango.Text(status)),
		xango.El("div", []xango.Attr{xango.A("class", "gj-prefs-actions"), xango.A("style", "display:flex;gap:6px;flex-wrap:wrap")},
			prefsButton("gj-prefs-export", T("prefs.exportShort", "Export to a file"), func() { exportPrefs(u) }),
			prefsButton("gj-prefs-import", T("prefs.importShort", "Import from a file"), func() { importPrefs(u, renderUI) }),
			prefsButton("gj-prefs-reset", T("prefs.reset", "Reset to defaults"), func() {
				changePrefs(u, renderUI, T("a11y.prefsReset", "Display preferences reset"), func(p *prefs) { *p = prefs{} })
			})))
	return xango.El("section", []xango.Attr{xango.A("class", "gj-prefs"), xango.A("aria-label", T("prefs.title", "Preferences"))}, kids...)
}

func prefsButton(class, label string, on func()) xango.Node {
	return xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button "+class),
		xango.On("click", func(xango.Event) { on() })}, xango.Text(label))
}

func prefRow(u *ui, renderUI func(), s setting) xango.Node {
	if s.toggle {
		on := s.on(curPrefs)
		return components.Checkbox(components.CheckboxProps{Label: s.label, Checked: on, OnChange: func(v bool) {
			state := T("a11y.off", "off")
			if v {
				state = T("a11y.on", "on")
			}
			changePrefs(u, renderUI, s.label+": "+state, func(p *prefs) { s.setOn(p, v) })
		}})
	}
	cur := s.cur(curPrefs)
	var opts []xango.Node
	for _, c := range s.choices {
		a := []xango.Attr{xango.A("value", c.value)}
		if c.value == cur {
			a = append(a, xango.A("selected", "selected"))
		}
		opts = append(opts, xango.El("option", a, xango.Text(c.label)))
	}
	return xango.El("label", []xango.Attr{xango.A("class", "gj-pagesetup-field")},
		xango.El("span", nil, xango.Text(s.label)),
		xango.El("select", []xango.Attr{xango.A("class", "gj-prefs-"+s.id), xango.On("change", func(e xango.Event) {
			v := xango.EventValue[string](e)
			label := v
			for _, c := range s.choices {
				if c.value == v {
					label = c.label
				}
			}
			changePrefs(u, renderUI, s.label+": "+label, func(p *prefs) { s.set(p, v) })
		})}, opts...))
}
