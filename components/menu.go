package components

import (
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

// MenuItem is one entry of a Menu: a command, a separator, or (with
// Items) a submenu.
type MenuItem struct {
	Label string
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
func Menu(props MenuProps) gox.Node {
	attrs := []gox.Attr{gox.A("class", "gj-menu"), gox.A("role", "menu")}
	if props.Label != "" {
		attrs = append(attrs, gox.A("aria-label", props.Label))
	}
	var items []gox.Node
	for _, it := range props.Items {
		items = append(items, menuItem(it, props.OnClose))
	}
	return gox.El("ul", attrs, items...)
}

func menuItem(it MenuItem, onClose func()) gox.Node {
	if it.Separator {
		return gox.El("li", []gox.Attr{gox.A("class", "gj-menu-separator"), gox.A("role", "separator")})
	}
	class := "gj-menu-item"
	attrs := []gox.Attr{gox.A("role", "menuitem")}
	if it.Disabled {
		class += " gj-menu-item-disabled"
		attrs = append(attrs, gox.A("aria-disabled", "true"))
	}
	if len(it.Items) > 0 {
		class += " gj-menu-item-sub"
		attrs = append(attrs, gox.A("aria-haspopup", "menu"), gox.A("tabindex", "0"))
	} else if !it.Disabled && it.OnSelect != nil {
		sel := it.OnSelect
		attrs = append(attrs, gox.A("tabindex", "0"), gox.On("click", func(gox.Event) {
			sel()
			if onClose != nil {
				onClose()
			}
		}))
	}
	if it.Checked {
		attrs = append(attrs, gox.A("aria-checked", "true"))
	}
	attrs = append([]gox.Attr{gox.A("class", class)}, attrs...)
	check := ""
	if it.Checked {
		check = "✓"
	}
	row := []gox.Node{
		gox.El("span", []gox.Attr{gox.A("class", "gj-menu-check")}, gox.Text(check)),
		gox.El("span", []gox.Attr{gox.A("class", "gj-menu-label")}, gox.Text(it.Label)),
	}
	switch {
	case len(it.Items) > 0:
		row = append(row, gox.El("span", []gox.Attr{gox.A("class", "gj-menu-arrow"), gox.A("aria-hidden", "true")}, gox.Text("▸")))
		row = append(row, Menu(MenuProps{Items: it.Items, OnClose: onClose}))
	case it.Shortcut != "":
		row = append(row, gox.El("span", []gox.Attr{gox.A("class", "gj-menu-shortcut")}, gox.Text(it.Shortcut)))
	}
	return gox.El("li", attrs, row...)
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
func MenuBar(props MenuBarProps) gox.Node {
	var entries []gox.Node
	for i, e := range props.Entries {
		i, e := i, e
		open := i == props.Open
		class := "gj-menubar-entry"
		if open {
			class += " gj-menubar-entry-open"
		}
		btn := gox.El("button", []gox.Attr{
			gox.A("class", "gj-menubar-button"), gox.A("type", "button"), gox.A("role", "menuitem"),
			gox.A("aria-haspopup", "menu"), gox.A("aria-expanded", map[bool]string{true: "true", false: "false"}[open]),
			gox.On("click", func(gox.Event) {
				if props.OnOpen != nil {
					props.OnOpen(map[bool]int{true: -1, false: i}[open])
				}
			}),
			gox.On("mouseenter", func(gox.Event) {
				if props.Open >= 0 && !open && props.OnOpen != nil {
					props.OnOpen(i)
				}
			}),
		}, gox.Text(e.Label))
		children := []gox.Node{btn}
		if open {
			children = append(children, gox.El("div", []gox.Attr{gox.A("class", "gj-menubar-panel")},
				Menu(MenuProps{Items: e.Items, Label: e.Label, OnClose: func() {
					if props.OnOpen != nil {
						props.OnOpen(-1)
					}
				}})))
		}
		entries = append(entries, gox.El("div", []gox.Attr{gox.A("class", class)}, children...))
	}
	nodes := []gox.Node{gox.El("nav", []gox.Attr{gox.A("class", "gj-menubar"), gox.A("role", "menubar")}, entries...)}
	if props.Open >= 0 {
		nodes = append([]gox.Node{gox.El("div", []gox.Attr{
			gox.A("class", "gj-menubar-backdrop"),
			gox.On("click", func(gox.Event) {
				if props.OnOpen != nil {
					props.OnOpen(-1)
				}
			}),
		})}, nodes...)
	}
	return gox.Frag(nodes...)
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
			Label: c.Title, Shortcut: shortcut, OnSelect: func() { run(c.Name) },
		})
	}
	var out []MenuItem
	for _, f := range order {
		out = append(out, MenuItem{Label: featureTitle(f), Items: byFeature[f]})
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
