package canvas

import (
	"fmt"
	"slices"
)

type Feature interface {
	// Name identifies the feature ("multi-cursor").
	Name() string
	// Install registers the feature's commands and chords.
	Install(r *Registry)
}

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

type Edit struct {
	From, To int
	Items    []Item
	// AfterOps inserts after breaks at From instead of before them.
	AfterOps bool
}

type EditTarget interface {
	EditText() string
	// Slice returns the runes [from, to) with their formatting.
	Slice(from, to int) []Item
	// Cursors returns every cursor.
	Cursors() []Cursor
	SetCursors([]Cursor)
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
	Redraw    func()
	Prompt    func(message, initial string) (text string, ok bool)
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

// Target is nil when nothing is being edited
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

func textRunes(t EditTarget) []rune { return []rune(t.EditText()) }

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
			// two empty cursors at one offset stay two across a break
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

func lineStart(rs []rune, o int) int {
	for o > 0 && rs[o-1] != '\n' {
		o--
	}
	return o
}

func lineEnd(rs []rune, o int) int {
	for o < len(rs) && rs[o] != '\n' {
		o++
	}
	return o
}

func plainItems(s string) []Item {
	if s == "" {
		return nil
	}
	return []Item{Chars{Text: s}}
}

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

func editCursors(t EditTarget, gen func(c Cursor, rs []rune) (from, to int, ins []Item)) {
	// in suggestion mode an edit proposes the change instead of making it
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
