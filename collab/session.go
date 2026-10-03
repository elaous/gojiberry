package collab

import (
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
	Conn   *client.Conn
	// Role is what the server says you may do (empty until it says).
	Role wire.Role
	// Peers are the other people's cursors, by replica id.
	Peers map[string]wire.Presence
	// Status is the connection's state.
	Status client.Status
	// Err is the last error the server reported for something you sent
	// (see client.Rejected); the replica is then out of step and the
	// session should be rebuilt.
	Err error
	// Name and Color are shown to others with your cursor.
	Name, Color string

	snapshots int
}

// Connect joins the document opts.Doc with flow. If the server has no
// content for it yet and you may edit, the flow's content becomes the
// document. opts.Replica is set to replica.
func Connect(replica string, flow *canvas.TextFlow, opts client.Options) (*Session, error) {
	b, err := Join(replica, flow)
	if err != nil {
		return nil, err
	}
	opts.Replica = replica
	conn := client.Dial(opts)
	b.Outgoing = conn.Send
	return &Session{Bridge: b, Conn: conn, Peers: map[string]wire.Presence{}}, nil
}

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
}

// Pending is how many of your changes have not reached the server yet.
func (s *Session) Pending() int { return s.Conn.Pending() }

func (s *Session) handle(e client.Event) bool {
	switch e.Kind {
	case client.Hello:
		s.Role = e.Role
		return true
	case client.Snapshot:
		s.snapshots++
		if len(e.Ops) == 0 && !s.Bridge.Ready() {
			if s.Role.CanEdit() {
				s.Bridge.Seed()
			}
			return true
		}
		s.Bridge.Apply(e.Ops...)
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
		return true
	case client.PresenceEvent:
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
		s.Err = e.Err
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
	s.Conn.SetPresence(wire.Presence{Name: s.Name, Color: s.Color, Has: true, Anchor: a.id, Caret: p.id})
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
