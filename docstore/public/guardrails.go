package public

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// versionsKeyPrefix is where a store that keeps version snapshots (docstore.DefaultRelated's
// layout) holds those of public documents; the byte budget counts them with the documents.
const versionsKeyPrefix = "versions/" + docKeyPrefix

// ---- the global byte budget

// budget is the global counter of live public bytes (documents and their version snapshots).
// Writes reserve against it atomically; Sweep recomputes it from the store and replaces the
// counter, keeping what writes added while it was listing (since).
type budget struct {
	mu         sync.Mutex
	total      int64
	since      int64 // net change since begin()
	reconciled bool
}

// full reports whether nothing more fits.
func (b *budget) full(limit int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total >= limit
}

// reserve adds delta unless that would pass limit (a negative delta always fits).
func (b *budget) reserve(delta, limit int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if delta > 0 && b.total+delta > limit {
		return false
	}
	b.total += delta
	b.since += delta
	return true
}

func (b *budget) add(delta int64) {
	b.mu.Lock()
	b.total += delta
	b.since += delta
	b.mu.Unlock()
}

func (b *budget) begin() {
	b.mu.Lock()
	b.since = 0
	b.mu.Unlock()
}

func (b *budget) set(listed int64) {
	b.mu.Lock()
	b.total = max(0, listed+b.since)
	b.reconciled = true
	b.mu.Unlock()
}

func (b *budget) get() (total int64, reconciled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total, b.reconciled
}

// reportStats counts the stored reports and their bytes.
type reportStats struct {
	mu    sync.Mutex
	n     int
	bytes int64
	since [2]int64
}

func (r *reportStats) add(n int, bytes int64) {
	r.mu.Lock()
	r.n += n
	r.bytes += bytes
	r.since[0] += int64(n)
	r.since[1] += bytes
	r.mu.Unlock()
}

func (r *reportStats) begin() {
	r.mu.Lock()
	r.since = [2]int64{}
	r.mu.Unlock()
}

func (r *reportStats) set(n int, bytes int64) {
	r.mu.Lock()
	r.n = max(0, n+int(r.since[0]))
	r.bytes = max(0, bytes+r.since[1])
	r.mu.Unlock()
}

func (r *reportStats) over(maxN int, maxBytes int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n > maxN || r.bytes > maxBytes
}

// counters are reconciled with the store once before the first write (and by every Sweep): the
// budget must not start from zero after a restart.
func (s *Service) ensureCounters(ctx context.Context) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if _, done := s.bytes.get(); done {
		return
	}
	if err := s.reconcile(ctx); err != nil {
		log.Printf("public docstore: counting stored bytes: %s", redact(err))
	}
}

// reconcile recomputes the byte and report counters from the store.
func (s *Service) reconcile(ctx context.Context) error {
	s.bytes.begin()
	s.reports.begin()
	metas, err := docstore.ListPrefix(ctx, s.store, docKeyPrefix)
	if err != nil {
		return err
	}
	var total int64
	for _, m := range metas {
		total += int64(m.Size)
	}
	if vs, err := docstore.ListPrefix(ctx, s.store, versionsKeyPrefix); err == nil {
		for _, m := range vs {
			total += int64(m.Size)
		}
	} else {
		return err
	}
	s.bytes.set(total)
	reps, err := docstore.ListPrefix(ctx, s.store, docstore.ReportPrefix)
	if err != nil {
		return err
	}
	var rb int64
	for _, m := range reps {
		rb += int64(m.Size)
	}
	s.reports.set(len(reps), rb)
	return nil
}

