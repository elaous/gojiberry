package collab

import (
	"slices"
	"strings"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

// DefaultConflictWindow is how long a paragraph counts as "being edited here" after the last local change in it
// (Bridge.ConflictWindow).
const DefaultConflictWindow = 10 * time.Second

// Conflict is a paragraph that another person changed while it was being edited here. The merge keeps both
// changes (characters are merged one by one), so nothing is lost; it is worth saying because the text the
// person is looking at moved under them.
type Conflict struct {
	// Paragraph is the paragraph as it was here just before the other change arrived.
	Paragraph string
	// Replicas are the replicas whose operations arrived (other than this one), in order.
	Replicas []string
}

// recentEdit is a local change, by the ids of the characters it put in or took out beside (a deletion is
// remembered by the character before it), and when it was made.
type recentEdit struct {
	ids []crdt.Timestamp
	at  time.Time
}

// now is replaced by tests.
var now = time.Now

func (b *Bridge) conflictWindow() time.Duration {
	if b.ConflictWindow > 0 {
		return b.ConflictWindow
	}
	return DefaultConflictWindow
}

// noteRecent remembers that the characters with these ids were just edited here.
func (b *Bridge) noteRecent(ids []crdt.Timestamp) {
	if len(ids) == 0 {
		return
	}
	t := now()
	w := b.conflictWindow()
	b.recent = slices.DeleteFunc(b.recent, func(r recentEdit) bool { return t.Sub(r.at) > w })
	b.recent = append(b.recent, recentEdit{slices.Clone(ids), t})
	if len(b.recent) > 64 {
		b.recent = b.recent[len(b.recent)-64:]
	}
}

// checkConflict looks at the operations other people sent, against the text as it was here before they were
// merged (before, and the id of each of its characters): an insert placed after a character of a paragraph
// that was edited here lately, or a deletion of one, is a change to that paragraph. Paragraphs are found by the
// ids of their characters, so two paragraphs with the same words are not mixed up.
func (b *Bridge) checkConflict(before []atom, beforeIDs []crdt.Timestamp, ops []crdt.Op) {
	if b.OnConflict == nil || len(b.recent) == 0 || len(before) != len(beforeIDs) {
		return
	}
	t := now()
	w := b.conflictWindow()
	var replicas []string
	add := func(r string) {
		if r != "" && r != b.replica && !slices.Contains(replicas, r) {
			replicas = append(replicas, r)
		}
	}
	for _, op := range ops {
		for _, c := range op.CharOps {
			add(c.ID.Replica)
		}
	}
	if len(replicas) == 0 {
		return
	}
	index := make(map[crdt.Timestamp]int, len(beforeIDs))
	for i, id := range beforeIDs {
		index[id] = i
	}
	// the paragraphs (index ranges) edited here lately
	type span struct{ start, end int }
	var spans []span
	for _, r := range b.recent {
		if t.Sub(r.at) > w {
			continue
		}
		for _, id := range r.ids {
			i, ok := index[id]
			if !ok {
				continue
			}
			s, e := i, i
			for s > 0 && before[s-1].r != '\n' {
				s--
			}
			for e < len(before) && before[e].r != '\n' {
				e++
			}
			sp := span{s, e}
			if !slices.Contains(spans, sp) {
				spans = append(spans, sp)
			}
		}
	}
	if len(spans) == 0 {
		return
	}
	for _, op := range ops {
		for _, c := range op.CharOps {
			if c.ID.Replica == b.replica {
				continue
			}
			var at int
			var ok bool
			switch c.Kind {
			case crdt.CharInsert:
				at, ok = index[c.Origin] // placed after this character
				if ok && before[at].r == '\n' {
					at++ // after a line break: the start of the next paragraph
				}
			case crdt.CharDelete:
				at, ok = index[c.ID]
			}
			if !ok {
				continue
			}
			for _, sp := range spans {
				// an insert at the very end of the paragraph (after its last character) or the start of the next
				// (after the break) is at e / e+1: only e belongs to this one.
				if at >= sp.start && at <= sp.end {
					b.OnConflict(Conflict{Paragraph: paragraphText(before, sp.start, sp.end), Replicas: replicas})
					b.recent = nil
					return
				}
			}
		}
	}
}

func paragraphText(atoms []atom, start, end int) string {
	var sb strings.Builder
	for _, a := range atoms[start:min(end, len(atoms))] {
		sb.WriteRune(a.r)
	}
	return sb.String()
}
