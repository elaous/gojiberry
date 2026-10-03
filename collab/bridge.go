package collab

import (
	"errors"
	"slices"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/crdt"
)

// block is the crdt block holding the document's text.
const block = "doc"

// Bridge keeps a TextFlow and a crdt.Doc replica in step.
//
//   - A local edit (the flow's OnChange) is diffed against the replica: the
//     text difference becomes character operations and the format difference
//     becomes marks, one per changed run. They wait in the outbox (Drain).
//   - Remote operations (Apply) are merged into the replica and the flow's
//     content is rebuilt from it, with each cursor moved so that it stays
//     beside the same character.
//   - Comments, footnotes and named styles travel as whole-value records.
//
// Use New to start a document from the flow's content, or Join to receive
// one that exists elsewhere.
type Bridge struct {
	// Doc is the replica. Send Drain's ops to the others, give Apply what
	// they send, and onboard a newcomer with Snapshot.
	Doc  *crdt.Doc
	Flow *canvas.TextFlow

	// Outgoing, if set, is called with the operations each local change
	// produced (see Drain); wire it to client.Conn.Send.
	Outgoing func([]crdt.Op)

	ready    bool
	applying bool
	detached bool
	err      error
	side     sideSync

	// Per-user undo (see undo.go): the inverse of each of your own changes.
	undo, redo []entry
	recordTo   *[]entry // where the change being made is recorded; nil: the undo stack
}

func attach(replica string, f *canvas.TextFlow) (*Bridge, error) {
	if _, err := atomsFromItems(f.ContentItems()); err != nil {
		return nil, err
	}
	// Comments and notes made here get IDs no other replica can mint.
	if f.Comments == nil {
		f.Comments = canvas.NewCommentSet()
	}
	if f.Footnotes == nil {
		f.Footnotes = canvas.NewFootnoteSet()
	}
	prefix := strings.NewReplacer(",", "_", ":", "_").Replace(replica) + "-"
	f.Comments.IDPrefix, f.Footnotes.IDPrefix = prefix, prefix
	b := &Bridge{Doc: crdt.NewDocWithStrategy(replica, crdt.MergeCharCRDT), Flow: f}
	f.Subscribe(func(*canvas.TextFlow) { b.pushLocal() })
	f.UndoHook = b.UndoStep
	return b, nil
}

// New starts a shared document from the flow's current content, as
// replica. It fails if the flow has content the bridge cannot carry (see
// ErrUnsupported).
func New(replica string, f *canvas.TextFlow) (*Bridge, error) {
	b, err := attach(replica, f)
	if err != nil {
		return nil, err
	}
	b.ready = true
	b.pushLocal()
	b.undo, b.redo = nil, nil // the starting content is not an edit to undo
	return b, nil
}

// Join connects a flow to a document that exists elsewhere: the flow's own
// content is replaced by the first operations received (typically the
// other side's Snapshot), and local edits are not shared before then.
func Join(replica string, f *canvas.TextFlow) (*Bridge, error) {
	return attach(replica, f)
}

// Detach stops the bridge sharing: later edits to the flow stay local and
// undo goes back to the flow's own history. A replacement bridge attached to
// the same flow takes over (see Session.Repair).
func (b *Bridge) Detach() {
	b.detached = true
	b.Outgoing = nil
	b.Flow.UndoHook = nil
}

// seedReplica is the replica id the starting content of every shared document
// is written under.
const seedReplica = "seed"

// Seed makes a joined bridge share the flow's own content as the start of
// the document (when the server turned out to have none).
//
// The content is written as the replica "seed", not as this replica: two
// clients that open an empty document at the same moment and start from the
// same content then produce the same operations, which merge into one copy
// instead of two. (Different content from the two sides is still both kept.)
func (b *Bridge) Seed() {
	replica, outgoing := b.Doc, b.Outgoing
	b.Doc, b.Outgoing = crdt.NewDocWithStrategy(seedReplica, crdt.MergeCharCRDT), nil
	b.ready = true
	b.pushLocal()
	ops := b.Doc.Drain()
	b.Doc, b.Outgoing = replica, outgoing
	b.applying = true
	b.Doc.Apply(ops...)
	b.applying = false
	b.undo, b.redo = nil, nil
	if b.Outgoing != nil && len(ops) > 0 {
		b.Outgoing(ops)
	}
}

// Ready reports whether the bridge is sharing yet: always after New, after
// the first Apply after Join.
func (b *Bridge) Ready() bool { return b.ready }

// Err returns the last error that stopped a local change being shared (flow
// content the bridge cannot carry), or nil.
func (b *Bridge) Err() error { return b.err }

// Drain returns the operations produced locally since the last call.
func (b *Bridge) Drain() []crdt.Op { return b.Doc.Drain() }

