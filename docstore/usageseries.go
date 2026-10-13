package docstore

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/observe"
	"html/template"
	"log"
	"net/http"
	"sync"
	"time"
)

// Usage over time.
//
// The counters of GET /admin/usage (documents, records, bytes, the largest document, the backend's
// quota in use, and the batches the telemetry receiver took and refused) are sampled into a short
// series so that an operator can see a trend, not only the present. The series is kept by these rules:
//
//   - aggregate only: every number is a count or a size of the whole store, never about a document,
//     a person, an address or a session. A sample has no identifier of any kind;
//   - fixed small cardinality: at most UsageHourlyPoints hourly points and UsageDailyPoints daily
//     points, each with the same seven numbers; an older point is dropped as a newer one comes. The
//     series can never grow with the number of documents or people;
//   - retention: 48 hours at hour resolution, 90 days at day resolution. Nothing older is kept;
//   - kept across restarts: the series is one small record in the store (UsageSeriesKey, written through the
//     same Adapter as the documents, at most once every ten minutes and whenever a new hour begins). It
//     holds exactly the points above and nothing else, is read strictly (a field, time or number outside
//     the rules refuses the whole record and the series starts again, the gap showing as missing points,
//     never as made-up ones), and is never written to a log. A point is the last sample taken in its hour
//     (or day), so the totals read at the end of the period. Several server processes on one store each
//     write their own series and the last write wins.
//
// Samples are taken when the operator reads the usage history routes and, with RunUsageSampling, every hour.

// The retention of the series.
const (
	UsageHourlyPoints = 48 // two days of hours
	UsageDailyPoints  = 90 // about three months of days
)

// UsageSeriesKey is where the series is kept: a reserved key ("usage:" is not reachable through /docs and is
// not a document, so it never expires by itself).
const UsageSeriesKey = "usage:series"

// usageKeyspace is the reserved keyspace of the persisted series.
const usageKeyspace = "usage:"

// usageSaveEvery is the longest a changed point waits to be written, unless a new hour begins (then at once).
const usageSaveEvery = 10 * time.Minute

// UsagePoint is one sample of the counters, at the start (UTC) of its hour or day.
type UsagePoint struct {
	Time      time.Time `json:"time"`
	Documents int       `json:"documents"`
	Records   int       `json:"records"`
	Bytes     int64     `json:"bytes"`
	Largest   int       `json:"largest"`
	// QuotaUsed is the backend's quota in use; 0 when the backend does not report one.
	QuotaUsed int64 `json:"quotaUsed"`
	// Accepted and Refused are the usage batches the telemetry receiver has taken and refused up to
	// this point (running totals; 0 when it is off).
	Accepted int `json:"accepted"`
	Refused  int `json:"refused"`
}

// usageSeries is the bounded history. The zero value is ready to use.
type usageSeries struct {
	mu        sync.Mutex
	hourly    []UsagePoint // oldest first, at most UsageHourlyPoints
	daily     []UsagePoint // oldest first, at most UsageDailyPoints
	lastSaved time.Time
}

// persisted is the record in the store: the two lists and a version.
type persistedUsage struct {
	Version int          `json:"v"`
	Hourly  []UsagePoint `json:"hourly"`
	Daily   []UsagePoint `json:"daily"`
}

// encode is the series as the record to store.
func (u *usageSeries) encode() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	out, _ := json.Marshal(persistedUsage{Version: 1, Hourly: u.hourly, Daily: u.daily})
	return out
}

