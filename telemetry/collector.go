package telemetry

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

// Collector is the client side.
type Collector struct {
	mu      sync.Mutex
	on      bool
	crashOn bool // crash codes are sent: a consent of their own
	blocked bool
	counts  map[Event]int
	now     func() time.Time
	sent    int
	oneIn   int
	sampled bool
	rnd     func(n int) int
	logOn   bool
	log     []LogEntry
}

// NewCollector is a Collector with consent off.
func NewCollector(now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{counts: map[Event]int{}, now: now, oneIn: 1, sampled: true, rnd: rand.IntN}
}

func (c *Collector) SetSampling(oneIn int, rnd func(n int) int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if oneIn < 1 {
		oneIn = 1
	}
	if rnd != nil {
		c.rnd = rnd
	}
	c.oneIn = oneIn
	c.drawLocked()
}

func (c *Collector) drawLocked() { c.sampled = c.oneIn <= 1 || c.rnd(c.oneIn) == 0 }

func (c *Collector) Sampled() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.sampled }

func (c *Collector) SampleOneIn() int { c.mu.Lock(); defer c.mu.Unlock(); return c.oneIn }

// BrowserSignals tells the collector what the browser asks for.
func (c *Collector) BrowserSignals(doNotTrack, globalPrivacyControl bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = doNotTrack || globalPrivacyControl
	if c.blocked {
		c.on, c.crashOn = false, false
		clear(c.counts)
	}
}

// Blocked reports whether a browser signal keeps telemetry off.
func (c *Collector) Blocked() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.blocked }

// SetConsent turns sending on or off.
func (c *Collector) SetConsent(on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on && c.blocked {
		return ErrNoConsent
	}
	if on && !c.on {
		c.drawLocked() // a new opt-in is a new draw
	}
	c.on = on
	if !on {
		c.dropLocked(false)
	}
	return nil
}

func (c *Collector) dropLocked(crashes bool) {
	for e := range c.counts {
		if crashes || e.Name != Crash {
			delete(c.counts, e)
		}
	}
	if !c.on && !c.crashOn {
		c.log = nil
	}
}

func (c *Collector) SetCrashConsent(on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on && c.blocked {
		return ErrNoConsent
	}
	if on && !c.crashOn && !c.on {
		c.drawLocked()
	}
	c.crashOn = on
	if !on {
		c.dropLocked(true)
	}
	return nil
}

// CrashConsent reports whether the person agreed to send crash codes.
func (c *Collector) CrashConsent() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.crashOn }

// Consent reports whether the person opted in.
func (c *Collector) Consent() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.on }

// Record counts an event.
func (c *Collector) Record(name Name, value string) error {
	e := Event{name, value}
	if !e.Valid() {
		return ErrNotAllowed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if (!c.on || e.Name == Crash) && (!c.crashOn || e.Name != Crash) || (!c.sampled && !c.logOn) {
		return nil
	}
	if c.counts[e] < MaxCount {
		c.counts[e]++
	}
	return nil
}

func (c *Collector) Batch() (b Batch, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.sampled {
		return Batch{}, false // not in the sample: nothing would be sent
	}
	return c.pendingLocked()
}

func (c *Collector) pendingLocked() (b Batch, ok bool) {
	if (!c.on && !c.crashOn) || len(c.counts) == 0 {
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

func (c *Collector) Preview() []byte {
	b, ok := c.Batch()
	if !ok {
		return nil
	}
	out, _ := json.MarshalIndent(b, "", "  ")
	return out
}

func (c *Collector) Body() []byte {
	b, ok := c.Batch()
	if !ok {
		return nil
	}
	out, _ := json.Marshal(b)
	return out
}

func (c *Collector) Sent() int { c.mu.Lock(); defer c.mu.Unlock(); return c.sent }

func (c *Collector) Flush(ctx context.Context, send func(ctx context.Context, body []byte) error) (int, error) {
	c.mu.Lock()
	if !c.sampled {
		c.withholdLocked(OutcomeNotSampled)
		c.mu.Unlock()
		return 0, nil
	}
	c.mu.Unlock()
	b, ok := c.Batch()
	if !ok {
		return 0, nil
	}
	body, _ := json.Marshal(b)
	if err := send(ctx, body); err != nil {
		c.mu.Lock()
		c.logLocked(OutcomeFailed, b)
		c.mu.Unlock()
		return 0, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logLocked(OutcomeSent, b)
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
