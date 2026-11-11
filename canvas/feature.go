package canvas

import (
	"fmt"
	"slices"
)

// A Feature is a self-contained editing capability with its own commands and chords.
type Feature interface {
	// Name identifies the feature ("multi-cursor").
	Name() string
	// Install registers the feature's commands and chords.
	Install(r *Registry)
}

// Cursor is one caret; Anchor is the selection start and Caret the moving end.
type Cursor struct {
	Anchor, Caret int
	// After places the caret after a break at its offset.
	After bool
}

// Range returns the selected range [from, to).
func (c Cursor) Range() (from, to int) { return min(c.Anchor, c.Caret), max(c.Anchor, c.Caret) }

// Empty reports whether nothing is selected.
func (c Cursor) Empty() bool { return c.Anchor == c.Caret }

// At returns a Cursor with no selection at offset o.
func At(o int) Cursor { return Cursor{Anchor: o, Caret: o} }

// Edit replaces runes [From, To) with Items; From == To inserts.
type Edit struct {
	From, To int
	Items    []Item
	// AfterOps inserts after breaks at From instead of before them.
	AfterOps bool
}

// EditTarget is what a Feature edits: text with one or more cursors.
type EditTarget interface {
	// EditText returns the text; each inline object is one placeholder rune.
	EditText() string
	// Slice returns the runes [from, to) with their formatting.
	Slice(from, to int) []Item
	// Cursors returns every cursor.
	Cursors() []Cursor
	// SetCursors replaces the cursors; the first is primary; offsets are clamped.
	SetCursors([]Cursor)
	// Replace applies non-overlapping edits as one change; cursors do not move.
	Replace(edits []Edit)
}

// Editable is implemented by components that expose an EditTarget.
type Editable interface{ EditTarget() EditTarget }

// Clipboard is the system clipboard: Write, and Read asynchronously.
type Clipboard struct {
	Write func(text string)
	Read  func(fn func(text string))
}

// CommandContext is what a feature command gets.
type CommandContext struct {
	Target    EditTarget
	Clipboard *Clipboard
	Formats   *FormatClipboard
	// Redraw asks the Stage to redraw, for commands that finish later.
	Redraw func()
	// Prompt asks for a line of text; nil if the host cannot.
	Prompt func(message, initial string) (text string, ok bool)
	// EditNote asks for a longer text, with the limit shown.
	EditNote func(title, initial string, max int, done func(text string, ok bool))
	// OpenLink opens a URL.
	OpenLink func(url string)
}

// CommandFunc runs a command; it reports whether it applied.
type CommandFunc func(*CommandContext) bool

// CommandInfo describes a registered command for menus and key editors.
type CommandInfo struct {
	Name    Command
	Title   string
	Feature string
}

// Registry holds the features and the commands they registered.
type Registry struct {
	features []Feature
	infos    []CommandInfo
	funcs    map[Command]CommandFunc
	keys     Keymap
	current  string           // feature being installed
	global   map[Command]bool // commands that run without an edit target (AddGlobalCommand)
}

// NewRegistry returns a Registry with the features installed.
func NewRegistry(features ...Feature) *Registry {
	r := &Registry{funcs: map[Command]CommandFunc{}, keys: Keymap{}}
	for _, f := range features {
		r.Register(f)
	}
	return r
}

// DefaultFeatures are the features a new Stage has.
func DefaultFeatures() []Feature {
	return []Feature{History{}, MultiCursor{}, LineOps{}, MarkdownClipboard{}, Breaks{}, Paragraphs{}, TextCase{}, Links{}, Styles{}, Comments{}, Bookmarks{}, Notes{}, PageFurniture{}}
}

// Register installs f; a duplicate feature or command name panics.
func (r *Registry) Register(f Feature) {
	for _, g := range r.features {
		if g.Name() == f.Name() {
			panic(fmt.Sprintf("canvas: feature %q registered twice", f.Name()))
		}
	}
	r.features = append(r.features, f)
	r.current = f.Name()
	f.Install(r)
	r.current = ""
}

// AddCommand registers fn under name, with chord as its default binding.
func (r *Registry) AddCommand(name Command, title, chord string, fn CommandFunc) {
	if _, dup := r.funcs[name]; dup {
		panic(fmt.Sprintf("canvas: command %q registered twice", name))
	}
	r.funcs[name] = fn
	r.infos = append(r.infos, CommandInfo{Name: name, Title: title, Feature: r.current})
	if chord != "" {
		r.keys[chord] = name
	}
}

