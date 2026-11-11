package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// Status is the state of the connection.
type Status int

const (
	// Connecting: trying to reach the server.
	Connecting Status = iota
	// Online: the event stream is open.
	Online
	// Offline: the last attempt failed; the next is scheduled.
	Offline
	// Closed: Close was called.
	Closed
)

func (s Status) String() string {
	return [...]string{"connecting", "online", "offline", "closed"}[s]
}

// Kind says what an Event carries.
type Kind int

const (
	// Hello: the connection's Role and replica id.
	Hello Kind = iota
	// Snapshot: every op the server holds.
	Snapshot
	// Ops: changes by other people.
	Ops
	// PresenceEvent: another replica's cursor, or Gone.
	PresenceEvent
	// StatusEvent: the connection's Status changed.
	StatusEvent
	// Rejected: the server refused ops you sent.
	Rejected
)

// Event is something that happened on the connection.
type Event struct {
	Kind     Kind
	Role     wire.Role
	Ops      []crdt.Op
	Presence wire.Presence
	Status   Status
	Err      error
	// Waiting, on Hello, is how many ops were made offline.
	Waiting int
}

// Options configures Dial.
type Options struct {
	// BaseURL is where the hub is mounted.
	BaseURL string
	// Doc is the document id.
	Doc string
	// Replica identifies this copy; it must be unique.
	Replica string
	// Header is sent with every request.
	Header http.Header
	// Client is the HTTP client; nil means the default.
	Client *http.Client
	// Retry is the first reconnection delay, doubled each attempt.
	Retry, MaxRetry time.Duration
	// PresenceInterval is the least time between presence sends.
	PresenceInterval time.Duration
	// OnError, if set, hears about connection errors.
	OnError func(error)
	Queue   Queue
}

type Queue interface {
	Load() []crdt.Op
	Save([]crdt.Op)
}

// Conn is a live connection to one document.
type Conn struct {
	opts   Options
	client *http.Client
	events chan Event
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	queue   []Event       // events not yet taken from the channel, in order
	wake    chan struct{} // wakes the pump
	pending []crdt.Op
	offline int
	status  Status
	kick    chan struct{} // wakes the sender
	done    sync.WaitGroup

	presMu      sync.Mutex
	presLatest  *wire.Presence
	presRunning bool
}

// Dial starts connecting and returns at once.
func Dial(opts Options) *Conn {
	if opts.Retry <= 0 {
		opts.Retry = 500 * time.Millisecond
	}
	if opts.MaxRetry <= 0 {
		opts.MaxRetry = 15 * time.Second
	}
	if opts.PresenceInterval == 0 {
		opts.PresenceInterval = 100 * time.Millisecond
	}
	cl := opts.Client
	if cl == nil {
		cl = &http.Client{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{opts: opts, client: cl, events: make(chan Event), ctx: ctx, cancel: cancel, kick: make(chan struct{}, 1), wake: make(chan struct{}, 1)}
	if opts.Queue != nil {
		c.pending = append(c.pending, opts.Queue.Load()...)
		c.offline = len(c.pending)
		if len(c.pending) > 0 {
			c.kick <- struct{}{}
		}
	}
	c.done.Add(3)
	go func() { defer c.done.Done(); c.pump() }()
	go func() { defer c.done.Done(); c.stream() }()
	go func() { defer c.done.Done(); c.sender() }()
	return c
}

// Events delivers what happens, in order.
func (c *Conn) Events() <-chan Event { return c.events }

// Status returns the current connection state.
func (c *Conn) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Conn) takeOffline() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.offline
	c.offline = 0
	return n
}

