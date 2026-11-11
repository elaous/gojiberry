package collab

import (
	"slices"
	"strings"
	"time"

	"github.com/elaous/gojiberry/crdt"
)

// DefaultConflictWindow is the default recent-edit window.
const DefaultConflictWindow = 10 * time.Second

type Conflict struct {
	Paragraph string
	// Replicas are the replicas whose changes were merged.
	Replicas []string
}

type recentEdit struct {
	ids []crdt.Timestamp
	at  time.Time
}

var now = time.Now

func (b *Bridge) conflictWindow() time.Duration {
	if b.ConflictWindow > 0 {
		return b.ConflictWindow
	}
	return DefaultConflictWindow
}

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
					at++
				}
			case crdt.CharDelete:
				at, ok = index[c.ID]
			}
			if !ok {
				continue
			}
			for _, sp := range spans {
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
