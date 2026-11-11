package telemetry

import (
	"encoding/json"
)

// Outcomes of a log entry.
const (
	// OutcomeSent: the server accepted the batch.
	OutcomeSent       = "sent"
	OutcomeFailed     = "failed"
	OutcomeNotSampled = "not-sampled"
	OutcomeNoServer   = "no-server"
)

const MaxLog = 100

// LogEntry is one line of the local log.
type LogEntry struct {
	Time    string  `json:"time"`
	Outcome string  `json:"outcome"`
	Day     string  `json:"day"`
	Counts  []Count `json:"counts"`
}

// SetLog turns the local log on or off.
func (c *Collector) SetLog(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logOn = on
	if !on {
		c.log = nil
	}
}

// LogOn reports whether the local log is on.
func (c *Collector) LogOn() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.logOn }

// Log is a copy of the entries, oldest first.
func (c *Collector) Log() []LogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LogEntry, len(c.log))
	for i, e := range c.log {
		e.Counts = append([]Count(nil), e.Counts...)
		out[i] = e
	}
	return out
}

func (c *Collector) LogJSON() []byte {
	out, _ := json.MarshalIndent(struct {
		Note    string     `json:"note"`
		Entries []LogEntry `json:"entries"`
	}{
		Note:    "A local log of the anonymous usage counts this page would send or sent. It was kept in this browser only and was never sent anywhere.",
		Entries: c.Log(),
	}, "", "  ")
	return out
}

func (c *Collector) Withhold(outcome string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.withholdLocked(outcome)
}

func (c *Collector) withholdLocked(outcome string) {
	if !c.logOn {
		return
	}
	if b, ok := c.pendingLocked(); ok {
		c.logLocked(outcome, b)
	}
	clear(c.counts)
}

// caller holds c.mu
func (c *Collector) logLocked(outcome string, b Batch) {
	if !c.logOn {
		return
	}
	c.log = append(c.log, LogEntry{Time: c.now().UTC().Format("2006-01-02T15:04Z"), Outcome: outcome, Day: b.Day, Counts: append([]Count(nil), b.Counts...)})
	if len(c.log) > MaxLog {
		c.log = append([]LogEntry(nil), c.log[len(c.log)-MaxLog:]...)
	}
}
