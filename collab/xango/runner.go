package xango

import (
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/collab"
	"github.com/elaous/gojiberry/collab/wire"
)

// runner polls a session on a timer and applies what it learns: the others'
// edits (Session.Poll puts them in the flow), their cursors, the role.
type runner struct {
	sess  *collab.Session
	flow  *canvas.TextFlow
	ctx   *cxango.Context
	props atomic.Pointer[CollabProps]

	quit chan struct{}
}

func newRunner(ctx *cxango.Context, sess *collab.Session, flow *canvas.TextFlow, p *CollabProps) *runner {
	r := &runner{sess: sess, flow: flow, ctx: ctx, quit: make(chan struct{})}
	r.props.Store(p)
	go r.loop()
	return r
}

// stop ends the runner without waiting for it: it is called from the goroutine
// that owns the canvas, which the loop may be waiting for (Sync). The loop
// closes the session itself when it sees quit, and does nothing once it has.
func (r *runner) stop() {
	close(r.quit)
	r.sess.Bridge.Detach() // the flow stops sharing and gets its own undo back
}

func (r *runner) do(f func()) {
	if s := r.props.Load().Sync; s != nil {
		s(f)
		return
	}
	f()
}

func (r *runner) loop() {
	defer func() { r.sess.Close() }() // the loop owns the session, which Repair may replace
	interval := 40 * time.Millisecond
	if iv := r.props.Load().Interval; iv > 0 {
		interval = iv
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var lastCursor, lastCursors, lastTyped, lastState string
	var lastPublish time.Time
	for {
		select {
		case <-r.quit:
			return
		case <-tick.C:
		}
		r.do(func() {
			select {
			case <-r.quit:
				return // stopped while waiting for the canvas
			default:
			}
			s := r.sess // Poll may repair the session and replace what is inside it
			changed := s.Poll()
			if s.Role.CanEdit() { // a viewer's or commenter's surface records would be refused
				s.Bridge.SyncSurfaces()
			}
			redraw := changed
			if role := canvas.ParseRole(string(s.Role)); s.Role != "" && r.ctx.Stage != nil && role != r.ctx.Stage.Role {
				r.ctx.Stage.SetRole(role)
				redraw = true
			}
			s.RefreshPeers()
			// Tell the others where we are when the cursor moves, and also when
			// the session becomes ready or reconnects (they know nothing of us
			// until then, and the cursor may not move for a while).
			if f := r.props.Load().TypedFor; f != nil {
				s.TypedFor = f()
			}
			if h := r.props.Load().Holding; h != nil {
				s.Holding = h()
			}
			// A held object is renewed every few seconds, before the hub forgets the presence it came with.
			renew := s.Holding != "" && time.Since(lastPublish) > holdRenew
			if c := fmt.Sprint(r.flow.Cursors()[0], s.Bridge.Ready(), s.Status, s.Repairs, s.Holding); (c != lastCursor || renew) && s.Bridge.Ready() {
				lastCursor, lastPublish = c, time.Now()
				s.PublishCursor()
			}
			if typed := fmt.Sprint(r.flow.TypedRanges); typed != lastTyped {
				lastTyped = typed
				redraw = true
			}
			if cursors := fmt.Sprint(r.flow.RemoteCursors); cursors != lastCursors {
				lastCursors = cursors
				redraw = true
			}
			if st := r.state(); fmt.Sprint(st) != lastState {
				lastState = fmt.Sprint(st)
				if cb := r.props.Load().OnState; cb != nil {
					cb(st)
				}
			}
			if redraw && r.ctx.Redraw != nil {
				r.ctx.Redraw()
			}
		})
	}
}

// state summarises the session for OnState.
func (r *runner) state() State {
	s := r.sess
	st := State{Replica: s.Replica(), Status: s.Status, Role: s.Role, Pending: s.Pending(), Repairs: s.Repairs, Merged: s.Merged, Rejected: s.Rejected, Conflicts: s.Conflicts, ConflictWith: slices.Clone(s.ConflictWith), Err: s.Err}
	if err := s.Bridge.Err(); err != nil && st.Err == nil {
		st.Err = err
	}
	for _, p := range s.Peers {
		st.Peers = append(st.Peers, p)
	}
	slices.SortFunc(st.Peers, func(a, b wire.Presence) int {
		if a.Name != b.Name {
			if a.Name < b.Name {
				return -1
			}
			return 1
		}
		if a.Replica < b.Replica {
			return -1
		}
		if a.Replica > b.Replica {
			return 1
		}
		return 0
	})
	return st
}

// holdRenew is how often a held object is announced again.
const holdRenew = 3 * time.Second
