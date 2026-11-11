package collab

import (
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/crdt"
)

// DefaultTypedFor is how long others' typed text stays marked.
const DefaultTypedFor = 4 * time.Second

// maxTyped bounds the characters remembered as just typed.
const maxTyped = 2000

// typedChar is a character another person inserted, and when.
type typedChar struct {
	id      crdt.Timestamp
	replica string
	at      time.Time
}

func (s *Session) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// seen records the newest character each replica is known to have.
func (s *Session) seen(ops []crdt.Op, note bool) {
	if s.maxSeen == nil {
		s.maxSeen = map[string]uint64{}
	}
	now := s.clock()
	if !s.loaded {
		for _, op := range ops {
			for _, c := range op.CharOps {
				if c.Kind == crdt.CharInsert {
					s.loaded, note = true, false
				}
			}
		}
	}
	for _, op := range ops {
		for _, c := range op.CharOps {
			// A typed character, or an object, with its author.
			var stamp crdt.Timestamp
			var at crdt.Timestamp // the character to show
			switch {
			case c.Kind == crdt.CharInsert:
				stamp, at = c.ID, c.ID
			case c.Kind == crdt.CharMark && c.Mark != nil && c.Mark.Key == ctlKey:
				stamp = c.Mark.ID
				if c.Mark.End.Side == crdt.After && c.Mark.End.ID != (crdt.Timestamp{}) {
					at = c.Mark.End.ID
				} else {
					at = c.Mark.Start.ID
				}
			default:
				continue
			}
			if stamp.Replica == "" {
				continue
			}
			fresh := stamp.Counter > s.maxSeen[stamp.Replica]
			if fresh {
				s.maxSeen[stamp.Replica] = stamp.Counter
			}
			if note && fresh && s.TypedFor >= 0 && stamp.Replica != s.replica && at != (crdt.Timestamp{}) {
				s.typed = append(s.typed, typedChar{id: at, replica: stamp.Replica, at: now})
			}
		}
	}
	if len(s.typed) > maxTyped {
		s.typed = s.typed[len(s.typed)-maxTyped:]
	}
}

// noteTyped remembers characters other people inserted in ops.
func (s *Session) noteTyped(ops []crdt.Op) { s.seen(ops, true) }

// typedRanges drops text typed longer ago than TypedFor.
func (s *Session) typedRanges() []canvas.TypedRange {
	if len(s.typed) == 0 {
		return nil
	}
	keep := s.TypedFor
	if keep == 0 {
		keep = DefaultTypedFor
	}
	now := s.clock()
	live := s.typed[:0]
	for _, t := range s.typed {
		if now.Sub(t.at) < keep {
			live = append(live, t)
		}
	}
	s.typed = live
	if len(live) == 0 || !s.Bridge.Ready() {
		return nil
	}
	b := s.Bridge
	atoms, ids := b.read(), b.Doc.VisibleIDs(block)
	index := make(map[crdt.Timestamp]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	cum := make([]int, len(atoms)+1) // stream offset before each atom
	for i, a := range atoms {
		cum[i+1] = cum[i]
		if a.inStream() {
			cum[i+1]++
		}
	}
	var out []canvas.TypedRange
	done := map[crdt.Timestamp]bool{}
	for _, t := range live {
		if done[t.id] {
			continue
		}
		done[t.id] = true
		i, ok := index[t.id]
		if !ok || i >= len(atoms) || !atoms[i].inStream() {
			continue
		}
		name, color := "", ""
		if p, ok := s.Peers[t.replica]; ok {
			name, color = p.Name, p.Color
			if name == "" {
				name = p.User
			}
		} else if n, ok := s.names[t.replica]; ok {
			name = n
		}
		if name == "" {
			name = t.replica
		}
		from, to := cum[i], cum[i+1]
		if n := len(out); n > 0 && out[n-1].To == from && out[n-1].Name == name && out[n-1].Color == color {
			out[n-1].To = to
			continue
		}
		out = append(out, canvas.TypedRange{From: from, To: to, Name: name, Color: color})
	}
	return out
}
