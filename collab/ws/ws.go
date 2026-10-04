// Package ws is a collab transport over one WebSocket (collab.Transport):
// what HTTPEndpoints does with an event stream and POSTs, here with a single
// connection to the hub's /docs/{id}/ws endpoint. Use it where a long-lived
// socket is better than a stream plus requests -- lower latency, one
// connection instead of two, cheaper cursor traffic -- and your proxies allow
// WebSockets.
//
//	collab.ConnectWith(ws.WebSockets(), replica, flow, client.Options{BaseURL: "https://host", Doc: "d1"})
//
// BaseURL must be absolute (http or https; the socket uses ws or wss). A
// browser cannot set headers on a WebSocket, so under js/wasm the sign-in
// token (Authorization: Bearer ...) is sent as ?access_token= and a guard
// token (X-Gojiberry-Token) as ?guard_token=; other platforms send headers.
// The connection reconnects with backoff, sends what waited while offline, and
// reports the same events and statuses as the HTTP transport.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/link"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// WebSockets returns the WebSocket transport.
func WebSockets() link.Transport {
	return link.TransportFunc(func(opts client.Options) link.Conn { return Dial(opts) })
}

// maxBatch is how many operations one frame carries.
const maxBatch = 200

// Conn is a WebSocket connection to a hub.
type Conn struct {
	opts   client.Options
	ctx    context.Context
	cancel context.CancelFunc
	events chan client.Event
	wake   chan struct{}
	kick   chan struct{} // new ops to send
	pkick  chan struct{} // a cursor update to send
	done   sync.WaitGroup

	mu        sync.Mutex
	queue     []client.Event
	pending   []crdt.Op
	status    client.Status
	presence  *wire.Presence
	lastPres  time.Time
	presTimer *time.Timer
}

var _ link.Conn = (*Conn)(nil)

