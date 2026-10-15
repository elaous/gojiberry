package components

import (
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// MenuItem is one entry of a Menu: a command, a separator, or (with
// Items) a submenu.
type MenuItem struct {
	Label string
	// Icon is the name of a Material Symbols icon drawn before the label (see Material);
	// "" for none.
	Icon string
	// Shortcut is shown at the right ("Ctrl+B"); see FormatChord.
	Shortcut string
	// Checked puts a check mark in front (a toggle that is on).
	Checked   bool
	Disabled  bool
	Separator bool
	OnSelect  func()
	// Items makes this entry a submenu, opened when it is hovered or
	// has the keyboard focus (pure CSS: no state to keep).
	Items []MenuItem
}

// MenuProps configures Menu.
type MenuProps struct {
	Items []MenuItem
	// OnClose is called after an entry is chosen, so a popup can close.
	OnClose func()
	// Label names the menu for assistive technology.
	Label string
}

// Menu renders a clean, lightweight menu: a list of entries with
// optional shortcuts, check marks, separators and nested submenus. It
// holds no state -- submenus open with CSS (:hover, :focus-within) -- and
// has no position of its own: put it in whatever popup or panel you
// like, or use MenuBar for a bar of them. It is built from plain <ul>/
// <li> with ARIA menu roles; its stylesheet is menu.css.
func Menu(props MenuProps) xango.Node {
	attrs := []xango.Attr{xango.A("class", "gj-menu"), xango.A("role", "menu")}
	if props.Label != "" {
		attrs = append(attrs, xango.A("aria-label", props.Label))
	}
	var items []xango.Node
	for _, it := range props.Items {
		items = append(items, menuItem(it, props.OnClose))
	}
	return xango.El("ul", attrs, items...)
}

// menuEntries selects the entries of the menu a key press happened in (its own, not a submenu's).
const menuEntries = ":scope > li.gj-menu-item[tabindex]"

// menuItemKey is the keyboard model of a menu entry. Up and Down move among the entries of the
// menu (wrapping), Home and End go to the ends, Right opens the submenu of an entry that has one and
// goes into it, Left goes back out to the entry that opened the submenu, and Enter or Space
// chooses the entry (choose is nil for an entry that only opens a submenu). A key with a
// modifier is left alone.
func menuItemKey(e xango.Event, choose func()) {
	k, ok := e.Value.(xango.KeyData)
	if !ok || k.Ctrl || k.Alt || k.Meta {
		return
	}
	// A key handled here must not go on to the entry that holds this one's submenu: its handler would move the
	// focus again, from the same target, and a submenu would be walked two entries at a time.
	move := func(step xango.FocusStep) {
		e.PreventDefault()
		e.StopPropagation()
		e.MoveFocus(".gj-menu", menuEntries, step)
	}
	switch k.Key {
	case "ArrowDown":
		move(xango.FocusNext)
	case "ArrowUp":
		move(xango.FocusPrev)
	case "Home":
		move(xango.FocusFirst)
	case "End":
		move(xango.FocusLast)
	case "ArrowRight":
		// into the submenu of the focused entry, if it has one
		if e.MoveFocus(".gj-menu-item-sub", ":scope > .gj-menu > li.gj-menu-item[tabindex]", xango.FocusFirst) {
			e.PreventDefault()
			e.StopPropagation()
		}
	case "ArrowLeft":
		// out of a submenu, to the entry that opened it
		if e.MoveFocus(".gj-menu-item-sub", ":scope", xango.FocusFirst) {
			e.PreventDefault()
			e.StopPropagation()
		}
	case "Enter", " ":
		if choose != nil {
			e.PreventDefault()
			e.StopPropagation()
			choose()
		}
	}
}

func menuItem(it MenuItem, onClose func()) xango.Node {
	if it.Separator {
		return xango.El("li", []xango.Attr{xango.A("class", "gj-menu-separator"), xango.A("role", "separator")})
	}
	class := "gj-menu-item"
	// aria-checked is allowed on a checkable menu item only.
	role := "menuitem"
	if it.Checked {
		role = "menuitemcheckbox"
	}
	attrs := []xango.Attr{xango.A("role", role)}
	if it.Disabled {
		class += " gj-menu-item-disabled"
		attrs = append(attrs, xango.A("aria-disabled", "true"))
	}
	if len(it.Items) > 0 {
		class += " gj-menu-item-sub"
		attrs = append(attrs, xango.A("aria-haspopup", "menu"), xango.A("tabindex", "0"),
			xango.On("keydown", func(e xango.Event) { menuItemKey(e, nil) }))
	} else if !it.Disabled && it.OnSelect != nil {
		sel := it.OnSelect
		choose := func() {
			sel()
			if onClose != nil {
				onClose()
			}
		}
		attrs = append(attrs, xango.A("tabindex", "0"), xango.On("click", func(xango.Event) { choose() }),
			// Enter and Space choose the focused entry, as a click does (a menu entry is not a
			// button, so the browser does not do it).
			xango.On("keydown", func(e xango.Event) { menuItemKey(e, choose) }))
	}
	if it.Checked {
		attrs = append(attrs, xango.A("aria-checked", "true"))
	}
	attrs = append([]xango.Attr{xango.A("class", class)}, attrs...)
	check := ""
	if it.Checked {
		check = "✓"
	}
	row := []xango.Node{xango.El("span", []xango.Attr{xango.A("class", "gj-menu-check")}, xango.Text(check))}
	if HasMaterial(it.Icon) {
		row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-menu-icon")}, Material(it.Icon)))
	}
	row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-menu-label")}, xango.Text(it.Label)))
	switch {
	case len(it.Items) > 0:
		row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-menu-arrow"), xango.A("aria-hidden", "true")}, xango.Text("▸")))
		row = append(row, Menu(MenuProps{Items: it.Items, OnClose: onClose}))
	case it.Shortcut != "":
		row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-menu-shortcut")}, xango.Text(it.Shortcut)))
	}
	return xango.El("li", attrs, row...)
}

// MenuBarEntry is one top-level menu of a MenuBar.
type MenuBarEntry struct {
	Label string
	Items []MenuItem
}

// MenuBarProps configures MenuBar.
type MenuBarProps struct {
	Entries []MenuBarEntry
	// Open is the index of the entry whose menu is showing, or -1.
	Open int
	// OnOpen is called with the entry to open: a click toggles (-1
	// closes), and moving onto another entry while one is open switches.
	OnOpen func(index int)
}

// MenuBar renders a row of menus (File, Edit, ...), at most one open at a
// time. It is controlled: the caller owns Open. A click anywhere outside
// closes it.
func MenuBar(props MenuBarProps) xango.Node {
	var entries []xango.Node
	for i, e := range props.Entries {
		i, e := i, e
		open := i == props.Open
		class := "gj-menubar-entry"
		if open {
			class += " gj-menubar-entry-open"
		}
		btn := xango.El("button", []xango.Attr{
			xango.A("class", "gj-menubar-button"), xango.A("type", "button"), xango.A("role", "menuitem"),
			xango.A("aria-haspopup", "menu"), xango.A("aria-expanded", map[bool]string{true: "true", false: "false"}[open]),
			xango.On("click", func(xango.Event) {
				if props.OnOpen != nil {
					props.OnOpen(map[bool]int{true: -1, false: i}[open])
				}
			}),
			xango.On("mouseenter", func(xango.Event) {
				if props.Open >= 0 && !open && props.OnOpen != nil {
					props.OnOpen(i)
				}
			}),
			// Left and Right move along the bar (taking an open menu along); Down, and Up, open
			// the menu and go into it, at its first and last entry.
			xango.On("keydown", func(e xango.Event) {
				k, ok := e.Value.(xango.KeyData)
				if !ok || k.Ctrl || k.Alt || k.Meta || props.OnOpen == nil {
					return
				}
				n := len(props.Entries)
				switch k.Key {
				case "ArrowRight", "ArrowLeft":
					e.PreventDefault()
					to := (i + 1) % n
					step := xango.FocusNext
					if k.Key == "ArrowLeft" {
						to, step = (i-1+n)%n, xango.FocusPrev
					}
					e.MoveFocus(".gj-menubar", ".gj-menubar-button", step)
					if props.Open >= 0 {
						props.OnOpen(to)
					}
				case "ArrowDown", "ArrowUp":
					e.PreventDefault()
					if !open {
						props.OnOpen(i)
					}
					step := xango.FocusFirst
					if k.Key == "ArrowUp" {
						step = xango.FocusLast
					}
					e.MoveFocus(".gj-menubar-entry", ".gj-menubar-panel > .gj-menu > li.gj-menu-item[tabindex]", step)
				}
			}),
		}, xango.Text(e.Label))
		children := []xango.Node{btn}
		if open {
			children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-menubar-panel")},
				Menu(MenuProps{Items: e.Items, Label: e.Label, OnClose: func() {
					if props.OnOpen != nil {
						props.OnOpen(-1)
					}
				}})))
		}
		entries = append(entries, xango.El("div", []xango.Attr{xango.A("class", class)}, children...))
	}
	// Escape closes the open menu from wherever the focus is inside it (keydown bubbles up to the bar).
	escape := xango.On("keydown", func(e xango.Event) {
		if k, ok := e.Value.(xango.KeyData); ok && k.Key == "Escape" && props.Open >= 0 && props.OnOpen != nil {
			e.PreventDefault()
			// the focus goes back to the button of the menu that closes, not to the page
			e.MoveFocus(".gj-menubar", ".gj-menubar-entry-open > .gj-menubar-button", xango.FocusFirst)
			props.OnOpen(-1)
		}
	})
	nodes := []xango.Node{xango.El("nav", []xango.Attr{xango.A("class", "gj-menubar"), xango.A("role", "menubar"), escape}, entries...)}
	if props.Open >= 0 {
		// After the bar, not before it: the bar keeps its place among its siblings when a menu
		// opens, so the browser keeps the focus on the button that was pressed (the bar is above the
		// backdrop by z-index either way).
		nodes = append(nodes, xango.El("div", []xango.Attr{
			xango.A("class", "gj-menubar-backdrop"),
			xango.On("click", func(xango.Event) {
				if props.OnOpen != nil {
					props.OnOpen(-1)
				}
			}),
		}))
	}
	return xango.Frag(nodes...)
}

