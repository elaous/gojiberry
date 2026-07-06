package crdt

// MergeStrategy selects how Doc.Apply resolves two replicas writing to
// the same block's Content concurrently. The zero value, MergeLWW, is
// this package's original behavior and NewDoc's default.
type MergeStrategy int

const (
	// MergeLWW keeps the entire Content of whichever write has the
	// later Timestamp and discards the other in full. Simple and
	// cheap, but a real conflict between two concurrent edits to the
	// same block always throws one of them away completely.
	MergeLWW MergeStrategy = iota
	// MergeCharCRDT merges concurrent writes to the same block's
	// Content at per-character granularity using an RGA (see Text):
	// edits to different parts of the same text both survive, instead
	// of one edit winning outright and the other being discarded.
	// Kind, Order, and Deleted are still resolved by MergeLWW's rule
	// -- only Content gets the finer-grained treatment.
	MergeCharCRDT
)

// CharOpKind is the kind of edit a CharOp carries.
type CharOpKind int

const (
	CharInsert CharOpKind = iota
	CharDelete
)

// CharOp is one character-level edit: the unit MergeCharCRDT exchanges
// between replicas instead of replacing a block's Content wholesale.
type CharOp struct {
	Kind CharOpKind
	// ID is this character's own globally unique id -- a Timestamp
	// doubling as a per-character id, using the same Lamport-clock
	// scheme Block writes use for the same reason.
	ID Timestamp
	// Origin is, for a CharInsert, the id of the character immediately
	// before this one at the moment it was inserted; the zero
	// Timestamp means "start of text". Unused for CharDelete.
	Origin Timestamp
	// Value is the inserted character. Unused for CharDelete.
	Value rune
}

// char is one element of a Text's converged sequence.
type char struct {
	id      Timestamp
	origin  Timestamp
	value   rune
	deleted bool
}

// Text is a per-character CRDT sequence -- a Replicated Growable Array
// (RGA, Roh et al. 2011): a convergent, conflict-free ordered list of
// characters supporting concurrent inserts and deletes at arbitrary
// positions. Every character carries its own globally unique id and
// the id of the character immediately before it at the moment it was
// inserted (its "origin"); replaying every insert in an order where
// each origin is already present reconstructs the identical sequence
// on every replica, regardless of what order the inserts/deletes
// themselves were received in.
//
// Like the original RGA algorithm, and unlike newer designs such as
// Fugue, this can interleave short concurrent insertions at the same
// position in an order a person wouldn't have intended in pathological
// cases (e.g. two replicas both typing a run of characters right after
// the same character while offline). Every replica still converges to
// the same result, just not always the most "expected" one -- a known,
// accepted trade-off of RGA, not a bug, and out of scope to fix here.
type Text struct {
	chars []char
	// tombstoned records ids deleted before their insert was ever
	// seen locally (an out-of-causal-order delivery), so the
	// character is created already-deleted once its insert does
	// arrive instead of briefly resurrecting content that was
	// supposed to be gone.
	tombstoned map[Timestamp]bool
}

func (t *Text) indexOf(id Timestamp) int {
	for i, c := range t.chars {
		if c.id == id {
			return i
		}
	}
	return -1
}

func (t *Text) hasID(id Timestamp) bool {
	return t.indexOf(id) >= 0
}

// visible returns the current non-tombstoned characters, in converged
// order, as both their rune values and their per-character ids.
func (t *Text) visible() (runes []rune, ids []Timestamp) {
	for _, c := range t.chars {
		if !c.deleted {
			runes = append(runes, c.value)
			ids = append(ids, c.id)
		}
	}
	return runes, ids
}

// String renders the current merged text.
func (t *Text) String() string {
	runes, _ := t.visible()
	return string(runes)
}

