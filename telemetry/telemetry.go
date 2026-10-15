// Package telemetry is a usage signal for the operator that cannot hold personal data. Nothing in it
// has a field for free text: an Event is a name and a value, both from a closed allowlist in this
// file, and what travels is a Batch of counts per day. There is no identifier, no timestamp finer
// than a day, no address, no document id and no error message, so a person has nothing to be
// identified by; the server side (Receiver) refuses anything outside the allowlist and only
// increments counters.
//
// The client side (Collector) sends nothing unless the person opted in: while consent is off it does
// not even keep a count. It also stays off when the browser says Do Not Track or Global Privacy
// Control. What would be sent is always available as the exact bytes (Collector.Preview), so a
// consent screen can show them before anything leaves.
//
// Adding a name or a value is a reviewed change to the allowlist here (and to docs/telemetry.md, which a
// test keeps in step with it).
package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// Name is what kind of fact a count is about.
type Name string

// The names of the allowlist.
const (
	// Feature counts uses of a part of the app (a side panel opened).
	Feature Name = "feature"
	// Load counts page loads by how long they took, in coarse buckets.
	Load Name = "load"
	// Browser counts page loads by browser family (no version, no user agent).
	Browser Name = "browser"
	// Locale counts page loads by interface language.
	Locale Name = "locale"
	// Error counts failures by a fixed code, never by message.
	Error Name = "error"
	// Crash counts crashes by the part of the page they happened in and their kind: two fixed words, never a
	// message, a stack, a file name or a line. It has its own consent (Collector.SetCrashConsent): a person can
	// send crash codes and no usage counts, or the other way round.
	Crash Name = "crash"
)

// The parts of the page a crash is placed in, and the kinds of crash. A crash code is "<part>_<kind>".
var (
	crashParts = []string{"editor", "files", "save", "collab", "speech", "worker", "page"}
	crashKinds = []string{"panic", "trap", "error", "rejection"}
)

func crashValues() []string {
	var out []string
	for _, p := range crashParts {
		for _, k := range crashKinds {
			out = append(out, p+"_"+k)
		}
	}
	return out
}

// allowed maps each name to its allowed values and the reason it is collected. It is the whole schema.
var allowed = map[Name]struct {
	why    string
	values []string
}{
	Feature: {"which parts of the editor matter, to decide what to improve", []string{
		"format", "styles", "symbols", "keys", "comments", "review", "outline", "check", "images", "find",
		"spell", "language", "stats", "share", "people", "migrate", "page", "save", "transfer", "privacy"}},
	Load:    {"where the app is slow to start", []string{"under_1s", "1_to_3s", "3_to_10s", "over_10s"}},
	Browser: {"which browsers to test on", []string{"chromium", "firefox", "safari", "other"}},
	Locale:  {"which translations matter", []string{"en", "de", "fr", "es", "other"}},
	Error:   {"which failures are common, by fixed code only", []string{"load_failed", "save_failed", "send_failed", "unlock_failed"}},
	Crash:   {"which part of the page crashes and how, by two fixed words (separate opt-in)", crashValues()},
}

var (
	// ErrNotAllowed is returned for a name or value outside the allowlist.
	ErrNotAllowed = errors.New("telemetry: the event is not on the allowlist")
	// ErrNoConsent is returned when consent is asked to turn on while the browser says not to track.
	ErrNoConsent = errors.New("telemetry: the browser asks not to be tracked")
)

// Event is one fact: a name and a value from the allowlist.
type Event struct {
	Name  Name   `json:"name"`
	Value string `json:"value"`
}

// Valid reports whether the event is on the allowlist.
func (e Event) Valid() bool {
	a, ok := allowed[e.Name]
	if !ok {
		return false
	}
	for _, v := range a.values {
		if v == e.Value {
			return true
		}
	}
	return false
}

// Row is one allowlisted fact with the reason it is collected, for the consent screen and the docs.
type Row struct {
	Name   Name
	Why    string
	Values []string
}

// Allowlist is everything that can ever be sent, in a stable order.
func Allowlist() []Row {
	var rows []Row
	for n, a := range allowed {
		rows = append(rows, Row{Name: n, Why: a.why, Values: append([]string(nil), a.values...)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// Count is an event and how many times it happened that day.
type Count struct {
	Name  Name   `json:"name"`
	Value string `json:"value"`
	N     int    `json:"n"`
}

// Batch is what is sent: the day (UTC, no finer) and counts. It is the whole wire format.
type Batch struct {
	Day    string  `json:"day"`
	Counts []Count `json:"counts"`
}

// Limits of a Batch.
const (
	// MaxCount is the largest count one entry may carry, so that one request cannot poison a total.
	MaxCount = 1000
	// MaxBody is the largest request body a Receiver reads.
	MaxBody = 8 << 10
	// DayFormat is how a day is written.
	DayFormat = "2006-01-02"
)

// maxEntries is how many distinct (name, value) pairs exist: a batch cannot have more.
func maxEntries() int {
	n := 0
	for _, a := range allowed {
		n += len(a.values)
	}
	return n
}

// Validate checks a batch against the allowlist and the limits; now is the receiver's clock. The day
// must be yesterday, today or tomorrow in UTC (clocks differ; older or later is refused so a
// batch cannot rewrite history), every entry must be allowed, appear once and have 1..MaxCount.
func (b Batch) Validate(now time.Time) error {
	day, err := time.Parse(DayFormat, b.Day)
	if err != nil || day.Format(DayFormat) != b.Day {
		return errors.New("telemetry: the day is not a date")
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if d := day.Sub(today); d < -24*time.Hour || d > 24*time.Hour {
		return errors.New("telemetry: the day is not today's")
	}
	if len(b.Counts) == 0 || len(b.Counts) > maxEntries() {
		return errors.New("telemetry: the number of counts is out of range")
	}
	seen := map[Event]bool{}
	for _, c := range b.Counts {
		e := Event{c.Name, c.Value}
		if !e.Valid() {
			return ErrNotAllowed
		}
		if seen[e] {
			return errors.New("telemetry: a count is listed twice")
		}
		seen[e] = true
		if c.N < 1 || c.N > MaxCount {
			return errors.New("telemetry: a count is out of range")
		}
	}
	return nil
}

// Decode reads a batch strictly: JSON only, no field beyond the ones of Batch and Count, nothing
// after it. The refusal of an extra field is what keeps a sender from slipping an identifier in.
func Decode(body []byte) (Batch, error) {
	var b Batch
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Batch{}, errors.New("telemetry: not a batch")
	}
	var rest json.RawMessage
	if err := dec.Decode(&rest); err == nil || dec.More() {
		return Batch{}, errors.New("telemetry: data after the batch")
	}
	return b, nil
}
