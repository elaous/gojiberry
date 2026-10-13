package telemetry

import (
	"encoding/json"
)

// The local debug log (roadmap 57, step 5). It is off by default and, when the person turns it on, it
// keeps what the collector would send, or did send, in memory in this page and nowhere else: it is never
// sent, never written to a server, and it is dropped when consent is turned off or the page closes. The
// person can download it to look at it or to attach it to a report. An entry holds only a Batch's worth:
// a time to the minute, an outcome word and the allowlisted counts, so the log can say no more than the
// request would have.

// Outcomes of a log entry.
const (
	// OutcomeSent: the server accepted the batch.
	OutcomeSent = "sent"
	// OutcomeFailed: sending failed; the counts are kept for the next try.
	OutcomeFailed = "failed"
	// OutcomeNotSampled: this visit is not in the sample, so the batch was not sent.
	OutcomeNotSampled = "not-sampled"
	// OutcomeNoServer: no server is confirmed, so the batch was not sent.
	OutcomeNoServer = "no-server"
)

// MaxLog is how many entries the log keeps; the oldest go first.
const MaxLog = 100

// LogEntry is one line of the local log.
type LogEntry struct {
	// Time is when, in UTC to the minute (the log never leaves the page; this is for the person reading it).
	Time    string  `json:"time"`
	Outcome string  `json:"outcome"`
	Day     string  `json:"day"`
	Counts  []Count `json:"counts"`
}

// SetLog turns the local log on or off. Turning it off forgets it.
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

// LogJSON is the log as a file the person can download: an object with a short note and the entries.
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

// Withhold records, in the log only, that the pending counts were not sent for the given outcome (for
// example OutcomeNoServer), and drops them: they will not be sent later under another reason. It does
// nothing when the log is off, so counts that wait for a server to be confirmed are kept then.
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

// logLocked adds an entry when the log is on. The caller holds c.mu.
func (c *Collector) logLocked(outcome string, b Batch) {
	if !c.logOn {
		return
	}
	c.log = append(c.log, LogEntry{Time: c.now().UTC().Format("2006-01-02T15:04Z"), Outcome: outcome, Day: b.Day, Counts: append([]Count(nil), b.Counts...)})
	if len(c.log) > MaxLog {
		c.log = append([]LogEntry(nil), c.log[len(c.log)-MaxLog:]...)
	}
}
