package canvas

import (
	"fmt"
	"slices"
)

// A Feature is a self-contained editing capability -- multiple cursors,
// line operations, Markdown copy and paste -- packaged as a component of
// its own. It registers Commands (and default key chords for them) with
// a Registry; the Stage's Registry decides which features exist, so a
// feature is switched on by registering it and off by leaving it out:
//
//	st := canvas.NewStage(p, w, h)          // DefaultFeatures
//	st.Features = canvas.NewRegistry(canvas.LineOps{}, myFeature{})
//
// Features act on an EditTarget, so they work with any editable
// component (FloatingText implements it) and are testable without a
// canvas.
type Feature interface {
	// Name identifies the feature ("multi-cursor").
	Name() string
	// Install registers the feature's commands and chords.
	Install(r *Registry)
}

// Cursor is one caret with an optional selection: Anchor is where the
// selection started and Caret the end that moves; equal means no
// selection. Offsets are in runes.
type Cursor struct {
	Anchor, Caret int
	// After says the caret sits after any page or column break at its
	// offset (on the line that starts there) rather than before it: in
	// a TextFlow both are the same offset. Only a TextFlow has the
	// distinction; others ignore it.
	After bool
}

// Range returns the selected range [from, to).
func (c Cursor) Range() (from, to int) { return min(c.Anchor, c.Caret), max(c.Anchor, c.Caret) }

// Empty reports whether nothing is selected.
func (c Cursor) Empty() bool { return c.Anchor == c.Caret }

// At returns a Cursor with no selection at offset o.
func At(o int) Cursor { return Cursor{Anchor: o, Caret: o} }

// Edit replaces the runes [From, To) with Items (Chars only; other items
// are ignored). From == To inserts; no Items deletes.
type Edit struct {
	From, To int
	Items    []Item
	// AfterOps puts an insertion after the page and column breaks at
	// offset From instead of in front of them (TextFlow only).
	AfterOps bool
}

// EditTarget is what a Feature edits: text with one or more cursors.
type EditTarget interface {
	// EditText returns the whole text. Anything that is not text (an
	// inline object) is one placeholder rune, so offsets line up.
	EditText() string
	// Slice returns the runes [from, to) with their formatting.
	Slice(from, to int) []Item
	// Cursors returns every cursor.
	Cursors() []Cursor
	// SetCursors replaces the cursors (at least one; the first is the
	// primary one). Offsets are clamped to the text.
	SetCursors([]Cursor)
	// Replace applies non-overlapping edits, given in any order, as one
	// change. Cursors are not moved: call SetCursors afterwards.
	Replace(edits []Edit)
}

// Editable is implemented by components that have an EditTarget while
// they are being edited (nil otherwise).
type Editable interface{ EditTarget() EditTarget }

// Clipboard is the system clipboard as the browser allows a page to use
// it: Write puts text on it, Read fetches the text asynchronously. A
// Stage's Bind fills it in; either may be nil (the command then does
// nothing).
type Clipboard struct {
	Write func(text string)
	Read  func(fn func(text string))
}

// CommandContext is what a feature command gets.
type CommandContext struct {
	Target    EditTarget
	Clipboard *Clipboard
	Formats   *FormatClipboard
	// Redraw asks the Stage to redraw; needed only by commands that
	// finish later (an asynchronous clipboard read).
	Redraw func()
	// Prompt asks the user for a line of text (a link address); ok is
	// false if they cancelled. Nil when the host cannot ask.
	Prompt func(message, initial string) (text string, ok bool)
	// OpenLink opens a URL.
	OpenLink func(url string)
}

// CommandFunc runs a command; it reports whether it applied.
type CommandFunc func(*CommandContext) bool

// CommandInfo describes a registered command, for menus and a key
// bindings editor.
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
	current  string // feature being installed
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
	return []Feature{MultiCursor{}, LineOps{}, MarkdownClipboard{}, Breaks{}, Paragraphs{}, TextCase{}, Links{}, PageFurniture{}}
}

// Register installs f. It panics on a duplicate feature or command
// name, which is a programming error.
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

// AddCommand registers fn under name; chord, if not empty, is its
// default key binding (see Keymap).
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
	return ok && ctx.Target != nil && fn(ctx)
}

// --- helpers shared by features --------------------------------------

// textRunes returns the target's text as runes.
func textRunes(t EditTarget) []rune { return []rune(t.EditText()) }

// normalize sorts cursors by position and merges overlapping or equal
// ones.
func normalize(cs []Cursor) []Cursor {
	out := slices.Clone(cs)
	slices.SortStableFunc(out, func(a, b Cursor) int {
		af, _ := a.Range()
		bf, _ := b.Range()
		return af - bf
	})
	var merged []Cursor
	for _, c := range out {
		if n := len(merged); n > 0 {
			pf, pt := merged[n-1].Range()
			cf, ct := c.Range()
			if cf < pt || (cf == pt && (cf == pf || c.Empty() || merged[n-1].Empty())) {
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

// lineEnd returns the offset of the end of the line containing o (the
// '\n' or the end of the text).
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

// itemsLen counts the text-stream offsets items take: the runes of the
// Chars, one per inline object (breaks and column configs take none).
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

// ObjectHolder is implemented by an EditTarget that can hold more than
// text: inline objects, page and column breaks and column configs (a
// TextFlow). Others keep only the Chars of what is inserted.
type ObjectHolder interface{ HoldsObjects() bool }

func holdsObjects(t EditTarget) bool {
	h, ok := t.(ObjectHolder)
	return ok && h.HoldsObjects()
}

// editCursors applies gen at every cursor of t, in document order: gen
// gets the cursor and the text and returns the range to replace and what
// to put there; afterwards each cursor is a plain one after its
// insertion.
func editCursors(t EditTarget, gen func(c Cursor, rs []rune) (from, to int, ins []Item)) {
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
