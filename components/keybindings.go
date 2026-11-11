package components

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// KeyBindingsProps configures KeyBindings.
type KeyBindingsProps struct {
	Commands         []canvas.CommandInfo
	Keymap, Defaults canvas.Keymap
	// Query filters the list by title, feature or chord.
	Query     string
	Recording canvas.Command
	Message   string
	// Mac shows chords the Mac way.
	Mac bool

	OnQuery    func(query string)
	OnRecord   func(cmd canvas.Command) // start recording; "" cancels
	OnBind     func(cmd canvas.Command, chord string)
	OnClear    func(cmd canvas.Command)
	OnReset    func(cmd canvas.Command)
	OnResetAll func()
}

func KeyBindings(props KeyBindingsProps) xango.Node {
	cmds := append([]canvas.CommandInfo(nil), props.Commands...)
	sort.SliceStable(cmds, func(i, j int) bool {
		if cmds[i].Feature != cmds[j].Feature {
			return cmds[i].Feature < cmds[j].Feature
		}
		return cmds[i].Title < cmds[j].Title
	})
	words := strings.Fields(strings.ToLower(props.Query))
	var rows []xango.Node
	for _, c := range cmds {
		c := c
		chords := props.Keymap.Chords(c.Name)
		shown := make([]string, len(chords))
		for i, ch := range chords {
			shown[i] = FormatChord(ch, props.Mac)
		}
		hay := strings.ToLower(c.Title + " " + c.Feature + " " + strings.Join(shown, " ") + " " + string(c.Name))
		skip := false
		for _, w := range words {
			skip = skip || !strings.Contains(hay, w)
		}
		if skip {
			continue
		}
		modified := props.Defaults != nil && strings.Join(chords, ",") != strings.Join(props.Defaults.Chords(c.Name), ",")

		var keyCell xango.Node
		if props.Recording == c.Name {
			keyCell = xango.El("div", []xango.Attr{
				xango.A("class", "gj-keybindings-capture"), xango.A("tabindex", "0"), xango.A("autofocus", "true"),
				xango.A("data-capture", string(c.Name)),
				xango.On("keydown", func(e xango.Event) {
					k, ok := e.Value.(xango.KeyData)
					if !ok {
						return
					}
					e.PreventDefault()
					if k.Key == "Escape" {
						if props.OnRecord != nil {
							props.OnRecord("")
						}
						return
					}
					chord, ok := ChordFromKey(k)
					if ok && props.OnBind != nil {
						props.OnBind(c.Name, chord)
					}
				}),
				xango.On("blur", func(xango.Event) {
					if props.OnRecord != nil {
						props.OnRecord("")
					}
				}),
			}, xango.Text("Press the new shortcut… (Esc cancels)"))
		} else if len(shown) == 0 {
			keyCell = xango.El("span", []xango.Attr{xango.A("class", "gj-keybindings-none")}, xango.Text("—"))
		} else {
			var keys []xango.Node
			for _, s := range shown {
				keys = append(keys, xango.El("kbd", nil, xango.Text(s)))
			}
			keyCell = xango.El("span", []xango.Attr{xango.A("class", "gj-keybindings-keys")}, keys...)
		}

		btn := func(label, action string, on func()) xango.Node {
			attrs := []xango.Attr{xango.A("class", "gj-keybindings-button"), xango.A("type", "button"), xango.A("data-action", action+":"+string(c.Name))}
			if on != nil {
				attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
			}
			return xango.El("button", attrs, xango.Text(label))
		}
		actions := []xango.Node{btn("Change", "change", func() {
			if props.OnRecord != nil {
				props.OnRecord(c.Name)
			}
		})}
		if len(chords) > 0 {
			actions = append(actions, btn("Clear", "clear", func() {
				if props.OnClear != nil {
					props.OnClear(c.Name)
				}
			}))
		}
		if modified {
			actions = append(actions, btn("Reset", "reset", func() {
				if props.OnReset != nil {
					props.OnReset(c.Name)
				}
			}))
		}
		class := "gj-keybindings-row"
		if modified {
			class += " gj-keybindings-row-modified"
		}
		rows = append(rows, xango.El("tr", []xango.Attr{xango.A("class", class)},
			xango.El("td", []xango.Attr{xango.A("class", "gj-keybindings-title")}, xango.Text(c.Title),
				xango.El("small", nil, xango.Text(" "+featureTitle(c.Feature)))),
			xango.El("td", nil, keyCell),
			xango.El("td", []xango.Attr{xango.A("class", "gj-keybindings-actions")}, actions...),
		))
	}

	search := []xango.Attr{xango.A("class", "gj-keybindings-search"), xango.A("type", "search"), xango.A("placeholder", "Search commands or keys"), xango.A("value", props.Query)}
	if props.OnQuery != nil {
		search = append(search, xango.On("input", func(e xango.Event) { props.OnQuery(xango.EventValue[string](e)) }))
	}
	head := []xango.Node{xango.El("input", search)}
	if props.OnResetAll != nil {
		head = append(head, xango.El("button", []xango.Attr{
			xango.A("class", "gj-keybindings-button"), xango.A("type", "button"), xango.A("data-action", "reset-all"),
			xango.On("click", func(xango.Event) { props.OnResetAll() }),
		}, xango.Text("Reset all")))
	}
	out := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-keybindings-head")}, head...)}
	if props.Message != "" {
		out = append(out, xango.El("p", []xango.Attr{xango.A("class", "gj-keybindings-message"), xango.A("role", "status")}, xango.Text(props.Message)))
	}
	out = append(out, xango.El("table", []xango.Attr{xango.A("class", "gj-keybindings-table")}, xango.El("tbody", nil, rows...)))
	return xango.El("div", []xango.Attr{xango.A("class", "gj-keybindings")}, out...)
}

func ChordFromKey(k xango.KeyData) (chord string, ok bool) {
	switch k.Key {
	case "Control", "Shift", "Alt", "Meta", "AltGraph", "CapsLock", "Dead":
		return "", false
	}
	if !k.Ctrl && !k.Alt && !k.Meta {
		return "", false
	}
	key := k.Key
	if strings.HasPrefix(k.Code, "Key") && len(k.Code) == 4 {
		key = k.Code[3:]
	}
	if strings.HasPrefix(k.Code, "Digit") && len(k.Code) == 6 {
		key = k.Code[5:]
	}
	if key == " " {
		key = "Space"
	}
	return canvas.Chord(key, k.Ctrl, k.Shift, k.Alt, k.Meta), true
}
