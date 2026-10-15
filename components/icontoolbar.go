package components

import (
	"github.com/elaous/gojiberry/canvas"
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// ToolbarItem is one button of an IconToolbar, or a gap between groups.
type ToolbarItem struct {
	// ID names the button for tests and styling (data-cmd).
	ID string
	// Label is the button's name for a screen reader and for the tooltip ("Bold"). The icon alone carries no
	// name, so an item without a label draws nothing.
	Label string
	// Icon is the Material Symbols name (see Material).
	Icon string
	// Shortcut is the key chord the same command has ("Ctrl+B"): added to the tooltip and announced.
	Shortcut string
	// Toggle makes the button a switch that is on or off (aria-pressed), for bold and the like; Pressed says which.
	Toggle, Pressed bool
	Disabled        bool
	// Separator draws a gap between groups instead of a button.
	Separator bool
	OnClick   func()
}

// IconToolbarProps configures IconToolbar.
type IconToolbarProps struct {
	// ID is the element id of the toolbar.
	ID string
	// Label names the toolbar for a screen reader ("Formatting").
	Label string
	Items []ToolbarItem
}

// IconToolbar is a toolbar of icon buttons for the commands a person uses most (the formatting ones), in the
// pattern the WAI-ARIA authoring practices give: one stop in the tab order, then the arrow keys, Home and End move
// between the buttons (a button that is disabled is skipped), and Tab leaves the toolbar. Each button has its
// name as an aria-label and a tooltip that adds the shortcut; a toggle says whether it is on with aria-pressed,
// not by colour alone.
func IconToolbar(p IconToolbarProps) xango.Node {
	id := p.ID
	if id == "" {
		id = "gj-icontoolbar"
	}
	// the way in: the first button that can be pressed, or the one that is on
	entry := -1
	for i, it := range p.Items {
		if it.Separator || it.Label == "" || it.Disabled {
			continue
		}
		if entry < 0 {
			entry = i
		}
	}
	var kids []xango.Node
	for i, it := range p.Items {
		if it.Separator {
			kids = append(kids, xango.El("span", []xango.Attr{xango.A("class", "gj-icontoolbar-gap"), xango.A("role", "separator"), xango.A("aria-orientation", "vertical")}))
			continue
		}
		if it.Label == "" {
			continue
		}
		title := it.Label
		if it.Shortcut != "" {
			title += " (" + it.Shortcut + ")"
		}
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-icontoolbar-button"), xango.A("id", id+"-"+strconv.Itoa(i)),
			xango.A("aria-label", it.Label), xango.A("title", title), rovingTab(i, entry)}
		if it.ID != "" {
			attrs = append(attrs, xango.A("data-cmd", it.ID))
		}
		if it.Shortcut != "" {
			attrs = append(attrs, xango.A("aria-keyshortcuts", it.Shortcut))
		}
		if it.Toggle {
			attrs = append(attrs, xango.A("aria-pressed", strconv.FormatBool(it.Pressed)))
			if it.Pressed {
				attrs[1] = xango.A("class", "gj-icontoolbar-button gj-icontoolbar-on")
			}
		}
		switch {
		case it.Disabled:
			attrs = append(attrs, xango.A("disabled", "disabled"))
		case it.OnClick != nil:
			on := it.OnClick
			attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
		}
		kids = append(kids, xango.El("button", attrs, Material(it.Icon)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-icontoolbar"), xango.A("id", id), xango.A("role", "toolbar"), xango.A("aria-label", p.Label),
		xango.A("aria-orientation", "horizontal"), gridKeys("#"+id, "#"+id+" button:not(:disabled)")}, kids...)
}

// CommandToolbarItems makes the items of an IconToolbar from editing commands, in groups (a gap between groups). The
// name, the icon and the shortcut of each are the ones the Edit menu shows for the command (CommandMenu): the title
// from the registry, translated as "cmd.<name>", the icon of CommandMaterial and the first chord of the keymap, so
// the button, the menu and the key cannot disagree. A command the registry lacks is left out (a feature that was
// not installed). state says, for a command, whether it is a toggle and on, and whether it is unavailable now.
func CommandToolbarItems(cmds []canvas.CommandInfo, keymap canvas.Keymap, mac bool, groups [][]canvas.Command,
	state func(canvas.Command) (toggle, pressed, disabled bool), run func(canvas.Command)) []ToolbarItem {
	title := map[canvas.Command]string{}
	for _, c := range cmds {
		title[c.Name] = c.Title
	}
	var out []ToolbarItem
	for _, g := range groups {
		start := len(out)
		for _, name := range g {
			t, ok := title[name]
			if !ok {
				continue
			}
			it := ToolbarItem{ID: string(name), Label: tr("cmd."+string(name), t), Icon: CommandMaterial(string(name)), OnClick: func() { run(name) }}
			if chords := keymap.Chords(name); len(chords) > 0 {
				it.Shortcut = FormatChord(chords[0], mac)
			}
			if state != nil {
				it.Toggle, it.Pressed, it.Disabled = state(name)
			}
			out = append(out, it)
		}
		if len(out) > start {
			out = append(out, ToolbarItem{Separator: true})
		}
	}
	if n := len(out); n > 0 && out[n-1].Separator {
		out = out[:n-1]
	}
	return out
}
