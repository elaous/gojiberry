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

type Counters struct {
	mu       sync.Mutex
	days     map[string]map[Event]int
	accepted int
	refused  int
	seed     []byte
	noise    int
	onChange func()
	saveErr  error
	shared   SharedStore
	base     *decoded
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
	if c.onChange != nil {
		c.onChange()
	}
	cutoff := now.UTC().AddDate(0, 0, -RetentionDays).Format(DayFormat)
	for d := range c.days {
		if d < cutoff {
			delete(c.days, d)
		}
	}
}

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

func (c *Counters) refuse() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refused++
	if c.onChange != nil {
		c.onChange()
	}
}

// Reported is one reported total.
type Reported struct {
	Day   string `json:"day"`
	Name  Name   `json:"name"`
	Value string `json:"value"`
	N     int    `json:"n"`
}

// k applies to the rounded number: a rare value points at none
func (c *Counters) Report(k int) []Reported {
	if k < 1 {
		k = DefaultK
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Reported
	for d, m := range c.days {
		for e, n := range m {
			n = c.noisyLocked(d, e, n)
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

// reads no header: no cookie, token or user agent
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

// the receiver never reads the address
func (r *Receiver) WithLimit(allow func(*http.Request) (bool, time.Duration)) *Receiver {
	r.allow = allow
	return r
}

func (r *Receiver) WithAudit(l *audit.Logger) *Receiver { r.log = l; return r }

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
