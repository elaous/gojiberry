package canvas

import (
	"sort"
	"strings"
	"unicode"
)

// Command is the name of something a key chord can ask a component to do.
type Command string

// The built-in commands. Copy, cut and paste are not here: they go
// through the browser's own clipboard events (see Copier, Cutter and
// Paster), which is how a page is allowed to touch the system clipboard.
const (
	CmdSelectAll   Command = "select-all"
	CmdBold        Command = "bold"
	CmdItalic      Command = "italic"
	CmdUnderline   Command = "underline"
	CmdStrike      Command = "strikethrough"
	CmdSuperscript Command = "superscript"
	CmdSubscript   Command = "subscript"
	CmdCopyFormat  Command = "copy-format"
	CmdPasteFormat Command = "paste-format"
)

// Keymap binds key chords to commands. A chord is written
// "Mod+Alt+Shift+Key": Mod is Ctrl (or Cmd on a Mac), letters are
// upper-case ("Mod+B"), other keys use their DOM names ("Mod+Enter",
// "Mod+."). Use Chord to build one from a key event.
type Keymap map[string]Command

// DefaultKeymap is the usual word-processor bindings; format copying and
// pasting use Google Docs' Ctrl+Alt+C and Ctrl+Alt+V.
func DefaultKeymap() Keymap {
	return Keymap{
		"Mod+A":       CmdSelectAll,
		"Mod+B":       CmdBold,
		"Mod+I":       CmdItalic,
		"Mod+U":       CmdUnderline,
		"Mod+Shift+X": CmdStrike,
		"Mod+.":       CmdSuperscript,
		"Mod+,":       CmdSubscript,
		"Mod+Alt+C":   CmdCopyFormat,
		"Mod+Alt+V":   CmdPasteFormat,
	}
}

// Chord builds the chord string for a key press. key is the DOM key
// (KeyboardEvent.key, or the letter from Code for layouts where Alt
// changes the character).
func Chord(key string, ctrl, shift, alt, meta bool) string {
	var parts []string
	if ctrl || meta {
		parts = append(parts, "Mod")
	}
	if alt {
		parts = append(parts, "Alt")
	}
	if shift {
		parts = append(parts, "Shift")
	}
	if key == " " {
		key = "Space"
	} else if r := []rune(key); len(r) == 1 {
		key = string(unicode.ToUpper(r[0]))
	}
	return strings.Join(append(parts, key), "+")
}

// Commander is implemented by components that run Commands. It reports
// whether the command applied (a component with no selection, say,
// declines, so another may take it).
type Commander interface{ HandleCommand(Command) bool }

// Copier, Cutter and Paster are the clipboard hooks: the browser's copy,
// cut and paste events are routed to them (topmost component first).
type (
	// Copier returns the text to put on the clipboard, if it has a
	// selection.
	Copier interface{ CopyText() (string, bool) }
	// Cutter returns the selected text and removes it.
	Cutter interface{ CutText() (string, bool) }
	// Paster inserts clipboard text; components without it get plain
	// InsertText.
	Paster interface{ PasteText(text string) bool }
)

// FormatClipboard holds a copied text format (the "format painter"). A
// Stage has one and shares it with its components, so a format copied
// from one component can be pasted into another.
type FormatClipboard struct {
	format Format
	has    bool
}

// Set stores f.
func (c *FormatClipboard) Set(f Format) { c.format, c.has = f, true }

// Get returns the stored format, and whether there is one.
func (c *FormatClipboard) Get() (Format, bool) { return c.format, c.has }

// Clear empties the clipboard.
func (c *FormatClipboard) Clear() { c.format, c.has = Format{}, false }

// FormatClipboardSetter receives the Stage's FormatClipboard.
type FormatClipboardSetter interface{ SetFormatClipboard(*FormatClipboard) }

// CommonFormat returns the formatting every one of fs shares: a field
// is kept where all agree and cleared where they differ, and a
// decoration is on only if it is on everywhere -- the "minimum common
// formatting" of a selection spanning mixed text.
func CommonFormat(fs ...Format) Format {
	if len(fs) == 0 {
		return Format{}
	}
	c := fs[0]
	// Comments, bookmarks and index entries mark places in the text; they
	// are not formatting, and copying one onto other text would duplicate it.
	c.Comment, c.Bookmark, c.Index = "", "", ""
	for _, f := range fs[1:] {
		if f.FontFamily != c.FontFamily {
			c.FontFamily = ""
		}
		if f.FontSize != c.FontSize {
			c.FontSize = 0
		}
		if f.Scale != c.Scale {
			c.Scale = 0
		}
		if f.FontWeight != c.FontWeight {
			c.FontWeight = 0
		}
		if f.FontStyle != c.FontStyle {
			c.FontStyle = ""
		}
		if f.Color != c.Color {
			c.Color = ""
		}
		if f.Background != c.Background {
			c.Background = ""
		}
		if f.Case != c.Case {
			c.Case = CaseNormal
		}
		if f.Link != c.Link {
			c.Link = ""
		}
		c.Underline = c.Underline && f.Underline
		c.Strike = c.Strike && f.Strike
		c.Superscript = c.Superscript && f.Superscript
		c.Subscript = c.Subscript && f.Subscript
	}
	return c
}

// Chords returns the chords bound to cmd, sorted.
func (k Keymap) Chords(cmd Command) []string {
	var out []string
	for chord, c := range k {
		if c == cmd {
			out = append(out, chord)
		}
	}
	sort.Strings(out)
	return out
}

// Owner returns the command chord is bound to ("" if none).
func (k Keymap) Owner(chord string) Command { return k[chord] }

// EffectiveKeymap is what the Stage really uses: the Keymap (or
// DefaultKeymap), plus the chords the registered features bind where it
// has none. A chord the Keymap maps to "" is unbound and is left out.
func (s *Stage) EffectiveKeymap() Keymap {
	km := s.Keymap
	if km == nil {
		km = DefaultKeymap()
	}
	out := Keymap{}
	if s.Features != nil {
		for chord, cmd := range s.Features.keys {
			out[chord] = cmd
		}
	}
	for chord, cmd := range km {
		out[chord] = cmd
	}
	for chord, cmd := range out {
		if cmd == "" {
			delete(out, chord)
		}
	}
	return out
}

// Rebind makes chord the (only) key binding of cmd, taking it away from
// whatever it was bound to, and unbinding cmd's other chords. An empty
// chord just unbinds cmd. The previous owner of chord, if any, is
// returned so the caller can tell the user.
func (s *Stage) Rebind(cmd Command, chord string) (previous Command) {
	eff := s.EffectiveKeymap()
	if s.Keymap == nil {
		s.Keymap = DefaultKeymap()
	}
	for _, old := range eff.Chords(cmd) {
		s.Keymap[old] = "" // an explicit blank wins over a feature's own chord
	}
	if chord == "" {
		return ""
	}
	previous = eff[chord]
	s.Keymap[chord] = cmd
	return previous
}

// ResetKeymap goes back to the default bindings.
func (s *Stage) ResetKeymap() { s.Keymap = nil }