// evictReports deletes the oldest reports among metas until the stored reports are within
// MaxReports and MaxReportBytes (a little under, so the next report does not evict again).
func (s *Service) evictReports(ctx context.Context, metas []docstore.Meta) int {
	maxN, maxB := s.cfg.MaxReports, s.cfg.MaxReportBytes
	targetN, targetB := maxN-max(1, maxN/20), maxB-max(1, maxB/20)
	var n int
	var bytes int64
	for _, m := range metas {
		n++
		bytes += int64(m.Size)
	}
	if n <= maxN && bytes <= maxB {
		return 0
	}
	sort.Slice(metas, func(i, j int) bool {
		if !metas[i].UpdatedAt.Equal(metas[j].UpdatedAt) {
			return metas[i].UpdatedAt.Before(metas[j].UpdatedAt)
		}
		return metas[i].ID < metas[j].ID
	})
	evicted := 0
	for _, m := range metas {
		if n <= targetN && bytes <= targetB {
			break
		}
		if err := s.deleteKey(ctx, m.ID); err != nil {
			log.Printf("public docstore: evicting a report: %s", redact(err))
			break
		}
		n--
		bytes -= int64(m.Size)
		s.reports.add(-1, -int64(m.Size))
		evicted++
	}
	return evicted
}

// ---- charging before the body is read

// ticket is what a write holds while it is in flight: the bytes charged to the address's daily
// quota. release gives back what the request did not end up writing.
type ticket struct {
	s    *Service
	key  string
	held int64
}

// admit runs the checks that need no body, in front of reading one: a declared size over the
// cap (413), a full store (503), the address's daily bytes (429, the declared size, or the cap
// for a body of unknown length, is charged and settle gives the difference back). The caller
// must `defer t.release()` and call t.keep() once the write is stored.
func (s *Service) admit(w http.ResponseWriter, r *http.Request) (*ticket, bool) {
	if r.ContentLength > s.cfg.MaxDocBytes {
		http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	s.ensureCounters(r.Context())
	if s.bytes.full(s.cfg.MaxTotalBytes) {
		storeFull(w)
		return nil, false
	}
	n := r.ContentLength
	if n < 0 {
		n = s.cfg.MaxDocBytes
	}
	t := &ticket{s: s, key: "ip:" + s.clientKey(r), held: n}
	if n > 0 {
		ok, retry, err := s.quota.Take(r.Context(), t.key, n)
		if err != nil {
			t.held = 0
			s.fail(w, err)
			return nil, false
		}
		if !ok {
			t.held = 0
			tooMany(w, retry)
			return nil, false
		}
	} else {
		t.held = 0
	}
	return t, true
}

func (t *ticket) refund(n int64) {
	if n <= 0 {
		return
	}
	if q, ok := t.s.quota.(QuotaRefunder); ok {
		q.Refund(context.Background(), t.key, n)
	}
}

// settle trims the charge to the bytes actually read.
func (t *ticket) settle(actual int64) {
	if t.held > actual {
		t.refund(t.held - actual)
		t.held = actual
	}
}

// release gives back what is still held (a write that failed or was refused). A nil ticket is fine.
func (t *ticket) release() {
	if t == nil {
		return
	}
	t.refund(t.held)
	t.held = 0
}

// keep makes the charge final.
func (t *ticket) keep() { t.held = 0 }

func storeFull(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "300")
	http.Error(w, "the public offer's storage is full right now; try again later", http.StatusServiceUnavailable)
}

// ---- health

// Health is what /public/health reports: whether the promises the offer rests on are being
// kept. It holds no secrets and no per-document data.
type Health struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems,omitempty"`
	// LastSweep is when Sweep last finished (the zero time: never).
	LastSweep time.Time `json:"lastSweep"`
	// StorageBytes and StorageMax are the byte counter and its budget.
	StorageBytes int64 `json:"storageBytes"`
	StorageMax   int64 `json:"storageMax"`
}

// Problem names reported in Health.Problems.
const (
	ProblemExpiryDisabled = "expiry-disabled" // the store's policy gives public documents no deadline
	ProblemNoDeadline     = "no-deadline"     // a public document was seen without a deadline (and removed)
	ProblemSweeperStale   = "sweeper-stale"   // Sweep has not run for twice its interval
	ProblemSweepFailing   = "sweep-failing"   // the last Sweep ended in an error
	ProblemStorageFull    = "storage-full"    // the byte budget is used up: writes are refused
)

