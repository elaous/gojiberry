package components

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

// KeyBindingsProps configures KeyBindings.
type KeyBindingsProps struct {
	// Commands are the commands to list (canvas.Registry.Commands, plus
	// any built-in ones such as bold); their Title is shown.
	Commands []canvas.CommandInfo
	// Keymap is what is bound now (canvas.Stage.EffectiveKeymap) and
	// Defaults what it started as (nil to hide the "modified" marks and
	// reset buttons).
	Keymap, Defaults canvas.Keymap
	// Query filters the list by title, feature or chord.
	Query string
	// Recording is the command waiting for a key press ("" for none).
	// While it is set the row shows a capture box that takes the next
	// chord: the box reports it with OnBind.
	Recording canvas.Command
	// Message is a line shown above the list (a conflict warning).
	Message string
	// Mac shows chords the Mac way.
	Mac bool

	OnQuery    func(query string)
	OnRecord   func(cmd canvas.Command) // start recording; "" cancels
	OnBind     func(cmd canvas.Command, chord string)
	OnClear    func(cmd canvas.Command)
	OnReset    func(cmd canvas.Command)
	OnResetAll func()
}

// KeyBindings renders the key bindings editor: every command with its
// shortcut, a search box, and per row Change (press the new keys), Clear
// and Reset. It is controlled: the caller owns Recording and applies
// OnBind/OnClear/OnReset to the Stage (canvas.Stage.Rebind) and says what
// happened in Message. A modifier-only press is ignored and Escape
// cancels.
func KeyBindings(props KeyBindingsProps) gox.Node {
	cmds := append([]canvas.CommandInfo(nil), props.Commands...)
	sort.SliceStable(cmds, func(i, j int) bool {
		if cmds[i].Feature != cmds[j].Feature {
			return cmds[i].Feature < cmds[j].Feature
		}
		return cmds[i].Title < cmds[j].Title
	})
	words := strings.Fields(strings.ToLower(props.Query))
	var rows []gox.Node
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

		var keyCell gox.Node
		if props.Recording == c.Name {
			keyCell = gox.El("div", []gox.Attr{
				gox.A("class", "gj-keybindings-capture"), gox.A("tabindex", "0"), gox.A("autofocus", "true"),
				gox.A("data-capture", string(c.Name)),
				gox.On("keydown", func(e gox.Event) {
					k, ok := e.Value.(gox.KeyData)
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
				gox.On("blur", func(gox.Event) {
					if props.OnRecord != nil {
						props.OnRecord("")
					}
				}),
			}, gox.Text("Press the new shortcut… (Esc cancels)"))
		} else if len(shown) == 0 {
			keyCell = gox.El("span", []gox.Attr{gox.A("class", "gj-keybindings-none")}, gox.Text("—"))
		} else {
			var keys []gox.Node
			for _, s := range shown {
				keys = append(keys, gox.El("kbd", nil, gox.Text(s)))
			}
			keyCell = gox.El("span", []gox.Attr{gox.A("class", "gj-keybindings-keys")}, keys...)
		}

		btn := func(label, action string, on func()) gox.Node {
			attrs := []gox.Attr{gox.A("class", "gj-keybindings-button"), gox.A("type", "button"), gox.A("data-action", action+":"+string(c.Name))}
			if on != nil {
				attrs = append(attrs, gox.On("click", func(gox.Event) { on() }))
			}
			return gox.El("button", attrs, gox.Text(label))
		}
		actions := []gox.Node{btn("Change", "change", func() {
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
		rows = append(rows, gox.El("tr", []gox.Attr{gox.A("class", class)},
			gox.El("td", []gox.Attr{gox.A("class", "gj-keybindings-title")}, gox.Text(c.Title),
				gox.El("small", nil, gox.Text(" "+featureTitle(c.Feature)))),
			gox.El("td", nil, keyCell),
			gox.El("td", []gox.Attr{gox.A("class", "gj-keybindings-actions")}, actions...),
		))
	}

	search := []gox.Attr{gox.A("class", "gj-keybindings-search"), gox.A("type", "search"), gox.A("placeholder", "Search commands or keys"), gox.A("value", props.Query)}
	if props.OnQuery != nil {
		search = append(search, gox.On("input", func(e gox.Event) { props.OnQuery(gox.EventValue[string](e)) }))
	}
	head := []gox.Node{gox.El("input", search)}
	if props.OnResetAll != nil {
		head = append(head, gox.El("button", []gox.Attr{
			gox.A("class", "gj-keybindings-button"), gox.A("type", "button"), gox.A("data-action", "reset-all"),
			gox.On("click", func(gox.Event) { props.OnResetAll() }),
		}, gox.Text("Reset all")))
	}
	out := []gox.Node{gox.El("div", []gox.Attr{gox.A("class", "gj-keybindings-head")}, head...)}
	if props.Message != "" {
		out = append(out, gox.El("p", []gox.Attr{gox.A("class", "gj-keybindings-message"), gox.A("role", "status")}, gox.Text(props.Message)))
	}
	out = append(out, gox.El("table", []gox.Attr{gox.A("class", "gj-keybindings-table")}, gox.El("tbody", nil, rows...)))
	return gox.El("div", []gox.Attr{gox.A("class", "gj-keybindings")}, out...)
}

// ChordFromKey builds the chord string (canvas.Chord) for a key press,
// reading letters and digits from the physical key the way the canvas
// does. ok is false for a press of a modifier on its own, and for one
// with no Ctrl, Cmd or Alt (the canvas only runs shortcuts that have one).
func ChordFromKey(k gox.KeyData) (chord string, ok bool) {
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
