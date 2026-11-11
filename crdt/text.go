package crdt

import "sort"

// MergeStrategy selects how Doc.Apply resolves concurrent writes.
type MergeStrategy int

const (
	// MergeLWW keeps the whole Content of the latest write.
	MergeLWW MergeStrategy = iota
	// MergeCharCRDT merges concurrent writes per character.
	MergeCharCRDT
)

// CharOpKind is the kind of edit a CharOp carries.
type CharOpKind int

const (
	CharInsert CharOpKind = iota
	CharDelete
	// CharMark adds a formatting Mark; see marks.go.
	CharMark
)

// CharOp is one character-level edit, the unit MergeCharCRDT exchanges.
type CharOp struct {
	Kind CharOpKind
	// ID is the character's globally unique id.
	ID     Timestamp
	Origin Timestamp
	// Value is the inserted character.
	Value rune
	// Mark is the formatting span a CharMark adds.
	Mark *Mark `json:",omitempty"`
}

type char struct {
	id      Timestamp
	origin  Timestamp
	value   rune
	deleted bool
}

const maxPending = 1 << 16

// Text is a per-character CRDT sequence.
type Text struct {
	chars      []char
	tombstoned map[Timestamp]bool
	pending    []CharOp
	marks      []Mark
	markIDs    map[Timestamp]bool
	pos        map[Timestamp]int
	posValid   int
}

func (t *Text) indexOf(id Timestamp) int {
	if t.pos == nil {
		t.pos = make(map[Timestamp]int, len(t.chars))
	}
	for ; t.posValid < len(t.chars); t.posValid++ {
		t.pos[t.chars[t.posValid].id] = t.posValid
	}
	if i, ok := t.pos[id]; ok && i < len(t.chars) && t.chars[i].id == id {
		return i
	}
	return -1
}

func (t *Text) invalidateFrom(i int) {
	if i < t.posValid {
		t.posValid = i
	}
}

func (t *Text) hasID(id Timestamp) bool {
	return t.indexOf(id) >= 0
}

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

func (t *Text) insert(id, origin Timestamp, value rune) {
	if t.hasID(id) {
		return
	}
	deleted := false
	if t.tombstoned[id] {
		deleted = true
		delete(t.tombstoned, id)
	}

	originPos := -1 // -1 represents the zero Timestamp.
	if origin != (Timestamp{}) {
		idx := t.indexOf(origin)
		if idx < 0 {
			for _, p := range t.pending {
				if p.ID == id {
					return
				}
			}
			if len(t.pending) >= maxPending {
				return
			}
			if deleted {
				t.tombstoned[id] = true // keep the early delete for when it is placed
			}
			t.pending = append(t.pending, CharOp{Kind: CharInsert, ID: id, Origin: origin, Value: value})
			return
		}
		originPos = idx
	}

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
			break
		}
		pos++
	}

	t.chars = append(t.chars, char{})
	copy(t.chars[pos+1:], t.chars[pos:])
	t.chars[pos] = char{id: id, origin: origin, value: value, deleted: deleted}
	t.invalidateFrom(pos)
	t.placePending(id)
}

func (t *Text) placePending(id Timestamp) {
	for i := 0; i < len(t.pending); i++ {
		p := t.pending[i]
		if p.Origin != id {
			continue
		}
		t.pending = append(t.pending[:i], t.pending[i+1:]...)
		t.insert(p.ID, p.Origin, p.Value)
		i = -1 // the slice changed under us; start over
	}
}

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

func (t *Text) apply(op CharOp) {
	switch op.Kind {
	case CharInsert:
		t.insert(op.ID, op.Origin, op.Value)
	case CharDelete:
		t.delete(op.ID)
	case CharMark:
		if op.Mark != nil {
			t.addMark(*op.Mark)
		}
	}
}

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
	for i := range t.marks {
		m := t.marks[i]
		ops = append(ops, CharOp{Kind: CharMark, Mark: &m})
	}
	ops = append(ops, t.pending...)
	var early []Timestamp
	for id := range t.tombstoned {
		early = append(early, id)
	}
	sort.Slice(early, func(i, j int) bool {
		if early[i].Counter != early[j].Counter {
			return early[i].Counter < early[j].Counter
		}
		return early[i].Replica < early[j].Replica
	})
	for _, id := range early {
		ops = append(ops, CharOp{Kind: CharDelete, ID: id})
	}
	return ops
}

type editKind int

const (
	editKeep editKind = iota
	editInsert
	editDelete
)

type editStep struct {
	kind     editKind
	oldIndex int
	r        rune
}

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