// AddGlobalCommand registers a command that needs no text to act on.
func (r *Registry) AddGlobalCommand(name Command, title, chord string, fn CommandFunc) {
	r.AddCommand(name, title, chord, fn)
	if r.global == nil {
		r.global = map[Command]bool{}
	}
	r.global[name] = true
}

// Bind adds a further default chord for a registered command.
func (r *Registry) Bind(chord string, cmd Command) {
	if !r.Has(cmd) {
		panic(fmt.Sprintf("canvas: Bind of unknown command %q", cmd))
	}
	r.keys[chord] = cmd
}

// Features returns the registered features.
func (r *Registry) Features() []Feature { return slices.Clone(r.features) }

// Commands describes every registered command, in registration order.
func (r *Registry) Commands() []CommandInfo { return slices.Clone(r.infos) }

// Keymap returns the features' default chords.
func (r *Registry) Keymap() Keymap {
	km := Keymap{}
	for k, v := range r.keys {
		km[k] = v
	}
	return km
}

// Has reports whether cmd is a registered command.
func (r *Registry) Has(cmd Command) bool { _, ok := r.funcs[cmd]; return ok }

// Run runs cmd, if registered, on ctx.
func (r *Registry) Run(cmd Command, ctx *CommandContext) bool {
	fn, ok := r.funcs[cmd]
	return ok && (ctx.Target != nil || r.global[cmd]) && fn(ctx)
}

// textRunes returns the target's text as runes.
func textRunes(t EditTarget) []rune { return []rune(t.EditText()) }

// normalize sorts cursors and merges overlapping ones.
func normalize(cs []Cursor) []Cursor {
	out := slices.Clone(cs)
	slices.SortStableFunc(out, func(a, b Cursor) int {
		af, _ := a.Range()
		bf, _ := b.Range()
		if af == bf && a.Empty() && b.Empty() && a.After != b.After {
			if !a.After {
				return -1
			}
			return 1
		}
		return af - bf
	})
	var merged []Cursor
	for _, c := range out {
		if n := len(merged); n > 0 {
			pf, pt := merged[n-1].Range()
			cf, ct := c.Range()
			// Two empty cursors at one offset stay apart across a break.
			sides := cf == pt && cf == pf && c.Empty() && merged[n-1].Empty() && c.After != merged[n-1].After
			if !sides && (cf < pt || (cf == pt && (cf == pf || c.Empty() || merged[n-1].Empty()))) {
				merged[n-1] = Cursor{Anchor: pf, Caret: max(pt, ct)}
				continue
			}
		}
		merged = append(merged, c)
	}
	return merged
}

// lineStart returns the offset of the start of the line containing o.
func lineStart(rs []rune, o int) int {
	for o > 0 && rs[o-1] != '\n' {
		o--
	}
	return o
}

// lineEnd returns the offset of the line end (the newline or text end).
func lineEnd(rs []rune, o int) int {
	for o < len(rs) && rs[o] != '\n' {
		o++
	}
	return o
}

// plainItems wraps s as an Items slice with no format.
func plainItems(s string) []Item {
	if s == "" {
		return nil
	}
	return []Item{Chars{Text: s}}
}

// itemsLen counts the text-stream offsets items take.
func itemsLen(items []Item) int {
	n := 0
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			n += len([]rune(v.Text))
		case Inline:
			n++
		}
	}
	return n
}

// ObjectHolder is an EditTarget that holds inline objects and breaks.
type ObjectHolder interface{ HoldsObjects() bool }

func holdsObjects(t EditTarget) bool {
	h, ok := t.(ObjectHolder)
	return ok && h.HoldsObjects()
}

// editCursors applies gen at every cursor, in document order.
func editCursors(t EditTarget, gen func(c Cursor, rs []rune) (from, to int, ins []Item)) {
	// In suggestion mode an edit proposes the change instead.
	if f, ok := t.(*TextFlow); ok && f.proposalsOn() {
		f.proposalEdits(gen)
		return
	}
	rs := textRunes(t)
	var edits []Edit
	var out []Cursor
	shift := 0
	for _, c := range normalize(t.Cursors()) {
		from, to, ins := gen(c, rs)
		edits = append(edits, Edit{From: from, To: to, Items: ins, AfterOps: c.After && from == to})
		n := itemsLen(ins)
		out = append(out, At(from+shift+n))
		shift += n - (to - from)
	}
	t.Replace(edits)
	t.SetCursors(out)
}