// FormatChord writes a key chord ("Mod+Alt+Shift+K") for people: "Ctrl+
// Alt+Shift+K", or on a Mac "⌃⌥⇧K" with ⌘ for Mod.
func FormatChord(chord string, mac bool) string {
	if chord == "" {
		return ""
	}
	names := map[string]string{"ArrowUp": "↑", "ArrowDown": "↓", "ArrowLeft": "←", "ArrowRight": "→", "Enter": "Enter", "Escape": "Esc"}
	parts := strings.Split(chord, "+")
	for i, p := range parts {
		switch {
		case p == "Mod" && mac:
			parts[i] = "⌘"
		case p == "Mod":
			parts[i] = "Ctrl"
		case p == "Alt" && mac:
			parts[i] = "⌥"
		case p == "Shift" && mac:
			parts[i] = "⇧"
		case names[p] != "":
			parts[i] = names[p]
		}
	}
	if mac {
		return strings.Join(parts, "")
	}
	return strings.Join(parts, "+")
}

// CommandMenu turns registered commands (canvas.Registry.Commands) into
// menu entries, one submenu per feature, each entry showing its current
// shortcut from keymap (Stage.EffectiveKeymap) and running via run.
func CommandMenu(cmds []canvas.CommandInfo, keymap canvas.Keymap, mac bool, run func(canvas.Command)) []MenuItem {
	var order []string
	byFeature := map[string][]MenuItem{}
	for _, c := range cmds {
		c := c
		if _, ok := byFeature[c.Feature]; !ok {
			order = append(order, c.Feature)
		}
		shortcut := ""
		if chords := keymap.Chords(c.Name); len(chords) > 0 {
			shortcut = FormatChord(chords[0], mac)
		}
		byFeature[c.Feature] = append(byFeature[c.Feature], MenuItem{
			Label: tr("cmd."+string(c.Name), c.Title), Icon: CommandMaterial(string(c.Name)), Shortcut: shortcut, OnSelect: func() { run(c.Name) },
		})
	}
	var out []MenuItem
	for _, f := range order {
		out = append(out, MenuItem{Label: tr("feature."+f, featureTitle(f)), Items: byFeature[f]})
	}
	return out
}

// featureTitle turns a feature name ("markdown-clipboard") into a label
// ("Markdown clipboard").
func featureTitle(name string) string {
	s := strings.ReplaceAll(name, "-", " ")
	if s == "" {
		return "Other"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
