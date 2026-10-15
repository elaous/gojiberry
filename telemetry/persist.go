package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Persistent totals and noise (roadmap 57). The totals can be kept in a store so that a restart does not
// start the counts again, and a small count can be rounded at random before it is reported. Neither adds
// anything that identifies a visitor: what is stored is exactly what Counters holds (a number per day,
// name and value, the two request totals) plus a random seed for the rounding.

// Store is where a Counters keeps its snapshot: one small value. docstore gives it an Adapter-backed
// implementation. Load reports found=false (and no error) when nothing was saved yet.
type Store interface {
	Load(ctx context.Context) (data []byte, found bool, err error)
	Save(ctx context.Context, data []byte) error
}

// Limits of a snapshot, so a damaged or tampered store cannot make the server hold more than the
// counters could ever have reached.
const (
	// MaxSnapshot is the largest snapshot Restore reads.
	MaxSnapshot = 1 << 20
	// MaxTotal is the largest total one entry may hold.
	MaxTotal = 1 << 40
	// snapshotVersion is the version of the snapshot format.
	snapshotVersion = 1
	// MaxNoiseStep is the largest rounding step.
	MaxNoiseStep = 1000
)

type snapshot struct {
	Version  int                `json:"v"`
	Seed     string             `json:"seed,omitempty"`
	Accepted int                `json:"accepted"`
	Refused  int                `json:"refused"`
	Days     map[string][]Total `json:"days"`
}

// Total is one total of a snapshot: an event and its sum over a day.
type Total struct {
	Name  Name   `json:"name"`
	Value string `json:"value"`
	N     int    `json:"n"`
}

// Snapshot is the totals as bytes for a Store. It holds no more than the counters do: days, names,
// values, sums, the two request totals and the seed of the noise.
func (c *Counters) Snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := snapshot{Version: snapshotVersion, Seed: hex.EncodeToString(c.seedLocked()), Accepted: c.accepted, Refused: c.refused, Days: map[string][]Total{}}
	for d, m := range c.days {
		for e, n := range m {
			s.Days[d] = append(s.Days[d], Total{e.Name, e.Value, n})
		}
	}
	out, _ := json.Marshal(s)
	return out
}

// decoded is a snapshot read and checked: the totals, the request totals and the seed.
type decoded struct {
	days     map[string]map[Event]int
	accepted int
	refused  int
	seed     []byte
}

// decodeSnapshot reads a snapshot strictly: an unknown field, a name or value outside the allowlist, a day that
// is not a date, a day older than the retention (dropped) or a total out of range refuses the whole snapshot.
func decodeSnapshot(data []byte, now time.Time) (*decoded, error) {
	if len(data) > MaxSnapshot {
		return nil, errors.New("telemetry: the snapshot is too large")
	}
	var s snapshot
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil || dec.More() {
		return nil, errors.New("telemetry: not a snapshot")
	}
	if s.Version != snapshotVersion {
		return nil, errors.New("telemetry: a snapshot of another version")
	}
	if s.Accepted < 0 || s.Refused < 0 || s.Accepted > MaxTotal || s.Refused > MaxTotal {
		return nil, errors.New("telemetry: a request total is out of range")
	}
	var seed []byte
	if s.Seed != "" {
		b, err := hex.DecodeString(s.Seed)
		if err != nil || len(b) != seedLen {
			return nil, errors.New("telemetry: the seed is not one")
		}
		seed = b
	}
	cutoff := now.UTC().AddDate(0, 0, -RetentionDays).Format(DayFormat)
	days := map[string]map[Event]int{}
	for d, list := range s.Days {
		if t, err := time.Parse(DayFormat, d); err != nil || t.Format(DayFormat) != d {
			return nil, errors.New("telemetry: a day is not a date")
		}
		if len(list) > maxEntries() {
			return nil, errors.New("telemetry: too many totals for a day")
		}
		m := map[Event]int{}
		for _, t := range list {
			e := Event{t.Name, t.Value}
			if !e.Valid() {
				return nil, ErrNotAllowed
			}
			if _, dup := m[e]; dup || t.N < 1 || t.N > MaxTotal {
				return nil, errors.New("telemetry: a total is listed twice or out of range")
			}
			m[e] = t.N
		}
		if d >= cutoff { // older days are dropped, as Add does
			days[d] = m
		}
	}
	return &decoded{days: days, accepted: s.Accepted, refused: s.Refused, seed: seed}, nil
}

