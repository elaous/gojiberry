package collab

import (
	"fmt"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// Session ties a Bridge to a live connection: local edits go to the server
// as they happen, and changes from the others are applied when you call
// Poll. Call Poll from the goroutine that owns the flow (the UI's loop or
// timer): it applies everything that has arrived and returns, so the canvas
// is only ever touched from one goroutine.
type Session struct {
	Bridge *Bridge
	Conn   Conn
	// Role is what the server says you may do (empty until it says).
	Role wire.Role
	// Peers are the other people's cursors, by replica id.
	Peers map[string]wire.Presence
	// Status is the connection's state.
	Status client.Status
	// Err is the last error the server reported for something you sent
	// (see client.Rejected). The replica is then out of step with the
	// server; Poll repairs the session (see Repair) unless NoRepair is set.
	Err error
	// NoRepair stops Poll from repairing a session after a rejection; Err is
	// then left set for the caller to deal with.
	NoRepair bool
	// Repairs counts how many times the session has been rebuilt.
	Repairs int
	// Merged counts the changes that were made while offline (or kept from an earlier visit) and met the
	// server's copy when the connection came back: each time, the number of changes that waited. Rejected
	// counts the changes the server refused (a viewer's edit, a batch that was too large), which a repair
	// then drops. Both only grow, and survive Repair; a host announces the difference since it last looked.
	Merged, Rejected int
	// Conflicts counts the times other people changed a paragraph that was being edited here (the merge keeps
	// both changes); ConflictWith names them (from the presence list; empty when a replica is not known)
	// for the latest one. Both survive Repair.
	Conflicts    int
	ConflictWith []string
	// Name and Color are shown to others with your cursor.
	Name, Color string
	// Holding is the object this person is changing, sent with the cursor (see wire.Presence.Holding); the host sets it.
	Holding string
	// TypedFor is how long text other people typed stays in their colour (see canvas.TypedRange); zero is
	// DefaultTypedFor, negative turns the colours off.
	TypedFor time.Duration

	typed   []typedChar
	maxSeen map[string]uint64
	loaded  bool             // the document's own characters have arrived
	now     func() time.Time // the clock, replaced in tests

	replica   string
	names     map[string]string // the name each replica gave, kept after it leaves, for saying who did something
	opts      client.Options
	transport Transport
	needFix   bool
	sharing   Sharing

	snapshots int
	waiting   int // changes that waited when the last connection was made, until its snapshot is applied
}

// Connect joins the document opts.Doc with flow. If the server has no
// content for it yet and you may edit, the flow's content becomes the
// document. opts.Replica is set to replica.
func Connect(replica string, flow *canvas.TextFlow, opts client.Options) (*Session, error) {
	return ConnectWith(HTTPEndpoints(), replica, flow, opts)
}

// ConnectWith is Connect over the given transport (HTTPEndpoints, or another
// such as collab/ws's WebSockets).
func ConnectWith(t Transport, replica string, flow *canvas.TextFlow, opts client.Options) (*Session, error) {
	return ConnectShared(t, replica, flow, Sharing{}, opts)
}

// ConnectShared is ConnectWith sharing more (or less) than the flow's text:
// see Sharing.
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

// Repair rebuilds the session after the server refused something: the local
// replica holds changes the server will never have, so it is dropped, a new
// replica joins from the server's copy, and the flow shows what the server
// has. Edits that were refused are lost; the others' work and the document
// are intact. It is called by Poll after a rejection (unless NoRepair), and
// can be called by hand.
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

// noteConflict counts a paragraph that others changed under the person and names who, as far as the
// presence list knows them.
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

// Replica is this session's replica id, as the others see it in their presence lists.
func (s *Session) Replica() string { return s.replica }

// Close ends the connection.
func (s *Session) Close() { s.Conn.Close() }

// Poll applies every event that has arrived, without waiting, and reports
// whether anything changed (the flow, the peers or the status), so the
// caller knows to redraw.
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

// RefreshPeers puts the other people's cursors into the flow
// (TextFlow.RemoteCursors) at the places their characters are now. Poll
// does it when something arrived; call it again after local edits if you
// want the cursors to stay on their text as you type.
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

// Pending is how many of your changes have not reached the server yet.
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
		// A hub that restarted (or lost its last saves) answers with less than
		// we hold: the edits it acknowledged earlier are gone from it, and
		// nothing else would send them again. Operations are idempotent, so
		// sending the whole document is safe.
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

// opWeight counts operations including their character operations: how much a
// set of operations says.
func opWeight(ops []crdt.Op) int {
	n := 0
	for _, op := range ops {
		n += 1 + len(op.CharOps)
	}
	return n
}
