package components

import (
	"github.com/elaous/gojiberry/canvas"
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

type ToolbarItem struct {
	// ID names the button for tests and styling (data-cmd).
	ID    string
	Label string
	// Icon is the Material Symbols name (see Material).
	Icon            string
	Shortcut        string
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

func IconToolbar(p IconToolbarProps) xango.Node {
	id := p.ID
	if id == "" {
		id = "gj-icontoolbar"
	}
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