// Restore replaces the totals by a snapshot. It reads strictly (see decodeSnapshot): a snapshot with anything
// outside the schema is refused whole and changes nothing, so what is in the store can never put anything into
// the counters that a request could not.
func (c *Counters) Restore(data []byte, now time.Time) error {
	d, err := decodeSnapshot(data, now)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.days, c.accepted, c.refused = d.days, d.accepted, d.refused
	if d.seed != nil {
		c.seed = d.seed
	}
	return nil
}

// Persist loads the totals from st and, from then on, saves them after a change, at most once per delay
// (a burst of batches is one write). It returns the load error; on one the counters carry on in memory,
// and a snapshot that cannot be read is replaced at the next save rather than trusted. Several
// processes sharing one store each save their own totals (the last write wins): run one counting
// process, or give each its own store.
func (c *Counters) Persist(ctx context.Context, st Store, delay time.Duration, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	if delay <= 0 {
		delay = 5 * time.Second
	}
	var loadErr error
	if data, found, err := st.Load(ctx); err != nil {
		loadErr = err
	} else if found {
		loadErr = c.Restore(data, now())
	}
	var mu sync.Mutex
	var timer *time.Timer
	c.mu.Lock()
	c.onChange = func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			return
		}
		timer = time.AfterFunc(delay, func() {
			mu.Lock()
			timer = nil
			mu.Unlock()
			err := st.Save(context.WithoutCancel(ctx), c.Snapshot())
			c.mu.Lock()
			c.saveErr = err
			c.mu.Unlock()
		})
	}
	c.mu.Unlock()
	return loadErr
}

// SaveError is the error of the last save, nil if it worked or none ran.
func (c *Counters) SaveError() error { c.mu.Lock(); defer c.mu.Unlock(); return c.saveErr }

const seedLen = 16

// seedLocked is the seed of the noise, made on first use. The caller holds c.mu.
func (c *Counters) seedLocked() []byte {
	if c.seed == nil {
		b := make([]byte, seedLen)
		_, _ = rand.Read(b)
		c.seed = b
	}
	return c.seed
}

// SetNoise makes Report round every total at random to a multiple of step (0 or 1: off). A total n
// becomes the multiple of step just below it or just above it, above with probability (n mod step)/step,
// so the reported number is right on average and a small total is a coarse figure that cannot point at
// one visit. The draw for a given day, event and total comes from a keyed hash of them with a secret seed
// that is kept with the totals, so asking again gives the same answer and the noise cannot be averaged
// away by asking often. This is differential-privacy-style rounding, not a proven (epsilon, delta)
// guarantee: it protects small counts in a report, and it does not make the raw totals in the store
// (which only the operator can reach) any less exact.
func (c *Counters) SetNoise(step int) error {
	if step < 0 || step > MaxNoiseStep {
		return errors.New("telemetry: the noise step is out of range")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if step <= 1 {
		step = 0
	}
	c.noise = step
	return nil
}

// Noise is the rounding step (0 when off).
func (c *Counters) Noise() int { c.mu.Lock(); defer c.mu.Unlock(); return c.noise }

// ParseNoise reads the operator's setting: "" or "off" or "0" (none), or a whole step from 2 to MaxNoiseStep.
func ParseNoise(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "off" || s == "0" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 2 || n > MaxNoiseStep {
		return 0, errors.New("telemetry: the noise setting is off or a whole number from 2 to " + strconv.Itoa(MaxNoiseStep))
	}
	return n, nil
}

// noisyLocked is n after the rounding (n itself when noise is off). The caller holds c.mu.
func (c *Counters) noisyLocked(day string, e Event, n int) int {
	step := c.noise
	if step <= 1 {
		return n
	}
	mac := hmac.New(sha256.New, c.seedLocked())
	mac.Write([]byte(day + "\x00" + string(e.Name) + "\x00" + e.Value + "\x00" + strconv.Itoa(n)))
	u := float64(binary.BigEndian.Uint64(mac.Sum(nil)[:8])>>11) / (1 << 53) // uniform in [0,1)
	q, r := n/step, n%step
	if r > 0 && u < float64(r)/float64(step) {
		q++
	}
	return q * step
}