// Dial starts connecting and returns at once; watch Events.
func Dial(opts client.Options) *Conn {
	if opts.Retry <= 0 {
		opts.Retry = 500 * time.Millisecond
	}
	if opts.MaxRetry <= 0 {
		opts.MaxRetry = 15 * time.Second
	}
	if opts.PresenceInterval == 0 {
		opts.PresenceInterval = 100 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{opts: opts, ctx: ctx, cancel: cancel, events: make(chan client.Event),
		wake: make(chan struct{}, 1), kick: make(chan struct{}, 1), pkick: make(chan struct{}, 1)}
	c.done.Add(2)
	go func() { defer c.done.Done(); c.pump() }()
	go func() { defer c.done.Done(); c.run() }()
	return c
}

// Events implements collab.Conn.
func (c *Conn) Events() <-chan client.Event { return c.events }

// Status is the connection's state.
func (c *Conn) Status() client.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Pending implements collab.Conn.
func (c *Conn) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

// Send implements collab.Conn: ops are kept until the server accepts them.
func (c *Conn) Send(ops []crdt.Op) {
	if len(ops) == 0 {
		return
	}
	c.mu.Lock()
	c.pending = append(c.pending, ops...)
	c.mu.Unlock()
	signal(c.kick)
}

// SetPresence implements collab.Conn: not queued, merged and throttled (see
// client.Options.PresenceInterval); the latest always goes out.
func (c *Conn) SetPresence(p wire.Presence) {
	p.Replica = c.opts.Replica
	c.mu.Lock()
	c.presence = &p
	c.mu.Unlock()
	signal(c.pkick)
}

// Close implements collab.Conn.
func (c *Conn) Close() {
	c.mu.Lock()
	already := c.status == client.Closed
	c.status = client.Closed
	if c.presTimer != nil {
		c.presTimer.Stop()
	}
	c.mu.Unlock()
	if already {
		return
	}
	c.cancel()
	c.done.Wait()
	close(c.events)
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// emit queues an event; pump hands them over in order without ever blocking
// the connection on a slow consumer.
func (c *Conn) emit(e client.Event) {
	c.mu.Lock()
	c.queue = append(c.queue, e)
	c.mu.Unlock()
	signal(c.wake)
}

func (c *Conn) pump() {
	for {
		c.mu.Lock()
		var e client.Event
		have := len(c.queue) > 0
		if have {
			e = c.queue[0]
			c.queue = c.queue[1:]
		}
		c.mu.Unlock()
		if !have {
			select {
			case <-c.wake:
				continue
			case <-c.ctx.Done():
				return
			}
		}
		select {
		case c.events <- e:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Conn) setStatus(s client.Status) {
	c.mu.Lock()
	changed := c.status != s && c.status != client.Closed
	if changed {
		c.status = s
	}
	c.mu.Unlock()
	if changed {
		c.emit(client.Event{Kind: client.StatusEvent, Status: s})
	}
}

func (c *Conn) fail(err error) {
	if c.opts.OnError != nil && c.ctx.Err() == nil {
		c.opts.OnError(err)
	}
}

// wsURL is the socket's address.
func (c *Conn) wsURL() (string, error) {
	u, err := url.Parse(strings.TrimRight(c.opts.BaseURL, "/") + "/docs/" + url.PathEscape(c.opts.Doc) + "/ws")
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", errors.New("ws: BaseURL must be an absolute http(s) URL")
	}
	q := u.Query()
	q.Set("replica", c.opts.Replica)
	addCredentials(q, c.opts.Header)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// run connects, serves until the socket ends, and reconnects with backoff.
func (c *Conn) run() {
	delay := c.opts.Retry
	for c.ctx.Err() == nil {
		c.setStatus(client.Connecting)
		began := time.Now()
		err := c.session()
		if c.ctx.Err() != nil {
			return
		}
		c.setStatus(client.Offline)
		if err != nil {
			c.fail(err)
		}
		if time.Since(began) > c.opts.MaxRetry {
			delay = c.opts.Retry // it was up for a while: start the backoff over
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, c.opts.MaxRetry)
	}
}

// session is one connection's life.
func (c *Conn) session() error {
	u, err := c.wsURL()
	if err != nil {
		return err
	}
	dctx, dcancel := context.WithTimeout(c.ctx, 15*time.Second)
	sock, _, err := websocket.Dial(dctx, u, dialOptions(c.opts))
	dcancel()
	if err != nil {
		return err
	}
	defer func() { _ = sock.CloseNow() }()
	sock.SetReadLimit(64 << 20)

	in := make(chan wire.Frame)
	readErr := make(chan error, 1)
	rctx, rcancel := context.WithCancel(c.ctx)
	defer rcancel()
	go func() {
		for {
			_, data, err := sock.Read(rctx)
			if err != nil {
				readErr <- err
				return
			}
			var f wire.Frame
			if json.Unmarshal(data, &f) != nil {
				continue
			}
			select {
			case in <- f:
			case <-rctx.Done():
				return
			}
		}
	}()

	write := func(f wire.Frame) error {
		b, err := json.Marshal(f)
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(c.ctx, 15*time.Second)
		defer wcancel()
		return sock.Write(wctx, websocket.MessageText, b)
	}
	var nextID, inflightID uint64
	var inflight []crdt.Op
	trySend := func() error {
		if len(inflight) > 0 {
			return nil // one batch at a time, answered before the next
		}
		c.mu.Lock()
		n := min(len(c.pending), maxBatch)
		batch := append([]crdt.Op(nil), c.pending[:n]...)
		c.mu.Unlock()
		if n == 0 {
			return nil
		}
		data, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		nextID++
		inflightID, inflight = nextID, batch
		return write(wire.Frame{Event: "ops", ID: nextID, Data: data})
	}
	settle := func() { // the in-flight batch is no longer waiting
		c.mu.Lock()
		c.pending = c.pending[min(len(inflight), len(c.pending)):]
		c.mu.Unlock()
		inflight = nil
	}
	sendPresence := func() error {
		c.mu.Lock()
		p := c.presence
		c.presence = nil
		c.lastPres = time.Now()
		c.mu.Unlock()
		if p == nil {
			return nil
		}
		data, _ := json.Marshal(p)
		return write(wire.Frame{Event: "presence", Data: data})
	}
	for {
		select {
		case f := <-in:
			switch f.Event {
			case "hello":
				var h struct{ Role wire.Role }
				if json.Unmarshal(f.Data, &h) == nil {
					c.setStatus(client.Online)
					c.emit(client.Event{Kind: client.Hello, Role: h.Role})
					if err := trySend(); err != nil { // what waited while offline goes now
						return err
					}
				}
			case "snapshot", "ops":
				var ops []crdt.Op
				if json.Unmarshal(f.Data, &ops) == nil {
					k := client.Ops
					if f.Event == "snapshot" {
						k = client.Snapshot
					}
					c.emit(client.Event{Kind: k, Ops: ops})
				}
			case "presence":
				var p wire.Presence
				if json.Unmarshal(f.Data, &p) == nil {
					c.emit(client.Event{Kind: client.PresenceEvent, Presence: p})
				}
			case "ack":
				if f.ID == inflightID && len(inflight) > 0 {
					settle()
					if err := trySend(); err != nil {
						return err
					}
				}
			case "reject":
				if f.ID == inflightID && len(inflight) > 0 {
					refused := inflight
					settle()
					c.emit(client.Event{Kind: client.Rejected, Ops: refused, Err: errors.New(f.Error)})
					if err := trySend(); err != nil {
						return err
					}
				}
			}
		case <-c.kick:
			if c.Status() == client.Online {
				if err := trySend(); err != nil {
					return err
				}
			}
		case <-c.pkick:
			if c.Status() != client.Online {
				continue
			}
			c.mu.Lock()
			wait := c.opts.PresenceInterval - time.Since(c.lastPres)
			c.mu.Unlock()
			if c.opts.PresenceInterval > 0 && wait > 0 {
				// Too soon: merge into one update when the interval ends.
				c.mu.Lock()
				if c.presTimer == nil {
					c.presTimer = time.AfterFunc(wait, func() {
						c.mu.Lock()
						c.presTimer = nil
						c.mu.Unlock()
						signal(c.pkick)
					})
				}
				c.mu.Unlock()
				continue
			}
			if err := sendPresence(); err != nil {
				return err
			}
		case err := <-readErr:
			return err
		case <-c.ctx.Done():
			return nil
		}
	}
}