// decodeUsage reads a stored series strictly. It refuses the whole record for an unknown field, a list longer
// than the retention, a time that is not the start of its hour (day), points out of order or more than a day
// ahead of now, or a negative number.
func decodeUsage(data []byte, now time.Time) (persistedUsage, error) {
	var p persistedUsage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || dec.More() {
		return p, errors.New("docstore: the usage series is not a series")
	}
	if p.Version != 1 || len(p.Hourly) > UsageHourlyPoints || len(p.Daily) > UsageDailyPoints {
		return p, errors.New("docstore: the usage series is of another shape")
	}
	limit := now.UTC().Add(24 * time.Hour)
	check := func(list []UsagePoint, step func(time.Time) time.Time) error {
		var prev time.Time
		for _, pt := range list {
			t := pt.Time
			if !t.Equal(step(t.UTC())) || (!prev.IsZero() && !t.After(prev)) || t.After(limit) {
				return errors.New("docstore: a usage point is out of order or not on its hour or day")
			}
			if pt.Documents < 0 || pt.Records < 0 || pt.Bytes < 0 || pt.Largest < 0 || pt.QuotaUsed < 0 || pt.Accepted < 0 || pt.Refused < 0 {
				return errors.New("docstore: a usage point has a negative number")
			}
			prev = t
		}
		return nil
	}
	if err := check(p.Hourly, func(t time.Time) time.Time { return t.Truncate(time.Hour) }); err != nil {
		return p, err
	}
	if err := check(p.Daily, func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }); err != nil {
		return p, err
	}
	return p, nil
}

// loadUsageSeries reads the stored series into s.usage, once. A record that is missing, unreadable or not
// valid leaves the series empty (it is replaced at the next save); nothing is made up.
func (s *Server) loadUsageSeries(ctx context.Context, now time.Time) {
	s.usageOnce.Do(func() {
		doc, err := s.adapter.Load(ctx, UsageSeriesKey)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				log.Printf("docstore: the usage history could not be read from the store (%v); starting it again", err)
			}
			return
		}
		p, err := decodeUsage(doc.Data, now)
		if err != nil {
			log.Printf("docstore: the stored usage history was refused (%v); starting it again", err)
			return
		}
		s.usage.mu.Lock()
		s.usage.hourly, s.usage.daily, s.usage.lastSaved = p.Hourly, p.Daily, now
		s.usage.mu.Unlock()
	})
}

// record stores p taken at now: it replaces the point of the same hour (day) or appends a new one, and
// drops what is past the retention.
// It reports whether the point opens a new hour (the moment worth saving at once).
func (u *usageSeries) record(now time.Time, p UsagePoint) (newHour bool) {
	now = now.UTC()
	u.mu.Lock()
	defer u.mu.Unlock()
	h := p
	h.Time = now.Truncate(time.Hour)
	newHour = len(u.hourly) == 0 || h.Time.After(u.hourly[len(u.hourly)-1].Time)
	u.hourly = putPoint(u.hourly, h, UsageHourlyPoints)
	d := p
	d.Time = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	u.daily = putPoint(u.daily, d, UsageDailyPoints)
	return newHour
}

func putPoint(s []UsagePoint, p UsagePoint, limit int) []UsagePoint {
	if n := len(s); n > 0 {
		last := s[n-1].Time
		if last.Equal(p.Time) {
			s[n-1] = p
			return s
		}
		if p.Time.Before(last) {
			return s // the clock went back: never reorder or rewrite the past
		}
	}
	s = append(s, p)
	if len(s) > limit {
		s = append([]UsagePoint(nil), s[len(s)-limit:]...)
	}
	return s
}

// UsageHistory is what GET /admin/usage/history reports.
type UsageHistory struct {
	Hourly []UsagePoint `json:"hourly"`
	Daily  []UsagePoint `json:"daily"`
	// Retention says how much is kept, for a reader of the JSON.
	Retention struct {
		Hours int `json:"hours"`
		Days  int `json:"days"`
	} `json:"retention"`
}

func (u *usageSeries) snapshot() UsageHistory {
	u.mu.Lock()
	defer u.mu.Unlock()
	h := UsageHistory{Hourly: append([]UsagePoint{}, u.hourly...), Daily: append([]UsagePoint{}, u.daily...)}
	h.Retention.Hours, h.Retention.Days = UsageHourlyPoints, UsageDailyPoints
	return h
}

// measureUsage reads the counters of the store now.
func (s *Server) measureUsage(ctx context.Context) (usageResponse, error) {
	var out usageResponse
	metas, err := s.adapter.List(ctx)
	if err != nil {
		return out, err
	}
	for _, m := range metas {
		if m.ID == UsageSeriesKey || m.ID == TelemetryKey {
			continue // the monitor's own records are not what it measures
		}
		if isReservedDocID(m.ID) {
			out.Records++
		} else {
			out.Documents++
		}
		out.Bytes += int64(m.Size)
		out.Largest = max(out.Largest, m.Size)
	}
	if s.telemetry != nil {
		t := s.telemetry.Totals()
		out.Telemetry = &t
	}
	if reporter, ok := s.adapter.(QuotaReporter); ok {
		if q, err := reporter.Usage(ctx); err == nil {
			out.Quota = quotaResponse{Available: true, Used: q.Used, Total: q.Total}
		}
	}
	return out, nil
}

