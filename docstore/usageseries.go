package docstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/elaous/gojiberry/observe"
	"html/template"
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
//   - in memory only: it is not written to the store or to a log, and a restart starts it again (the
//     gap shows as missing points, never as made-up ones). A point is the last sample taken in its hour
//     (or day), so the totals read at the end of the period.
//
// Samples are taken when the operator reads the usage history routes and, with RunUsageSampling, every hour.

// The retention of the series.
const (
	UsageHourlyPoints = 48 // two days of hours
	UsageDailyPoints  = 90 // about three months of days
)

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
	mu     sync.Mutex
	hourly []UsagePoint // oldest first, at most UsageHourlyPoints
	daily  []UsagePoint // oldest first, at most UsageDailyPoints
}

// record stores p taken at now: it replaces the point of the same hour (day) or appends a new one, and
// drops what is past the retention.
func (u *usageSeries) record(now time.Time, p UsagePoint) {
	now = now.UTC()
	u.mu.Lock()
	defer u.mu.Unlock()
	h := p
	h.Time = now.Truncate(time.Hour)
	u.hourly = putPoint(u.hourly, h, UsageHourlyPoints)
	d := p
	d.Time = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	u.daily = putPoint(u.daily, d, UsageDailyPoints)
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
	s.usage.record(now(), p)
	s.observeUsage(u)
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
