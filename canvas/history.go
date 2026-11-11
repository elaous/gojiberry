package canvas

import (
	"reflect"
	"slices"
	"sync/atomic"
)

// DefaultHistoryLimit is the default number of undo steps.
const DefaultHistoryLimit = 200

// MinHistoryLimit is the fewest undo steps SetHistoryLimit allows.
const MinHistoryLimit = 5

var historyLimit atomic.Int64

func init() { historyLimit.Store(DefaultHistoryLimit) }

// SetHistoryLimit sets undo steps kept per text; at least MinHistoryLimit.
func SetHistoryLimit(n int) { historyLimit.Store(int64(max(n, MinHistoryLimit))) }

// HistoryLimit is the number of undo steps kept.
func HistoryLimit() int { return int(historyLimit.Load()) }

// Historian is implemented by targets with undo and redo.
type Historian interface {
	Undo() bool
	// Redo re-applies the last undone change.
	Redo() bool
}

var (
	_ Historian = (*TextFlow)(nil)
	_ Historian = (*FloatingText)(nil)
)

type snapshotHistory[S any] struct {
	undo, redo []S
	last       S // the state after the latest change (or the start)
	hasLast    bool
	kind       string // what the latest change was, for merging runs
}

func (h *snapshotHistory[S]) start(s S) {
	if !h.hasLast {
		h.last, h.hasLast = s, true
	}
}

func (h *snapshotHistory[S]) commit(cur S, kind string) {
	if !h.hasLast {
		return
	}
	if reflect.DeepEqual(h.last, cur) {
		return
	}
	if kind == "" || kind != h.kind {
		h.undo = append(h.undo, h.last)
		if over := len(h.undo) - HistoryLimit(); over > 0 {
			h.undo = slices.Delete(h.undo, 0, over)
		}
	}
	h.redo = nil
	h.last, h.kind = cur, kind
}

func (h *snapshotHistory[S]) breakRun() { h.kind = "" }

func (h *snapshotHistory[S]) reset() { *h = snapshotHistory[S]{} }

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

type flowSnap struct {
	items    []Item
	cursors  []Cursor
	comments []Comment
}

func (f *TextFlow) histSnap() flowSnap {
	s := flowSnap{items: slices.Clone(f.Items), cursors: f.Cursors()}
	if f.Comments != nil {
		s.comments = f.Comments.List()
	}
	return s
}

func (f *TextFlow) histRestore(s flowSnap) {
	f.Items = slices.Clone(s.items)
	if f.Comments != nil {
		f.Comments.ReplaceAll(s.comments)
	}
	f.SetCursors(s.cursors)
	f.derivedOK = false
	f.notify()
}

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

func (ft *FloatingText) Undo() bool {
	s, ok := ft.hist.back(ft.histSnap())
	if ok {
		ft.histRestore(s)
	}
	return ok
}

func (ft *FloatingText) Redo() bool {
	s, ok := ft.hist.forward(ft.histSnap())
	if ok {
		ft.histRestore(s)
	}
	return ok
}

// History is the undo and redo feature: Ctrl/Cmd+Z and Ctrl/Cmd+Shift+Z.
type History struct{}

const (
	CmdUndo Command = "edit.undo"
	CmdRedo Command = "edit.redo"
)

func (History) Name() string { return "history" }

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

func endsRun(key string) bool {
	switch key {
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "Enter", "Tab", "Escape":
		return true
	}
	return false
}