// Snapshot returns the whole document, to onboard a replica.
func (b *Bridge) Snapshot() []crdt.Op { return b.Doc.Snapshot() }

// Sync shares side data (comments, notes, styles) that changed without a
// text edit. Commands that change them call TextFlow.Touch, which gets
// here through OnChange; call Sync after changing them yourself.
func (b *Bridge) Sync() { b.pushLocal() }

// read returns the replica's document as atoms. A non-text atom (paragraph
// format, break, object) keeps only its payload: character formatting
// marks that happen to span it mean nothing for it.
func (b *Bridge) read() []atom {
	var out []atom
	for _, run := range b.Doc.Runs(block) {
		var ctlOnly map[string]string
		if payload, ok := run.Attrs[ctlKey]; ok {
			ctlOnly = map[string]string{ctlKey: payload}
		}
		for _, r := range run.Text {
			if ctlOnly != nil && (isReserved(r) || r == objRune) {
				out = append(out, atom{r: r, attrs: ctlOnly})
				continue
			}
			out = append(out, atom{r: r, attrs: run.Attrs})
		}
	}
	return out
}

// edit is one change to the text, in the old text's indexes.
type edit struct {
	at, del int
	ins     []rune
}

// maxLCS bounds the work of finding the differences between the changed
// middles of two texts; beyond it the middle is replaced whole.
const maxLCS = 4_000_000

