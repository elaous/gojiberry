package canvas

import (
	"reflect"
	"slices"
)

// Undo and redo for editable text. Every change a TextFlow or a
// FloatingText makes -- typing, deleting, pasting, formatting, line
// operations, list and paragraph commands -- can be undone with
// Ctrl/Cmd+Z and redone with Ctrl/Cmd+Shift+Z (or Ctrl+Y). A run of typed
// characters, or of Backspace/Delete presses, is one step; moving the
// caret or clicking ends the run. Each undo step restores the text, its
// formatting, and the cursors.

// maxHistory is how many undo steps are kept.
const maxHistory = 200

// Historian is implemented by targets with undo and redo.
type Historian interface {
	// Undo reverts the last change; it reports whether there was one.
	Undo() bool
	// Redo re-applies the last undone change.
	Redo() bool
}

var (
	_ Historian = (*TextFlow)(nil)
	_ Historian = (*FloatingText)(nil)
)

// snapshotHistory keeps whole-state snapshots, which is simple and cheap
// enough for documents of this size. S must be comparable with
// reflect.DeepEqual.
type snapshotHistory[S any] struct {
	undo, redo []S
	last       S // the state after the latest change (or the start)
	hasLast    bool
	kind       string // what the latest change was, for merging runs
}

// start records s as the state to go back to, if nothing is yet.
func (h *snapshotHistory[S]) start(s S) {
	if !h.hasLast {
		h.last, h.hasLast = s, true
	}
}

// commit records that the state is now cur after a change of the given
// kind: "" (never merged) or a run kind such as "type". A change of the
// same kind as the one before extends its step instead of starting one.
func (h *snapshotHistory[S]) commit(cur S, kind string) {
	if !h.hasLast {
		return
	}
	if reflect.DeepEqual(h.last, cur) {
		return
	}
	if kind == "" || kind != h.kind {
		h.undo = append(h.undo, h.last)
		if len(h.undo) > maxHistory {
			h.undo = slices.Delete(h.undo, 0, 1)
		}
	}
	h.redo = nil
	h.last, h.kind = cur, kind
}

// breakRun ends a run of merged changes.
func (h *snapshotHistory[S]) breakRun() { h.kind = "" }

// reset forgets everything.
func (h *snapshotHistory[S]) reset() { *h = snapshotHistory[S]{} }

// back pops an undo step; cur is the state being left.
func (h *snapshotHistory[S]) back(cur S) (S, bool) {
	var zero S
	if len(h.undo) == 0 {
		return zero, false
	}
	h.redo = append(h.redo, cur)
	prev := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	h.last, h.kind = prev, ""
	return prev, true
}

// forward pops a redo step.
func (h *snapshotHistory[S]) forward(cur S) (S, bool) {
	var zero S
	if len(h.redo) == 0 {
		return zero, false
	}
	h.undo = append(h.undo, cur)
	next := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	h.last, h.kind = next, ""
	return next, true
}

// --- TextFlow ---------------------------------------------------------

type flowSnap struct {
	items   []Item
	cursors []Cursor
}

func (f *TextFlow) histSnap() flowSnap {
	return flowSnap{items: slices.Clone(f.Items), cursors: f.Cursors()}
}

func (f *TextFlow) histRestore(s flowSnap) {
	f.Items = slices.Clone(s.items)
	f.SetCursors(s.cursors)
	f.derivedOK = false
	if f.OnChange != nil {
		f.OnChange(f)
	}
}

// Undo implements Historian.
func (f *TextFlow) Undo() bool {
	if f.UndoHook != nil {
		return f.UndoHook(false)
	}
	s, ok := f.hist.back(f.histSnap())
	if ok {
		f.histRestore(s)
	}
	return ok
}

// Redo implements Historian.
func (f *TextFlow) Redo() bool {
	if f.UndoHook != nil {
		return f.UndoHook(true)
	}
	s, ok := f.hist.forward(f.histSnap())
	if ok {
		f.histRestore(s)
	}
	return ok
}

// --- FloatingText -----------------------------------------------------

type boxSnap struct {
	runes   []fRune
	cursors []Cursor
}

func (ft *FloatingText) histSnap() boxSnap {
	return boxSnap{runes: slices.Clone(ft.runes), cursors: ft.Cursors()}
}

func (ft *FloatingText) histRestore(s boxSnap) {
	ft.runes = slices.Clone(s.runes)
	ft.SetCursors(s.cursors)
	if ft.OnChange != nil {
		ft.OnChange(ft)
	}
}

// Undo implements Historian.
func (ft *FloatingText) Undo() bool {
	s, ok := ft.hist.back(ft.histSnap())
	if ok {
		ft.histRestore(s)
	}
	return ok
}

// Redo implements Historian.
func (ft *FloatingText) Redo() bool {
	s, ok := ft.hist.forward(ft.histSnap())
	if ok {
		ft.histRestore(s)
	}
	return ok
}

// --- the feature --------------------------------------------------------

// History is the undo and redo commands: Ctrl/Cmd+Z, and Ctrl/Cmd+Shift+Z
// or Ctrl+Y.
type History struct{}

// Command names.
const (
	CmdUndo Command = "edit.undo"
	CmdRedo Command = "edit.redo"
)

// Name implements Feature.
func (History) Name() string { return "history" }

// Install implements Feature.
func (History) Install(r *Registry) {
	r.AddCommand(CmdUndo, "Undo", "Mod+Z", func(c *CommandContext) bool {
		h, ok := c.Target.(Historian)
		return ok && h.Undo()
	})
	r.AddCommand(CmdRedo, "Redo", "Mod+Shift+Z", func(c *CommandContext) bool {
		h, ok := c.Target.(Historian)
		return ok && h.Redo()
	})
	r.Bind("Mod+Y", CmdRedo)
}

// endsRun reports whether pressing key ends a run of typing or deleting
// (the keys that move the caret or break the line; a letter key does not,
// it is about to be typed).
func endsRun(key string) bool {
	switch key {
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "Enter", "Tab", "Escape":
		return true
	}
	return false
}