// DefaultSweepInterval is how often Run sweeps when it is given no interval, and the interval
// Health assumes for a Service whose Run was never started.
const DefaultSweepInterval = 5 * time.Minute

// expiryDisabled asks the store's policy (when it is an ExpiringAdapter, the only one that can be
// asked) whether a public document would get a deadline.
func (s *Service) expiryDisabled() bool {
	ea, ok := s.store.(*docstore.ExpiringAdapter)
	if !ok || ea.Policy == nil {
		return false
	}
	ttl, has := ea.Policy.TTL(docKey("health-probe"), s.now())
	return !has || ttl <= 0
}

// Health reports on the offer's own promises: documents get deadlines, the sweeper runs, the
// storage budget has room.
func (s *Service) Health() Health {
	now := s.now()
	s.healthMu.Lock()
	last, failing, every, born := s.lastSweep, s.sweepFailing, s.sweepEvery, s.born
	s.healthMu.Unlock()
	if every <= 0 {
		every = DefaultSweepInterval
	}
	h := Health{LastSweep: last.UTC(), StorageMax: s.cfg.MaxTotalBytes}
	h.StorageBytes, _ = s.bytes.get()
	if s.expiryDisabled() {
		h.Problems = append(h.Problems, ProblemExpiryDisabled)
	}
	if s.noDeadline.Load() {
		h.Problems = append(h.Problems, ProblemNoDeadline)
	}
	base := last
	if base.IsZero() {
		base = born
	}
	if now.Sub(base) > 2*every {
		h.Problems = append(h.Problems, ProblemSweeperStale)
	}
	if failing {
		h.Problems = append(h.Problems, ProblemSweepFailing)
	}
	if h.StorageBytes >= h.StorageMax {
		h.Problems = append(h.Problems, ProblemStorageFull)
	}
	h.OK = len(h.Problems) == 0
	return h
}

// handleHealth serves GET /public/health: 200 when everything holds, 503 with the problem names
// when not, so a monitor can alarm on it.
func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	h := s.Health()
	status := http.StatusOK
	if !h.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, h)
}

// ---- small helpers

// keyPattern matches the store keys of this package: they hold document IDs, which a log line
// must not carry.
var keyPattern = regexp.MustCompile(`(?i)(pubdoc|public|report|versions/pubdoc|expiry:pubdoc)/[A-Za-z0-9_/.\-]*`)

// redact is err's text with store keys (which contain document IDs) replaced by their keyspace.
func redact(err error) string {
	if err == nil {
		return ""
	}
	return keyPattern.ReplaceAllString(err.Error(), "$1/<id>")
}

// canListPrefix reports whether a listing by prefix is cheaper than listing the store: a
// PrefixLister, looking through the ExpiringAdapter (which always has the method).
func canListPrefix(a docstore.Adapter) bool {
	if e, ok := a.(*docstore.ExpiringAdapter); ok {
		return canListPrefix(e.Inner)
	}
	_, ok := a.(docstore.PrefixLister)
	return ok
}

var errNoDeadline = errors.New("a public document got no expiry; check the expiry policy")

func (s *Service) alarmNoDeadline() {
	s.noDeadline.Store(true)
	log.Print("public docstore: ALARM: a public document was stored without a deadline and was removed: the store's expiry policy does not cover " + fmt.Sprintf("%q", docKeyPrefix) + " keys, so the 24 hour promise is not kept; see /public/health")
}

// RequireDocsClosed is the check behind "the public offer does not start with the generic /docs API
// open": it returns an error unless srv's /docs is closed (docstore.Server.DocsClosed: CloseDocs, or
// required ACLs with sign-in) or the operator has explicitly allowed an open one (allowOpen).
func RequireDocsClosed(srv *docstore.Server, allowOpen bool) error {
	if srv.DocsClosed() || allowOpen {
		return nil
	}
	return errors.New("public: the generic /docs API is open to anonymous readers and writers; close it (docstore.Server.CloseDocs), protect it with sign-in and required ACLs, or allow it explicitly with GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true")
}