// diffRunes returns the edits turning old into new (ascending, in old's
// indexes): common ends are trimmed, the middle is matched by longest
// common subsequence.
func diffRunes(old, cur []rune) []edit {
	pre := 0
	for pre < len(old) && pre < len(cur) && old[pre] == cur[pre] {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(cur)-pre && old[len(old)-1-suf] == cur[len(cur)-1-suf] {
		suf++
	}
	om, nm := old[pre:len(old)-suf], cur[pre:len(cur)-suf]
	if len(om) == 0 && len(nm) == 0 {
		return nil
	}
	if len(om) == 0 || len(nm) == 0 || len(om)*len(nm) > maxLCS {
		return []edit{{at: pre, del: len(om), ins: slices.Clone(nm)}}
	}
	n, m := len(om), len(nm)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case om[i] == nm[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []edit
	var cur2 *edit
	emit := func() {
		if cur2 != nil {
			out = append(out, *cur2)
			cur2 = nil
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && om[i] == nm[j]:
			emit()
			i++
			j++
		case i < n && (j == m || dp[i+1][j] >= dp[i][j+1]):
			if cur2 == nil {
				cur2 = &edit{at: pre + i}
			}
			cur2.del++
			i++
		default:
			if cur2 == nil {
				cur2 = &edit{at: pre + i}
			}
			cur2.ins = append(cur2.ins, nm[j])
			j++
		}
	}
	emit()
	return out
}

// slideEdit moves a single insertion or deletion within a run of equal
// characters to where the caret says it happened. Deleting one of two
// equal letters in "ll" looks the same whichever is meant, and the diff
// alone would always pick the same one; the caret, left at the place of the
// change, knows which was.
func slideEdit(old, cur []rune, e edit, caret int) edit {
	switch {
	case e.del > 0 && len(e.ins) == 0:
		lo, hi := e.at, e.at
		for lo > 0 && old[lo-1] == old[lo-1+e.del] {
			lo--
		}
		for hi+e.del < len(old) && old[hi] == old[hi+e.del] {
			hi++
		}
		if caret >= lo && caret <= hi {
			e.at = caret
		}
	case e.del == 0 && len(e.ins) > 0:
		m := len(e.ins)
		lo, hi := e.at, e.at
		for lo > 0 && cur[lo-1] == cur[lo-1+m] {
			lo--
		}
		for hi+m < len(cur) && cur[hi] == cur[hi+m] {
			hi++
		}
		if start := caret - m; start >= lo && start <= hi {
			e.at, e.ins = start, slices.Clone(cur[start:start+m])
		}
	}
	return e
}

// pushLocal shares what changed in the flow since the replica last agreed
// with it.
func (b *Bridge) pushLocal() {
	if b.applying || !b.ready || b.detached {
		return
	}
	cur, err := atomsFromItems(b.Flow.ContentItems())
	if err != nil {
		b.err = err
		return
	}
	b.err = nil
	old := b.read()
	runesOf := func(as []atom) []rune {
		rs := make([]rune, len(as))
		for i, a := range as {
			rs[i] = a.r
		}
		return rs
	}
	oldIDs := b.Doc.VisibleIDs(block)
	var rec entry
	oldRunes, curRunes := runesOf(old), runesOf(cur)
	edits := diffRunes(oldRunes, curRunes)
	if len(edits) == 1 {
		edits[0] = slideEdit(oldRunes, curRunes, edits[0], atomPos(cur, b.Flow.Cursors()[0].Caret))
	}
	for i := len(edits) - 1; i >= 0; i-- { // last first: earlier indexes stay valid
		ed := edits[i]
		if ed.del > 0 {
			run := deletedRun{atoms: slices.Clone(old[ed.at : ed.at+ed.del]), ids: slices.Clone(oldIDs[ed.at : ed.at+ed.del])}
			if ed.at > 0 {
				run.after = oldIDs[ed.at-1]
			}
			rec.deleted = append(rec.deleted, run)
		}
		if op, ok := b.Doc.Splice(block, ed.at, ed.del, ed.ins); ok {
			for _, c := range op.CharOps {
				if c.Kind == crdt.CharInsert {
					if len(rec.inserted) == 0 {
						rec.origin = c.Origin
					}
					rec.inserted = append(rec.inserted, c.ID)
				}
			}
		}
	}
	b.pushFormats(b.read(), cur, &rec)
	b.pushSide()
	b.record(rec)
	if b.Outgoing != nil {
		if ops := b.Doc.Drain(); len(ops) > 0 {
			b.Outgoing(ops)
		}
	}
}

// pushFormats turns the differences between the replica's attributes (have)
// and the flow's (want) into marks, one per changed run and key.
func (b *Bridge) pushFormats(have, want []atom, rec *entry) {
	if len(have) != len(want) {
		b.err = errors.New("collab: internal error: the replica and the flow disagree about the text")
		return
	}
	ids := b.Doc.VisibleIDs(block)
	inserted := map[crdt.Timestamp]bool{}
	for _, id := range rec.inserted {
		inserted[id] = true
	}
	keys := map[string]bool{}
	for _, a := range have {
		for k := range a.attrs {
			keys[k] = true
		}
	}
	for _, a := range want {
		for k := range a.attrs {
			keys[k] = true
		}
	}
	for key := range keys {
		for i := 0; i < len(want); {
			w, h := want[i].attrs[key], have[i].attrs[key]
			if w == h {
				i++
				continue
			}
			j := i + 1
			for j < len(want) && want[j].attrs[key] == w && have[j].attrs[key] != w {
				j++
			}
			b.Doc.Format(block, i, j, key, w, growsAtEnd(key))
			fc := formatChange{key: key}
			for k := i; k < j && k < len(ids); k++ {
				if !inserted[ids[k]] { // text I just typed is undone by deleting it
					fc.ids = append(fc.ids, ids[k])
					fc.old = append(fc.old, have[k].attrs[key])
				}
			}
			if len(fc.ids) > 0 {
				rec.formats = append(rec.formats, fc)
			}
			i = j
		}
	}
}

// cursorAnchor remembers where a caret was as the id of the character just
// before it (none for the very start), plus the flow's After flag.
type cursorAnchor struct {
	id    crdt.Timestamp
	start bool
}

func (b *Bridge) anchorOf(atoms []atom, ids []crdt.Timestamp, offset int) cursorAnchor {
	k := atomPos(atoms, offset)
	if k == 0 || k > len(ids) {
		return cursorAnchor{start: true}
	}
	return cursorAnchor{id: ids[k-1]}
}

func (b *Bridge) offsetOf(a cursorAnchor, atoms []atom) int {
	if a.start {
		return 0
	}
	pos, ok := b.Doc.PositionAfter(block, a.id)
	if !ok {
		return streamCount(atoms)
	}
	return streamCount(atoms[:min(pos, len(atoms))])
}

// Apply merges operations received from other replicas and brings the flow
// up to date. Cursors stay beside the characters they were beside; the undo
// history is cleared (see canvas.TextFlow.SetRemoteContent).
func (b *Bridge) Apply(ops ...crdt.Op) {
	oldAtoms := b.read()
	oldIDs := b.Doc.VisibleIDs(block)
	cursors := b.Flow.Cursors()
	type anchored struct{ anchor, caret cursorAnchor }
	anchors := make([]anchored, len(cursors))
	for i, c := range cursors {
		anchors[i] = anchored{b.anchorOf(oldAtoms, oldIDs, c.Anchor), b.anchorOf(oldAtoms, oldIDs, c.Caret)}
	}

	b.Doc.Apply(ops...)
	b.ready = true
	atoms := b.read()
	items := itemsFromAtoms(atoms)
	moved := make([]canvas.Cursor, len(cursors))
	for i, c := range cursors {
		moved[i] = canvas.Cursor{Anchor: b.offsetOf(anchors[i].anchor, atoms), Caret: b.offsetOf(anchors[i].caret, atoms), After: c.After}
	}

	b.applying = true
	defer func() { b.applying = false }()
	b.Flow.SetRemoteContent(items, moved)
	b.pullSide()
}