// Pending returns how many ops wait to be delivered.
func (c *Conn) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func (c *Conn) emit(e Event) {
	c.mu.Lock()
	c.queue = append(c.queue, e)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// unbounded queue: dropping an event would lose operations
func (c *Conn) pump() {
	for {
		c.mu.Lock()
		var e Event
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

func (c *Conn) setStatus(s Status) {
	c.mu.Lock()
	changed := c.status != s && c.status != Closed
	if changed {
		c.status = s
	}
	c.mu.Unlock()
	if changed {
		c.emit(Event{Kind: StatusEvent, Status: s})
	}
}

func (c *Conn) fail(err error) {
	if c.opts.OnError != nil && c.ctx.Err() == nil {
		c.opts.OnError(err)
	}
}

func (c *Conn) url(path string, query url.Values) string {
	u := strings.TrimRight(c.opts.BaseURL, "/") + "/docs/" + url.PathEscape(c.opts.Doc) + "/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *Conn) request(method, u string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.ctx, method, u, r)
	if err != nil {
		return nil, err
	}
	for k, vs := range c.opts.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// Send queues ops for delivery; they are kept until sent.
func (c *Conn) Send(ops []crdt.Op) {
	if len(ops) == 0 {
		return
	}
	c.mu.Lock()
	c.pending = append(c.pending, ops...)
	if c.status != Online {
		c.offline += len(ops)
	}
	c.persistLocked()
	c.mu.Unlock()
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// SetPresence tells the others where this replica's cursor is.
func (c *Conn) SetPresence(p wire.Presence) {
	p.Replica = c.opts.Replica
	c.presMu.Lock()
	c.presLatest = &p
	start := !c.presRunning
	c.presRunning = true
	c.presMu.Unlock()
	if start {
		go c.presenceSender()
	}
}

func (c *Conn) presenceSender() {
	for c.ctx.Err() == nil {
		c.presMu.Lock()
		p := c.presLatest
		c.presLatest = nil
		if p == nil {
			c.presRunning = false
			c.presMu.Unlock()
			return
		}
		c.presMu.Unlock()
		body, _ := json.Marshal(p)
		req, err := c.request(http.MethodPost, c.url("presence", nil), body)
		if err != nil {
			continue
		}
		if resp, err := c.client.Do(req); err == nil {
			_ = resp.Body.Close()
		}
		if c.opts.PresenceInterval > 0 {
			select {
			case <-time.After(c.opts.PresenceInterval):
			case <-c.ctx.Done():
				return
			}
		}
	}
}

// Close stops the connection and closes Events.
func (c *Conn) Close() {
	c.mu.Lock()
	already := c.status == Closed
	c.status = Closed
	c.mu.Unlock()
	if already {
		return
	}
	c.cancel()
	c.done.Wait()
	close(c.events)
}

const maxBatch = 200

func (c *Conn) sender() {
	delay := c.opts.Retry
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.kick:
		}
		for {
			c.mu.Lock()
			batch := append([]crdt.Op(nil), c.pending[:min(len(c.pending), maxBatch)]...)
			c.mu.Unlock()
			if len(batch) == 0 {
				break
			}
			err := c.post(batch)
			var rej *rejection
			switch {
			case err == nil:
				c.dropSent(len(batch))
				delay = c.opts.Retry
			case errors.As(err, &rej):
				c.dropSent(len(batch))
				c.emit(Event{Kind: Rejected, Ops: batch, Err: err})
			default:
				c.fail(err)
				select {
				case <-c.ctx.Done():
					return
				case <-time.After(delay):
				}
				delay = min(delay*2, c.opts.MaxRetry)
			}
		}
	}
}

func (c *Conn) dropSent(n int) {
	c.mu.Lock()
	c.pending = c.pending[min(n, len(c.pending)):]
	c.persistLocked()
	c.mu.Unlock()
}

// c.mu held
func (c *Conn) persistLocked() {
	if c.opts.Queue != nil {
		c.opts.Queue.Save(append([]crdt.Op(nil), c.pending...))
	}
}

type rejection struct{ msg string }

func (r *rejection) Error() string { return r.msg }

func (c *Conn) post(ops []crdt.Op) error {
	body, err := json.Marshal(ops)
	if err != nil {
		return &rejection{err.Error()}
	}
	req, err := c.request(http.MethodPost, c.url("ops", url.Values{"replica": {c.opts.Replica}}), body)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusBadRequest, resp.StatusCode == http.StatusRequestEntityTooLarge:
		var e struct{ Error string }
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		if e.Error == "" {
			e.Error = resp.Status
		}
		return &rejection{e.Error}
	}
	return fmt.Errorf("client: the server said %s", resp.Status)
}

func (c *Conn) stream() {
	delay := c.opts.Retry
	for c.ctx.Err() == nil {
		c.setStatus(Connecting)
		began := time.Now()
		err := c.readEvents()
		if c.ctx.Err() != nil {
			return
		}
		if time.Since(began) > c.opts.MaxRetry {
			delay = c.opts.Retry // it was up for a while: start the backoff over
		}
		c.setStatus(Offline)
		if err != nil {
			c.fail(err)
		}
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, c.opts.MaxRetry)
	}
}

func (c *Conn) readEvents() error {
	req, err := c.request(http.MethodGet, c.url("events", url.Values{"replica": {c.opts.Replica}}), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("client: the server said %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	var name string
	var data []string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if name != "" || len(data) > 0 {
				c.dispatch(name, strings.Join(data, "\n"))
			}
			name, data = "", nil
		case strings.HasPrefix(line, ":"): // a keepalive comment
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("client: the event stream ended")
}

func (c *Conn) dispatch(name, data string) {
	switch name {
	case "hello":
		var h struct{ Role wire.Role }
		if json.Unmarshal([]byte(data), &h) == nil {
			c.setStatus(Online)
			c.emit(Event{Kind: Hello, Role: h.Role, Waiting: c.takeOffline()})
			select {
			case c.kick <- struct{}{}:
			default:
			}
		}
	case "snapshot", "ops":
		var ops []crdt.Op
		if json.Unmarshal([]byte(data), &ops) == nil {
			k := Ops
			if name == "snapshot" {
				k = Snapshot
			}
			c.emit(Event{Kind: k, Ops: ops})
		}
	case "presence":
		var p wire.Presence
		if json.Unmarshal([]byte(data), &p) == nil {
			c.emit(Event{Kind: PresenceEvent, Presence: p})
		}
	}
}
