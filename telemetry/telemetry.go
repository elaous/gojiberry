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
	Feature Name = "feature"
	Load    Name = "load"
	Browser Name = "browser"
	// Locale counts page loads by interface language.
	Locale Name = "locale"
	// Error counts failures by a fixed code, never by message.
	Error Name = "error"
	// never a message, a stack, a file name or a line
	Crash Name = "crash"
)

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
	ErrNotAllowed = errors.New("telemetry: the event is not on the allowlist")
	ErrNoConsent  = errors.New("telemetry: the browser asks not to be tracked")
)

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

type Row struct {
	Name   Name
	Why    string
	Values []string
}

func Allowlist() []Row {
	var rows []Row
	for n, a := range allowed {
		rows = append(rows, Row{Name: n, Why: a.why, Values: append([]string(nil), a.values...)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

type Count struct {
	Name  Name   `json:"name"`
	Value string `json:"value"`
	N     int    `json:"n"`
}

type Batch struct {
	Day    string  `json:"day"`
	Counts []Count `json:"counts"`
}

// Limits of a Batch.
const (
	// one request cannot poison a total
	MaxCount = 1000
	// MaxBody is the largest request body a Receiver reads.
	MaxBody = 8 << 10
	// DayFormat is how a day is written.
	DayFormat = "2006-01-02"
)

func maxEntries() int {
	n := 0
	for _, a := range allowed {
		n += len(a.values)
	}
	return n
}

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

// strict: an extra field could smuggle in an identifier
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
