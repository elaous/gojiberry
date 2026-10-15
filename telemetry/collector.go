package telemetry

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

// Collector is the client side. It starts with consent off, and while it is off Record keeps nothing
// and Flush sends nothing: there is no queue to leak later.
type Collector struct {
	mu      sync.Mutex
	on      bool
	crashOn bool // crash codes are sent: a consent of their own
	blocked bool
	counts  map[Event]int
	now     func() time.Time
	sent    int
	// sampling: a visit is in the sample with probability 1/oneIn, decided once when consent is given
	// (and again when the rate changes), so a visit is sent whole or not at all.
	oneIn   int
	sampled bool
	rnd     func(n int) int
	// the local log (log.go) of what would be, or was, sent
	logOn bool
	log   []LogEntry
}

// NewCollector is a Collector with consent off. now is the clock (time.Now if nil).
func NewCollector(now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{counts: map[Event]int{}, now: now, oneIn: 1, sampled: true, rnd: rand.IntN}
}

// SetSampling sends from about one visit in oneIn (1 or less: every visit). The draw is made now and
// again each time consent is turned on; rnd(n) returns a number in [0, n) (math/rand if nil). A visit
// that is not in the sample records and sends nothing, exactly as if consent were off, except that
// with the local log on it still counts, for the log only (see SetLog).
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

// Sampled reports whether this visit is in the sample (always true at one in one).
func (c *Collector) Sampled() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.sampled }

// SampleOneIn is the sampling rate as set: 1 means every visit.
func (c *Collector) SampleOneIn() int { c.mu.Lock(); defer c.mu.Unlock(); return c.oneIn }

// BrowserSignals tells the collector what the browser asks for. With Do Not Track or Global Privacy
// Control set, consent is turned off and cannot be turned on.
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

// SetConsent turns sending on or off. Turning it off drops everything not yet sent. It returns
// ErrNoConsent when the browser asks not to be tracked.
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
		c.dropLocked(false) // the crash codes stay if they were agreed to on their own
	}
	return nil
}

// dropLocked forgets the counts held: those of crashes too when crashes is true. With neither consent left, the
// log goes as well.
func (c *Collector) dropLocked(crashes bool) {
	for e := range c.counts {
		if crashes || e.Name != Crash {
			delete(c.counts, e)
		}
	}
	if !c.on && !c.crashOn {
		c.log = nil // nothing is kept about a person who chose to send nothing
	}
}

// SetCrashConsent turns the sending of crash codes on or off, apart from the usage counts: the two are chosen
// separately. Turning it off drops the crash codes not yet sent. It returns ErrNoConsent when the browser asks
// not to be tracked.
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
	if (!c.on || e.Name == Crash) && (!c.crashOn || e.Name != Crash) || (!c.sampled && !c.logOn) {
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
	if !c.sampled {
		return Batch{}, false // not in the sample: nothing would be sent
	}
	return c.pendingLocked()
}

// pendingLocked is the batch of the counts held, whether or not this visit is in the sample.
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
	c.mu.Lock()
	if !c.sampled { // not in the sample: nothing is sent; with the log on, say what would have been
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
