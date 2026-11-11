package canvas

import (
	"sort"
	"strings"
	"unicode"
)

// Command names an action a key chord can request.
type Command string

// Built-in commands; copy, cut and paste use browser clipboard events.
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

// Keymap binds chords such as Mod+Alt+Shift+Key to commands.
type Keymap map[string]Command

// DefaultKeymap is the usual word-processor bindings.
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

// Chord builds the chord string for a key press.
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

// Commander runs Commands; it reports whether the command applied.
type Commander interface{ HandleCommand(Command) bool }

// Copier, Cutter and Paster are the clipboard hooks.
type (
	// Copier returns the selected text for the clipboard.
	Copier interface{ CopyText() (string, bool) }
	// Cutter returns the selected text and removes it.
	Cutter interface{ CutText() (string, bool) }
	// Paster inserts clipboard text; falls back to InsertText.
	Paster interface{ PasteText(text string) bool }
)

// FormatClipboard holds a copied text format, shared by a Stage.
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

// CommonFormat returns the formatting shared by all of fs.
func CommonFormat(fs ...Format) Format {
	if len(fs) == 0 {
		return Format{}
	}
	c := fs[0]
	// Comments, bookmarks and index entries are not formatting and are not copied.
	c.Comment, c.Bookmark, c.Index, c.Proposal = "", "", "", ""
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

// EffectiveKeymap returns the Keymap or default, plus feature chords.
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

// Rebind makes chord the only binding of cmd, taking it from others.
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
