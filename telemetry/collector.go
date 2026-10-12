package telemetry

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// Collector is the client side. It starts with consent off, and while it is off Record keeps nothing
// and Flush sends nothing: there is no queue to leak later.
type Collector struct {
	mu      sync.Mutex
	on      bool
	blocked bool
	counts  map[Event]int
	now     func() time.Time
	sent    int
}

// NewCollector is a Collector with consent off. now is the clock (time.Now if nil).
func NewCollector(now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{counts: map[Event]int{}, now: now}
}

// BrowserSignals tells the collector what the browser asks for. With Do Not Track or Global Privacy
// Control set, consent is turned off and cannot be turned on.
func (c *Collector) BrowserSignals(doNotTrack, globalPrivacyControl bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = doNotTrack || globalPrivacyControl
	if c.blocked {
		c.on = false
		clear(c.counts)
	}
}

// Blocked reports whether a browser signal keeps telemetry off.
func (c *Collector) Blocked() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.blocked }

// SetConsent turns sending on or off. Turning it off drops everything not yet sent. It returns
// ErrNoConsent when the browser asks not to be tracked.
func (c *Collector) SetConsent(on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on && c.blocked {
		return ErrNoConsent
	}
	c.on = on
	if !on {
		clear(c.counts)
	}
	return nil
}

// Consent reports whether the person opted in.
func (c *Collector) Consent() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.on }

// Record counts an event. An event outside the allowlist is refused with ErrNotAllowed whether or not
// consent is on (a call site that sends one is a bug to see); with consent off a valid event is
// dropped without a trace.
func (c *Collector) Record(name Name, value string) error {
	e := Event{name, value}
	if !e.Valid() {
		return ErrNotAllowed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on {
		return nil
	}
	if c.counts[e] < MaxCount {
		c.counts[e]++
	}
	return nil
}

// Batch is what would be sent now (ok is false when there is nothing).
func (c *Collector) Batch() (b Batch, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.on || len(c.counts) == 0 {
		return Batch{}, false
	}
	b.Day = c.now().UTC().Format(DayFormat)
	for e, n := range c.counts {
		b.Counts = append(b.Counts, Count{e.Name, e.Value, n})
	}
	sort.Slice(b.Counts, func(i, j int) bool {
		if b.Counts[i].Name != b.Counts[j].Name {
			return b.Counts[i].Name < b.Counts[j].Name
		}
		return b.Counts[i].Value < b.Counts[j].Value
	})
	return b, true
}

// Preview is the pending batch indented for the person to read, or nil when there is nothing to
// send. It decodes to exactly the batch Body sends.
func (c *Collector) Preview() []byte {
	b, ok := c.Batch()
	if !ok {
		return nil
	}
	out, _ := json.MarshalIndent(b, "", "  ")
	return out
}

// Body is the request body Flush sends (nil when there is nothing).
func (c *Collector) Body() []byte {
	b, ok := c.Batch()
	if !ok {
		return nil
	}
	out, _ := json.Marshal(b)
	return out
}

// Sent is how many batches were accepted by the send function.
func (c *Collector) Sent() int { c.mu.Lock(); defer c.mu.Unlock(); return c.sent }

// Flush sends the pending counts with send (one call, a POST body) and returns how many events it
// covered. It does nothing, and does not call send, when consent is off or nothing is pending.
// The counts are cleared only when send succeeds; a failure keeps them for the next try. Counts
// recorded while send ran are kept.
func (c *Collector) Flush(ctx context.Context, send func(ctx context.Context, body []byte) error) (int, error) {
	b, ok := c.Batch()
	if !ok {
		return 0, nil
	}
	body, _ := json.Marshal(b)
	if err := send(ctx, body); err != nil {
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, k := range b.Counts {
		e := Event{k.Name, k.Value}
		c.counts[e] -= k.N
		if c.counts[e] <= 0 {
			delete(c.counts, e)
		}
		n += k.N
	}
	c.sent++
	return n, nil
}