// insert splices a character into its RGA-converged position:
// immediately after origin, but after origin's ENTIRE existing
// subtree -- every character already nested under origin, at any
// depth, not just its direct children -- except for a direct sibling
// that loses the id tie-break (see Timestamp.after), which the new
// character is inserted ahead of. Skipping a whole subtree rather
// than just one sibling node is what keeps a winning sibling's own
// descendants attached to it regardless of which replica applies
// which insert first; comparing only direct array-adjacency here is
// a classic off-by-one that silently breaks associativity. Idempotent:
// re-inserting an id already present is a no-op.
func (t *Text) insert(id, origin Timestamp, value rune) {
	if t.hasID(id) {
		return
	}
	deleted := false
	if t.tombstoned[id] {
		deleted = true
		delete(t.tombstoned, id)
	}

	originPos := -1 // -1 represents the zero Timestamp: "start of text"
	if origin != (Timestamp{}) {
		idx := t.indexOf(origin)
		if idx < 0 {
			// Origin not seen yet. A correct topology delivers ops in
			// causal order per source replica, so this shouldn't
			// happen in practice -- append rather than drop the
			// character outright.
			t.chars = append(t.chars, char{id: id, origin: origin, value: value, deleted: deleted})
			return
		}
		originPos = idx
	}

	// A character's origin always appears earlier in the array than
	// the character itself, so walking forward from origin, anything
	// whose own origin sits at or after originPos is still somewhere
	// inside origin's subtree; the first character whose origin sits
	// *before* originPos marks having walked past that subtree
	// entirely.
	pos := originPos + 1
	for pos < len(t.chars) {
		cOriginPos := -1
		if t.chars[pos].origin != (Timestamp{}) {
			cOriginPos = t.indexOf(t.chars[pos].origin)
		}
		if cOriginPos < originPos {
			break
		}
		if cOriginPos == originPos && !t.chars[pos].id.after(id) {
			break // this direct sibling loses the tie-break; insert here
		}
		pos++
	}

	t.chars = append(t.chars, char{})
	copy(t.chars[pos+1:], t.chars[pos:])
	t.chars[pos] = char{id: id, origin: origin, value: value, deleted: deleted}
}

// delete tombstones a character by id, or records a pre-emptive
// tombstone if its insert hasn't arrived yet. Idempotent.
func (t *Text) delete(id Timestamp) {
	if i := t.indexOf(id); i >= 0 {
		t.chars[i].deleted = true
		return
	}
	if t.tombstoned == nil {
		t.tombstoned = make(map[Timestamp]bool)
	}
	t.tombstoned[id] = true
}

// apply performs one received CharOp against this Text.
func (t *Text) apply(op CharOp) {
	switch op.Kind {
	case CharInsert:
		t.insert(op.ID, op.Origin, op.Value)
	case CharDelete:
		t.delete(op.ID)
	}
}

// snapshotOps returns the full set of CharOps needed to reconstruct
// this exact Text, tombstones included, on a fresh replica: one
// CharInsert per character -- in an order where every origin already
// precedes its dependent, since that's the order they're stored in --
// followed by a CharDelete for each character already tombstoned.
func (t *Text) snapshotOps() []CharOp {
	ops := make([]CharOp, 0, len(t.chars)*2)
	for _, c := range t.chars {
		ops = append(ops, CharOp{Kind: CharInsert, ID: c.id, Origin: c.origin, Value: c.value})
	}
	for _, c := range t.chars {
		if c.deleted {
			ops = append(ops, CharOp{Kind: CharDelete, ID: c.id})
		}
	}
	return ops
}

// editKind is one step of a diffRunes edit script.
type editKind int

const (
	editKeep editKind = iota
	editInsert
	editDelete
)

// editStep is one instruction from diffRunes: for editKeep/editDelete,
// oldIndex points into the old rune slice; for editInsert, r is the
// character being added.
type editStep struct {
	kind     editKind
	oldIndex int
	r        rune
}

// diffRunes computes a minimal edit script turning old into new via a
// classic LCS (longest common subsequence) dynamic program. Not built
// for huge blocks -- O(n*m) time and space -- but fine for the
// paragraph-sized text this package's Content targets.
func diffRunes(old, new []rune) []editStep {
	n, m := len(old), len(new)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case old[i] == new[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var edits []editStep
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == new[j]:
			edits = append(edits, editStep{kind: editKeep, oldIndex: i})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			edits = append(edits, editStep{kind: editDelete, oldIndex: i})
			i++
		default:
			edits = append(edits, editStep{kind: editInsert, r: new[j]})
			j++
		}
	}
	for ; i < n; i++ {
		edits = append(edits, editStep{kind: editDelete, oldIndex: i})
	}
	for ; j < m; j++ {
		edits = append(edits, editStep{kind: editInsert, r: new[j]})
	}
	return edits
}
