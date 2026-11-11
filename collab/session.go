package collab

import (
	"fmt"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// Session ties a Bridge to a live connection.
type Session struct {
	Bridge *Bridge
	Conn   Conn
	// Role is what the server allows (empty until connected).
	Role wire.Role
	// Peers are other people's cursors, keyed by replica id.
	Peers map[string]wire.Presence
	// Status is the connection's state.
	Status client.Status
	// Err is the last error the server reported.
	Err error
	// NoRepair stops Poll from repairing a session automatically.
	NoRepair bool
	// Repairs counts how many times the session was repaired.
	Repairs int
	// Merged counts changes made while offline and merged later.
	Merged, Rejected int
	// Conflicts counts times others changed a paragraph edited here.
	Conflicts    int
	ConflictWith []string
	// Name and Color are shown to others with your cursor.
	Name, Color string
	// Holding is the object this person is changing.
	Holding string
	// TypedFor is how long others' typed text stays highlighted.
	TypedFor time.Duration

	typed   []typedChar
	maxSeen map[string]uint64
	loaded  bool             // the document's own characters have arrived
	now     func() time.Time // the clock, replaced in tests

	replica   string
	names     map[string]string
	opts      client.Options
	transport Transport
	needFix   bool
	sharing   Sharing

	snapshots int
	waiting   int
}

// Connect joins the document opts.Doc with flow.
func Connect(replica string, flow *canvas.TextFlow, opts client.Options) (*Session, error) {
	return ConnectWith(HTTPEndpoints(), replica, flow, opts)
}

// ConnectWith is Connect over the given transport.
func ConnectWith(t Transport, replica string, flow *canvas.TextFlow, opts client.Options) (*Session, error) {
	return ConnectShared(t, replica, flow, Sharing{}, opts)
}

// ConnectShared is ConnectWith, sharing a chosen set of side data.
func ConnectShared(t Transport, replica string, flow *canvas.TextFlow, sh Sharing, opts client.Options) (*Session, error) {
	if t == nil {
		t = HTTPEndpoints()
	}
	b, err := Join(replica, flow)
	if err != nil {
		return nil, err
	}
	b.Configure(sh)
	opts.Replica = replica
	conn := t.Dial(opts)
	b.Outgoing = conn.Send
	s := &Session{Bridge: b, Conn: conn, Peers: map[string]wire.Presence{}, replica: replica, opts: opts, transport: t, sharing: sh}
	b.OnConflict = s.noteConflict
	return s, nil
}

// Repair rebuilds the session after the server refused it.
func (s *Session) Repair() error {
	s.needFix = false
	s.Bridge.Detach()
	s.Conn.Close()
	s.Repairs++
	replica := fmt.Sprintf("%s-r%d", s.replica, s.Repairs)
	b, err := Join(replica, s.Bridge.Flow)
	if err != nil {
		return err
	}
	b.Configure(s.sharing)
	opts := s.opts
	opts.Replica = replica
	conn := s.transport.Dial(opts)
	b.Outgoing = conn.Send
	s.Bridge, s.Conn, s.Err, s.needFix = b, conn, nil, false
	b.OnConflict = s.noteConflict
	s.Peers = map[string]wire.Presence{}
	s.snapshots = 0
	return nil
}

func (s *Session) noteConflict(c Conflict) {
	s.Conflicts++
	s.ConflictWith = s.ConflictWith[:0:0]
	for _, r := range c.Replicas {
		name := s.names[r]
		if p, ok := s.Peers[r]; ok && p.Name != "" {
			name = p.Name
		}
		s.ConflictWith = append(s.ConflictWith, name)
	}
}

// Replica is this session's replica id.
func (s *Session) Replica() string { return s.replica }

// Close ends the connection.
func (s *Session) Close() { s.Conn.Close() }

// Poll applies every arrived event, without waiting.
func (s *Session) Poll() bool {
	changed := false
	for {
		select {
		case e, ok := <-s.Conn.Events():
			if !ok {
				return changed
			}
			if s.handle(e) {
				changed = true
			}
			if s.needFix {
				_ = s.Repair() // a flow the bridge cannot carry keeps its Err
				return true
			}
		default:
			if changed {
				s.RefreshPeers()
			}
			return changed
		}
	}
}

func (s *Session) RefreshPeers() {
	var rc []canvas.RemoteCursor
	for _, pc := range s.PeerCursors() {
		name := pc.Presence.Name
		if name == "" {
			name = pc.Presence.User
		}
		rc = append(rc, canvas.RemoteCursor{Name: name, Color: pc.Presence.Color, Anchor: pc.Anchor, Caret: pc.Caret})
	}
	s.Bridge.Flow.RemoteCursors = rc
	s.Bridge.Flow.TypedRanges = s.typedRanges()
}

func (s *Session) Pending() int { return s.Conn.Pending() }

func (s *Session) handle(e client.Event) bool {
	switch e.Kind {
	case client.Hello:
		s.Role = e.Role
		s.waiting = e.Waiting
		return true
	case client.Snapshot:
		s.snapshots++
		if s.waiting > 0 && s.Role.CanEdit() {
			s.Merged += s.waiting
		}
		s.waiting = 0
		if len(e.Ops) == 0 && !s.Bridge.Ready() {
			if s.Role.CanEdit() {
				s.Bridge.Seed()
			}
			return true
		}
		s.Bridge.Apply(e.Ops...)
		s.seen(e.Ops, false)
		if s.Role.CanEdit() {
			s.Bridge.PushSurfaces() // what this side has and the document lacks
		}
		if s.Bridge.Ready() && s.Role.CanEdit() {
			if all := s.Bridge.Snapshot(); opWeight(all) > opWeight(e.Ops) {
				s.Conn.Send(all)
			}
		}
		return true
	case client.Ops:
		s.Bridge.Apply(e.Ops...)
		s.noteTyped(e.Ops)
		return true
	case client.PresenceEvent:
		if e.Presence.Name != "" {
			if s.names == nil {
				s.names = map[string]string{}
			}
			s.names[e.Presence.Replica] = e.Presence.Name
		}
		if e.Presence.Gone {
			delete(s.Peers, e.Presence.Replica)
		} else {
			s.Peers[e.Presence.Replica] = e.Presence
		}
		return true
	case client.StatusEvent:
		s.Status = e.Status
		return true
	case client.Rejected:
		s.Rejected += len(e.Ops)
		s.Err = e.Err
		s.needFix = !s.NoRepair
		return true
	}
	return false
}

// PublishCursor tells the others where your cursor is.
func (s *Session) PublishCursor() {
	b := s.Bridge
	if !b.Ready() {
		return
	}
	atoms, ids := b.read(), b.Doc.VisibleIDs(block)
	c := b.Flow.Cursors()[0]
	a, p := b.anchorOf(atoms, ids, c.Anchor), b.anchorOf(atoms, ids, c.Caret)
	s.Conn.SetPresence(wire.Presence{Name: s.Name, Color: s.Color, Has: true, Anchor: a.id, Caret: p.id, Holding: s.Holding})
}

// PeerCursor is another person's cursor placed in your flow.
type PeerCursor struct {
	Presence wire.Presence
	// Anchor and Caret are stream offsets in the flow.
	Anchor, Caret int
}

// PeerCursors places the other people's cursors in the flow's text.
func (s *Session) PeerCursors() []PeerCursor {
	b := s.Bridge
	atoms := b.read()
	where := func(id crdt.Timestamp) int {
		return b.offsetOf(cursorAnchor{id: id, start: id == (crdt.Timestamp{})}, atoms)
	}
	var out []PeerCursor
	for _, p := range s.Peers {
		if p.Has {
			out = append(out, PeerCursor{Presence: p, Anchor: where(p.Anchor), Caret: where(p.Caret)})
		}
	}
	return out
}

func opWeight(ops []crdt.Op) int {
	n := 0
	for _, op := range ops {
		n += 1 + len(op.CharOps)
	}
	return n
}