// sampleUsage adds a sample of u to the series.
func (s *Server) sampleUsage(u usageResponse) {
	p := UsagePoint{Documents: u.Documents, Records: u.Records, Bytes: u.Bytes, Largest: u.Largest, QuotaUsed: u.Quota.Used}
	if u.Telemetry != nil {
		p.Accepted, p.Refused = u.Telemetry.Accepted, u.Telemetry.Refused
	}
	now := time.Now
	if s.usageClock != nil {
		now = s.usageClock
	}
	t := now()
	s.loadUsageSeries(context.Background(), t)
	newHour := s.usage.record(t, p)
	s.saveUsageSeries(t, newHour)
	s.observeUsage(u)
}

// saveUsageSeries writes the series when a new hour began or the last write is older than usageSaveEvery.
func (s *Server) saveUsageSeries(now time.Time, newHour bool) {
	s.usage.mu.Lock()
	due := newHour || now.Sub(s.usage.lastSaved) >= usageSaveEvery
	if due {
		s.usage.lastSaved = now
	}
	s.usage.mu.Unlock()
	if !due {
		return
	}
	if err := s.adapter.Save(context.Background(), UsageSeriesKey, s.usage.encode()); err != nil {
		log.Printf("docstore: the usage history could not be saved (%v)", err)
	}
}

// RunUsageSampling takes a sample of the counters now and then every interval (an hour is right for
// the hourly series) until ctx ends. A failing store skips that sample.
func (s *Server) RunUsageSampling(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	take := func() {
		done := observe.Default().Sweep("usage_sampling")
		u, err := s.measureUsage(ctx)
		if err == nil {
			s.sampleUsage(u)
		}
		done(0, err)
	}
	take()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			take()
		}
	}
}

// operatorOnly reports whether r is a GET from the operator; if not, it has written the refusal.
func (s *Server) operatorOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !s.dirAdmin(r) {
		lim := s.limits().adminFail
		if ok, wait := lim.Allow(r.Context(), "ad:"+s.clientAddr(r)); !ok {
			tooMany(w, wait)
			return false
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// currentHistory samples now and returns the series.
func (s *Server) currentHistory(w http.ResponseWriter, r *http.Request) (UsageHistory, bool) {
	u, err := s.measureUsage(r.Context())
	if err != nil {
		serverError(w, err)
		return UsageHistory{}, false
	}
	s.sampleUsage(u)
	return s.usage.snapshot(), true
}

// handleUsageHistory serves GET /admin/usage/history: the series as JSON.
func (s *Server) handleUsageHistory(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOnly(w, r) {
		return
	}
	h, ok := s.currentHistory(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(h)
}

//go:embed usagepage.html
var usagePageSource string

var usagePageTemplate = template.Must(template.New("usage").Funcs(template.FuncMap{
	"hour":  func(t time.Time) string { return t.Format("2006-01-02 15:04") + " UTC" },
	"day":   func(t time.Time) string { return t.Format("2006-01-02") },
	"iso":   func(t time.Time) string { return t.Format(time.RFC3339) },
	"size":  humanBytes,
	"isize": func(n int) string { return humanBytes(int64(n)) },
	"rev": func(s []UsagePoint) []UsagePoint { // newest first, for reading
		out := make([]UsagePoint, len(s))
		for i, p := range s {
			out[len(s)-1-i] = p
		}
		return out
	},
}).Parse(usagePageSource))

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// handleUsagePage serves GET /admin/usage/page: the series as a plain HTML page with tables, which
// needs no script. It carries the same numbers as the JSON and nothing else.
func (s *Server) handleUsagePage(w http.ResponseWriter, r *http.Request) {
	if !s.operatorOnly(w, r) {
		return
	}
	h, ok := s.currentHistory(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = usagePageTemplate.Execute(w, h) // an error here is the client going away
}
