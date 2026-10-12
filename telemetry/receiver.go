package telemetry

import (
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/elaous/gojiberry/audit"
)

// RetentionDays is how many days of counts a Counters keeps.
const RetentionDays = 90

// DefaultK is the smallest count Report shows (see Counters.Report).
const DefaultK = 5

// Counters are the totals the server keeps: a number per day, name and value, and nothing else. It
// is safe for concurrent use. Counts older than RetentionDays are dropped on write.
type Counters struct {
	mu       sync.Mutex
	days     map[string]map[Event]int
	accepted int
	refused  int
}

// NewCounters is an empty set of totals.
func NewCounters() *Counters { return &Counters{days: map[string]map[Event]int{}} }

// Add increments the totals by a validated batch.
func (c *Counters) Add(b Batch, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m := c.days[b.Day]
	if m == nil {
		m = map[Event]int{}
		c.days[b.Day] = m
	}
	for _, k := range b.Counts {
		m[Event{k.Name, k.Value}] += k.N
	}
	c.accepted++
	cutoff := now.UTC().AddDate(0, 0, -RetentionDays).Format(DayFormat)
	for d := range c.days {
		if d < cutoff {
			delete(c.days, d)
		}
	}
}

// Totals is how many batches were taken and how many requests were refused. It counts requests,
// not people, which is why /admin/usage can show it without the k threshold.
type Totals struct {
	Accepted int `json:"accepted"`
	Refused  int `json:"refused"`
}

// Totals reports the request totals.
func (c *Counters) Totals() Totals {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Totals{c.accepted, c.refused}
}

func (c *Counters) refuse() { c.mu.Lock(); c.refused++; c.mu.Unlock() }

// Reported is one reported total.
type Reported struct {
	Day   string `json:"day"`
	Name  Name   `json:"name"`
	Value string `json:"value"`
	N     int    `json:"n"`
}

// Report lists the totals per day with a count of at least k (DefaultK if k < 1). A smaller total
// is left out, so a rare value cannot point at a single visitor; the list is in day order.
func (c *Counters) Report(k int) []Reported {
	if k < 1 {
		k = DefaultK
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Reported
	for d, m := range c.days {
		for e, n := range m {
			if n >= k {
				out = append(out, Reported{d, e.Name, e.Value, n})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Value < b.Value
	})
	return out
}

// Receiver is the server side: an http.Handler that takes POSTed batches and increments counters. It
// reads no header (no cookie, no token, no user agent), never reads or stores the source address
// itself, and answers with a status only. A request with anything outside the allowlist is
// refused whole.
type Receiver struct {
	counters *Counters
	now      func() time.Time
	allow    func(*http.Request) (bool, time.Duration)
	log      *audit.Logger
	hook     func(Batch)
}

// NewReceiver takes batches into counters.
func NewReceiver(c *Counters) *Receiver { return &Receiver{counters: c, now: time.Now} }

// WithClock sets the clock (for tests).
func (r *Receiver) WithClock(now func() time.Time) *Receiver { r.now = now; return r }

// WithLimit sets a rate limit; allow is asked for each request and chooses the budget, which is
// the caller's business (the receiver itself never reads the address).
func (r *Receiver) WithLimit(allow func(*http.Request) (bool, time.Duration)) *Receiver {
	r.allow = allow
	return r
}

// WithAudit logs a refused request as an audit event (no content, no address).
func (r *Receiver) WithAudit(l *audit.Logger) *Receiver { r.log = l; return r }

// WithBatchHook calls hook with every accepted batch after its counts are added (for example to route
// them to the sinks of package observe). The batch has been validated against the allowlist; the
// hook gets nothing of the request, only the batch.
func (r *Receiver) WithBatchHook(hook func(Batch)) *Receiver { r.hook = hook; return r }

// Counters are the totals this receiver increments.
func (r *Receiver) Counters() *Counters { return r.counters }

// ServeHTTP implements http.Handler.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.allow != nil {
		if ok, wait := r.allow(req); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, MaxBody+1))
	if err != nil || len(body) > MaxBody {
		r.refuse(w, http.StatusRequestEntityTooLarge)
		return
	}
	b, err := Decode(body)
	if err == nil {
		err = b.Validate(r.now())
	}
	if err != nil {
		r.refuse(w, http.StatusBadRequest)
		return
	}
	r.counters.Add(b, r.now())
	if r.hook != nil {
		r.hook(b)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Receiver) refuse(w http.ResponseWriter, status int) {
	r.counters.refuse()
	if r.log != nil {
		_ = r.log.Log(audit.Event{Kind: audit.TelemetryRefused, Outcome: audit.Denied, Actor: audit.Anonymous, Reason: audit.NotAllowed})
	}
	http.Error(w, "refused", status)
}
