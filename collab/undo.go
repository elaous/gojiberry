package collab

import (
	"slices"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/crdt"
)

// Per-user undo. The flow's own undo restores snapshots, which would also
// rewind other people's edits. Instead the bridge records the inverse of each
// of your own changes by character id -- the characters you inserted, the
// runs you deleted (with the character they followed), the attributes you
// changed (with their old values) -- and undoing applies the inverse to the
// document as it is now: characters other people typed stay, a deleted run
// comes back where it was even if its neighbours moved.

// maxUndo is how many steps are kept.
const maxUndo = 200

type deletedRun struct {
	after crdt.Timestamp // the visible character before the run, zero for the start
	ids   []crdt.Timestamp
	atoms []atom
}

type formatChange struct {
	ids []crdt.Timestamp
	key string
	old []string // the value before, per id ("" for none)
}

// entry is the inverse of one local change.
type entry struct {
	inserted []crdt.Timestamp
	origin   crdt.Timestamp // what the first inserted character followed
	deleted  []deletedRun
	formats  []formatChange
}

func (e entry) empty() bool {
	return len(e.inserted) == 0 && len(e.deleted) == 0 && len(e.formats) == 0
}

// pureInsert and pureDelete say an entry is one run of typing or of
// deleting, which a following one may extend.
func (e entry) pureInsert() bool {
	return len(e.inserted) > 0 && len(e.deleted) == 0 && len(e.formats) == 0
}

func (e entry) pureDelete() bool {
	return len(e.deleted) == 1 && len(e.inserted) == 0 && len(e.formats) == 0
}

// record stores the inverse of a change just made: on the undo stack for
// an edit of yours (clearing redo), or on the stack an undo or redo in
// progress says. Typing and deleting runs are merged into one step.
func (b *Bridge) record(e entry) {
	if e.empty() {
		return
	}
	if b.recordTo != nil {
		*b.recordTo = append(*b.recordTo, e)
		return
	}
	b.redo = nil
	if n := len(b.undo); n > 0 {
		prev := &b.undo[n-1]
		switch {
		case prev.pureInsert() && e.pureInsert() && e.origin == prev.inserted[len(prev.inserted)-1]:
			prev.inserted = append(prev.inserted, e.inserted...) // typing carries on
			return
		case prev.pureDelete() && e.pureDelete():
			p, q := &prev.deleted[0], e.deleted[0]
			switch {
			case q.ids[len(q.ids)-1] == p.after: // backspacing: the new run is just before
				p.ids = append(slices.Clone(q.ids), p.ids...)
				p.atoms = append(slices.Clone(q.atoms), p.atoms...)
				p.after = q.after
				return
			case q.after == p.after: // the Delete key: the new run is just after
				p.ids = append(p.ids, q.ids...)
				p.atoms = append(p.atoms, q.atoms...)
				return
			}
		}
	}
	b.undo = append(b.undo, e)
	if len(b.undo) > maxUndo {
		b.undo = b.undo[len(b.undo)-maxUndo:]
	}
}

// CanUndo and CanRedo say whether there is a step of yours to undo or redo.
func (b *Bridge) CanUndo() bool { return len(b.undo) > 0 }

// CanRedo reports whether an undone step can be redone.
func (b *Bridge) CanRedo() bool { return len(b.redo) > 0 }

// Undo undoes your last change, leaving other people's alone; it reports
// whether it did anything. The flow's undo commands (Ctrl+Z) come here.
func (b *Bridge) Undo() bool { return b.UndoStep(false) }

// Redo redoes the change you undid last.
func (b *Bridge) Redo() bool { return b.UndoStep(true) }

// UndoStep undoes (or, with redo, redoes) one step, skipping steps whose
// text other people have since removed.
func (b *Bridge) UndoStep(redo bool) bool {
	if !b.ready {
		return false
	}
	from, to := &b.undo, &b.redo
	if redo {
		from, to = to, from
	}
	for len(*from) > 0 {
		e := (*from)[len(*from)-1]
		*from = (*from)[:len(*from)-1]
		b.recordTo = to
		changed := b.revert(e)
		b.recordTo = nil
		if changed {
			return true
		}
	}
	return false
}

// revert applies the inverse e to the document as it stands, through the
// flow (so the change is shared like any other and its own inverse recorded
// for redo).
func (b *Bridge) revert(e entry) bool {
	atoms := b.read()
	ids := b.Doc.VisibleIDs(block)
	idx := make(map[crdt.Timestamp]int, len(ids))
	for i, id := range ids {
		idx[id] = i
	}
	cells := slices.Clone(atoms)
	changed := false

	for _, fc := range e.formats { // put the old attribute values back
		for k, id := range fc.ids {
			i, ok := idx[id]
			if !ok || cells[i].attrs[fc.key] == fc.old[k] {
				continue
			}
			na := make(map[string]string, len(cells[i].attrs)+1)
			for key, v := range cells[i].attrs {
				na[key] = v
			}
			if fc.old[k] == "" {
				delete(na, fc.key)
			} else {
				na[fc.key] = fc.old[k]
			}
			cells[i].attrs = na
			changed = true
		}
	}
	removed := map[int]bool{}
	for _, id := range e.inserted { // take out what I typed
		if i, ok := idx[id]; ok {
			removed[i] = true
			changed = true
		}
	}
	type insertion struct {
		at    int // insert in front of cells[at]
		atoms []atom
	}
	var ins []insertion
	for i := len(e.deleted) - 1; i >= 0; i-- { // bring back what I deleted
		run := e.deleted[i]
		at := 0
		if run.after != (crdt.Timestamp{}) {
			pos, ok := b.Doc.PositionAfter(block, run.after)
			if !ok {
				continue
			}
			at = pos
		}
		ins = append(ins, insertion{at, run.atoms})
		changed = true
	}
	if !changed {
		return false
	}

	var out []atom
	caret := -1 // index in out where the caret goes
	emit := func(at int) {
		for _, in := range ins {
			if in.at == at {
				out = append(out, in.atoms...)
				caret = len(out)
			}
		}
	}
	for i := range cells {
		emit(i)
		if removed[i] {
			caret = max(caret, len(out))
			continue
		}
		out = append(out, cells[i])
	}
	emit(len(cells))
	if caret < 0 {
		caret = 0
	}
	o := streamCount(out[:min(caret, len(out))])

	b.Flow.SetRemoteContent(itemsFromAtoms(out), []canvas.Cursor{canvas.At(o)})
	b.pushLocal()
	if b.Flow.OnChange != nil {
		b.Flow.OnChange(b.Flow)
	}
	return true
}
