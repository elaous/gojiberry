package public

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// versionsKeyPrefix is where a store that keeps version snapshots (docstore.DefaultRelated's
// layout) holds those of public documents; the byte budget counts them with the documents.
const versionsKeyPrefix = "versions/" + docKeyPrefix

// ---- the global byte budget

// budgetEntry is what the counter holds for one document: doc is its own bytes (as the service
// wrote them), extra the bytes of its version snapshots (known only from a listing).
type budgetEntry struct{ doc, extra int64 }

func (e budgetEntry) bytes() int64 { return e.doc + e.extra }

// budgetTouch is what writes did to a document while a reconcile was listing the store: removed
// (a discard, a takedown), the size of the document a commit stored (hasDoc, doc), and the bytes of
// version snapshots written since (extra), which a listing that predates them has not counted.
type budgetTouch struct {
	removed bool
	hasDoc  bool
	doc     int64
	extra   int64
}

// budget is the global counter of live public bytes (documents and their version snapshots).
//
// It is a ledger, one entry per document, plus the bytes that writes have reserved and not stored
// yet (pending). A write reserves before it stores and commits (or cancels) after, so the
// counter never admits more than the budget while a write is in flight. Sweep and the first write
// reconcile it: they list the store, and the result replaces the ledger, except that a
// document a write committed or removed while the listing ran (a "touch", kept per open reconcile
// window) keeps the write's word, since the listing may predate it. Pending reservations are not
// part of the ledger and so are never dropped by a reconcile (the generation, gen, counts
// completed reconciles). A removal (discard, takedown, a rolled-back create) subtracts exactly
// what the ledger holds for that document, and nothing if the current generation does not count
// it: the total can neither go below the sum of its documents nor subtract a document twice.
// The one inexactness is the safe one: a write that lands while a listing runs can be counted
// twice until the next reconcile, never missed.
//
// Version snapshots (the saver's "versions/pubdoc/<id>/...", written by something other than this
// service) are added to their document's entry when they are written (addExtra, fed by the
// ExpiringAdapter's Guard hook or Service.NoteVersionWrite), not only when a reconcile lists
// them, so the budget stops admitting new documents as soon as they fill it. Through an
// ExpiringAdapter a snapshot that would pass the budget is refused (docstore.ErrStorageFull)
// and a deleted one is subtracted at once (OnDelete). A snapshot written some other way
// (NoteVersionWrite after the fact) cannot be refused, and one deleted some other way is
// subtracted by the next reconcile.
type budget struct {
	mu         sync.Mutex
	ledger     map[string]budgetEntry
	total      int64 // the sum of the ledger
	pending    int64 // reserved by writes that have not stored yet
	gen        uint64
	reconciled bool
	nextWin    uint64
	windows    map[uint64]map[string]budgetTouch
}

// full reports whether nothing more fits.
func (b *budget) full(limit int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total+b.pending >= limit
}

// reservation is a write's claim on the budget. Exactly one of commit and cancel ends it.
type reservation struct {
	b     *budget
	doc   string
	size  int64 // the document's size once stored
	held  int64 // what was added to pending
	ended bool
}

// reserve claims growth of delta bytes (a negative delta always fits and claims nothing) for
// document doc, whose size will be size once the write is stored; it fails if the budget limit
// would be passed.
func (b *budget) reserve(doc string, size, delta, limit int64) (*reservation, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if delta > 0 && b.total+b.pending+delta > limit {
		return nil, false
	}
	r := &reservation{b: b, doc: doc, size: size}
	if delta > 0 {
		r.held = delta
		b.pending += delta
	}
	return r, true
}

func (b *budget) unholdLocked(n int64) {
	b.pending -= n
	if b.pending < 0 {
		log.Printf("public docstore: byte counter: pending reservations went to %d; clamped to 0", b.pending)
		b.pending = 0
	}
}

// commit turns the reservation into the document's counted size (the write is stored).
func (r *reservation) commit() {
	if r == nil {
		return
	}
	b := r.b
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.ended {
		return
	}
	r.ended = true
	b.unholdLocked(r.held)
	e := b.ledger[r.doc]
	b.setLocked(r.doc, budgetEntry{doc: max(0, r.size), extra: e.extra}, e)
}

// cancel gives the reservation back (the write was not stored).
func (r *reservation) cancel() {
	if r == nil {
		return
	}
	b := r.b
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.ended {
		return
	}
	r.ended = true
	b.unholdLocked(r.held)
}

// setLocked records doc's entry (old is what it replaces) and tells the open reconcile windows.
func (b *budget) setLocked(doc string, e, old budgetEntry) {
	if b.ledger == nil {
		b.ledger = map[string]budgetEntry{}
	}
	b.ledger[doc] = e
	b.total += e.bytes() - old.bytes()
	for _, w := range b.windows {
		t := w[doc]
		t.removed, t.hasDoc, t.doc = false, true, e.doc
		w[doc] = t
	}
}

// addExtra counts n bytes of a version snapshot of document doc at once (a snapshot of a document
// the ledger does not know yet gets an entry of its own: its bytes are in the store).
func (b *budget) addExtra(doc string, n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.ledger[doc]
	ne := e
	ne.extra += n
	if b.ledger == nil {
		b.ledger = map[string]budgetEntry{}
	}
	b.ledger[doc] = ne
	b.total += n
	for _, w := range b.windows {
		t := w[doc]
		t.removed = false
		t.extra += n
		w[doc] = t
	}
}

// holdExtra claims n bytes of a version snapshot that is about to be written, if they fit under
// limit; releaseExtra gives the claim back (the write ended, stored or not: a stored one is then
// counted by addExtra).
func (b *budget) holdExtra(n, limit int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total+b.pending+n > limit {
		return false
	}
	b.pending += n
	return true
}

func (b *budget) releaseExtra(n int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.unholdLocked(n)
}

// subExtra takes n bytes of a deleted version snapshot of document doc off the count (never more
// than the document's entry holds as snapshots).
func (b *budget) subExtra(doc string, n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.ledger[doc]
	if !ok {
		return
	}
	n = min(n, e.extra)
	if n == 0 {
		return
	}
	e.extra -= n
	b.ledger[doc] = e
	b.total -= n
	for _, w := range b.windows {
		t := w[doc]
		t.removed = false
		t.extra -= n // a listing that predates the delete counted it
		w[doc] = t
	}
}

// release removes document doc from the count and returns the bytes it held: what a discard, a
// takedown or a rolled-back write subtracts. A document the current generation does not count
// subtracts nothing.
func (b *budget) release(doc string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.ledger[doc]
	for _, w := range b.windows {
		w[doc] = budgetTouch{removed: true}
	}
	if !ok {
		return 0
	}
	delete(b.ledger, doc)
	b.total -= e.bytes()
	if b.total < 0 {
		log.Printf("public docstore: byte counter: total went to %d; clamped to 0", b.total)
		b.total = 0
	}
	return e.bytes()
}

// begin opens a reconcile window: the documents that writes touch until set or abort are
// remembered. Windows may overlap; each keeps its own touches, so none loses a write.
func (b *budget) begin() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextWin++
	if b.windows == nil {
		b.windows = map[uint64]map[string]budgetTouch{}
	}
	b.windows[b.nextWin] = map[string]budgetTouch{}
	return b.nextWin
}

// abort closes a window whose listing failed.
func (b *budget) abort(win uint64) {
	b.mu.Lock()
	delete(b.windows, win)
	b.mu.Unlock()
}

// set replaces the ledger with the listing, keeping what writes did to documents since begin, and
// starts a new generation.
func (b *budget) set(win uint64, listed map[string]budgetEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	touches := b.windows[win]
	delete(b.windows, win)
	ledger := make(map[string]budgetEntry, len(listed))
	for k, e := range listed {
		ledger[k] = e
	}
	for k, t := range touches {
		if t.removed {
			delete(ledger, k)
			continue
		}
		e := ledger[k]
		if t.hasDoc {
			e.doc = t.doc
		}
		e.extra += t.extra
		ledger[k] = e
	}
	var total int64
	for _, e := range ledger {
		total += e.bytes()
	}
	b.ledger, b.total = ledger, total
	b.gen++
	b.reconciled = true
}

// get is the bytes counted, reserved ones included, and whether a reconcile has completed.
func (b *budget) get() (used int64, reconciled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total + b.pending, b.reconciled
}

// generation is the number of completed reconciles.
func (b *budget) generation() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.gen
}

// reportInfo is what the counters need to know about a stored report.
type reportInfo struct {
	reporter string
	doc      string
	priority bool
	at       time.Time
}

// reportWindow is what was added while a reconcile listed the reports.
type reportWindow struct {
	n     int
	bytes int64
	rep   map[string]int
}

// reportStats counts the stored reports, their bytes and how many each reporter holds.
type reportStats struct {
	mu        sync.Mutex
	n         int
	bytes     int64
	reporters map[string]int
	nextWin   uint64
	windows   map[uint64]*reportWindow
}

// add changes the counts (a negative n is an eviction; the counts are clamped at zero and a
// clamp is logged: it means a report was counted out twice).
func (r *reportStats) add(n int, bytes int64, reporter string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n += n
	r.bytes += bytes
	if r.n < 0 || r.bytes < 0 {
		log.Printf("public docstore: report counter went to %d reports, %d bytes; clamped to 0", r.n, r.bytes)
		r.n, r.bytes = max(0, r.n), max(0, r.bytes)
	}
	if reporter != "" {
		if r.reporters == nil {
			r.reporters = map[string]int{}
		}
		if r.reporters[reporter] += n; r.reporters[reporter] <= 0 {
			delete(r.reporters, reporter)
		}
	}
	if n > 0 { // only growth is remembered: a listing that missed it must not lose it
		for _, w := range r.windows {
			w.n += n
			w.bytes += bytes
			if reporter != "" {
				w.rep[reporter] += n
			}
		}
	}
}

func (r *reportStats) begin() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextWin++
	if r.windows == nil {
		r.windows = map[uint64]*reportWindow{}
	}
	r.windows[r.nextWin] = &reportWindow{rep: map[string]int{}}
	return r.nextWin
}

func (r *reportStats) abort(win uint64) {
	r.mu.Lock()
	delete(r.windows, win)
	r.mu.Unlock()
}

// set replaces the counts with a listing, plus the reports stored since begin.
func (r *reportStats) set(win uint64, n int, bytes int64, reporters map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := r.windows[win]
	delete(r.windows, win)
	if w == nil {
		w = &reportWindow{rep: map[string]int{}}
	}
	r.n = n + w.n
	r.bytes = bytes + w.bytes
	r.reporters = make(map[string]int, len(reporters))
	for k, v := range reporters {
		r.reporters[k] = v
	}
	for k, v := range w.rep {
		r.reporters[k] += v
	}
}

func (r *reportStats) over(maxN int, maxBytes int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n > maxN || r.bytes > maxBytes
}

// held is how many reports are stored in all, and how many of them reporter's.
func (r *reportStats) held(reporter string) (all, mine int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n, r.reporters[reporter]
}

// reconcileBackoff is how long writes stay refused after the first reconcile failed: the counters
// are unknown, and guessing zero would admit writes past the budget.
const reconcileBackoff = 15 * time.Second

// reconcileTimeout bounds the first reconcile. It runs on a context of its own (the request that
// happens to start it may go away, and its disconnect must not fail the reconcile for everyone),
// so it needs a bound of its own.
const reconcileTimeout = 2 * time.Minute

// ensureCounters reconciles the counters with the store once before the first write (every Sweep
// does it afterwards): the budget must not start from zero after a restart. It reports whether
// writes may go on. If the first reconcile fails, writes fail CLOSED (false) for
// reconcileBackoff, then try again, instead of running against an empty count.
//
// The reconcile does not run on the caller's context: a client that disconnects mid-way would
// cancel the store listing and put every write of everyone into the backoff. It runs under
// context.WithoutCancel with reconcileTimeout. A request that finds a reconcile under way waits for
// it for at most its own context (its client going away) or RequestTimeout, and is answered as a
// refusal (false) if that runs out first; the reconcile goes on.
func (s *Service) ensureCounters(ctx context.Context) bool {
	if _, done := s.bytes.get(); done {
		return true
	}
	wait := time.NewTimer(s.cfg.RequestTimeout)
	defer wait.Stop()
	select {
	case s.recSem <- struct{}{}:
	case <-ctx.Done():
		return false
	case <-wait.C:
		return false
	}
	defer func() { <-s.recSem }()
	if _, done := s.bytes.get(); done {
		return true
	}
	now := s.now()
	if now.Before(s.recRetry) {
		return false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reconcileTimeout)
	defer cancel()
	if err := s.reconcile(rctx); err != nil {
		log.Printf("public docstore: counting stored bytes: %s (writes are refused for %s)", redact(err), reconcileBackoff)
	}
	if _, done := s.bytes.get(); !done {
		s.recRetry = now.Add(reconcileBackoff)
		return false
	}
	return true
}

// listBudget lists the public documents and their version snapshots as a ledger.
func (s *Service) listBudget(ctx context.Context) (map[string]budgetEntry, error) {
	metas, err := docstore.ListPrefix(ctx, s.store, docKeyPrefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]budgetEntry, len(metas))
	for _, m := range metas {
		id := strings.TrimPrefix(m.ID, docKeyPrefix)
		e := out[id]
		e.doc += int64(m.Size)
		out[id] = e
	}
	vs, err := docstore.ListPrefix(ctx, s.store, versionsKeyPrefix)
	if err != nil {
		return nil, err
	}
	for _, m := range vs {
		id, _, _ := strings.Cut(strings.TrimPrefix(m.ID, versionsKeyPrefix), "/")
		e := out[id]
		e.extra += int64(m.Size)
		out[id] = e
	}
	return out, nil
}

// reconcile recomputes the byte and report counters from the store. The two are independent: the
// bytes are set even if the reports cannot be listed.
func (s *Service) reconcile(ctx context.Context) error {
	win := s.bytes.begin()
	listed, err := s.listBudget(ctx)
	if err != nil {
		s.bytes.abort(win)
		return err
	}
	s.bytes.set(win, listed)
	return s.recountReports(ctx)
}

// listReports lists the stored reports and reads each one's reporter and priority.
func (s *Service) listReports(ctx context.Context) ([]docstore.Meta, map[string]reportInfo, error) {
	metas, err := docstore.ListPrefix(ctx, s.store, docstore.ReportPrefix)
	if err != nil {
		return nil, nil, err
	}
	info := make(map[string]reportInfo, len(metas))
	for _, m := range metas {
		var rep Report
		if found, err := s.getJSON(ctx, m.ID, &rep); err != nil {
			return nil, nil, err
		} else if found {
			info[m.ID] = reportInfo{reporter: rep.Reporter, doc: rep.DocID, priority: rep.Priority, at: rep.At}
		}
	}
	return metas, info, nil
}

// recountReports sets the report counters from a fresh listing.
func (s *Service) recountReports(ctx context.Context) error {
	return s.recountReportsIn(ctx, s.reports.begin())
}

// recountReportsIn is recountReports for a window opened earlier (Sweep opens it at its start, so
// the reports stored while it works are kept).
func (s *Service) recountReportsIn(ctx context.Context, win uint64) error {
	start := s.now()
	metas, info, err := s.listReports(ctx)
	if err != nil {
		s.reports.abort(win)
		return err
	}
	s.setReportCounts(win, metas, info)
	s.prio.rebuild(info, s.now(), start)
	return nil
}

func (s *Service) setReportCounts(win uint64, metas []docstore.Meta, info map[string]reportInfo) {
	var b int64
	reporters := map[string]int{}
	for _, m := range metas {
		b += int64(m.Size)
		if r := info[m.ID].reporter; r != "" {
			reporters[r]++
		}
	}
	s.reports.set(win, len(metas), b, reporters)
}

// isPriority reports whether a report category is on the priority list. The free-text reason has
// no say.
func (s *Service) isPriority(category string) bool {
	n := categoryOf(category)
	for _, p := range s.cfg.ReportPriorityCategories {
		if n == p {
			return true
		}
	}
	return false
}

// evictReports deletes reports among metas until the stored reports are within MaxReports and
// MaxReportBytes (a little under, so the next report does not evict again): the oldest first, but
// every report whose reason is a priority one only after all the others.
func (s *Service) evictReports(ctx context.Context, metas []docstore.Meta, info map[string]reportInfo) int {
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
		if pi, pj := info[metas[i].ID].priority, info[metas[j].ID].priority; pi != pj {
			return !pi
		}
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
		s.reports.add(-1, -int64(m.Size), info[m.ID].reporter)
		s.dropRef(ctx, info[m.ID].doc, strings.TrimPrefix(m.ID, docstore.ReportPrefix))
		evicted++
	}
	return evicted
}

// dropRef removes the reference to report reportID from its document's record, under the
// document's lock: a report that was deleted (evicted, or past its TTL) must not stay in the
// record, where it would count against the document's slots and answer a duplicate with the id of
// a report that is gone. The caller must not hold any document lock.
func (s *Service) dropRef(ctx context.Context, docID, reportID string) {
	if !validID(docID) || reportID == "" {
		return
	}
	unlock := s.lock(docID)
	defer unlock()
	var meta docMeta
	if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil {
		log.Printf("public docstore: updating a document's report list: %s", redact(err))
		return
	} else if !found {
		return
	}
	if !removeRef(&meta, reportID) {
		return
	}
	if err := s.putJSON(ctx, docRecordPrefix+docID, meta); err != nil {
		log.Printf("public docstore: updating a document's report list: %s", redact(err))
	}
}

// removeRef deletes the reference with the given report id from meta and reports whether it was
// there; the count follows.
func removeRef(meta *docMeta, reportID string) bool {
	for i, ref := range meta.ReportRefs {
		if ref.ID == reportID {
			meta.ReportRefs = append(meta.ReportRefs[:i:i], meta.ReportRefs[i+1:]...)
			meta.Reports = len(meta.ReportRefs)
			return true
		}
	}
	return false
}

// ---- the cap on request bodies in flight

// inflight bounds the bytes of request bodies being read at once: in all, per client (an IPv4
// address or an IPv6 /64) and per network (a /48 or a /24). The shares are what keep a few slow
// writers from holding the whole cap and turning every write of everyone else into a 503.
type inflight struct {
	mu        sync.Mutex
	used      int64
	max       int64
	perClient int64 // 0: no per-client share
	perNet    int64 // 0: no per-network share
	byClient  map[string]int64
	byNet     map[string]int64
}

// flightResult says why acquire refused.
type flightResult int

const (
	flightOK flightResult = iota
	flightClient
	flightNet
	flightGlobal
)

func (f *inflight) acquire(client, net string, n int64) flightResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.used+n > f.max:
		return flightGlobal
	case f.perClient > 0 && f.byClient[client]+n > f.perClient:
		return flightClient
	case f.perNet > 0 && f.byNet[net]+n > f.perNet:
		return flightNet
	}
	f.used += n
	if f.byClient == nil {
		f.byClient, f.byNet = map[string]int64{}, map[string]int64{}
	}
	f.byClient[client] += n
	f.byNet[net] += n
	return flightOK
}

func (f *inflight) release(client, net string, n int64) {
	f.mu.Lock()
	f.used -= n
	if f.used < 0 {
		log.Printf("public docstore: in-flight counter went to %d; clamped to 0", f.used)
		f.used = 0
	}
	sub := func(m map[string]int64, k string) {
		if m == nil {
			return
		}
		if m[k] -= n; m[k] <= 0 {
			delete(m, k)
		}
	}
	sub(f.byClient, client)
	sub(f.byNet, net)
	f.mu.Unlock()
}

func (f *inflight) held() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used
}

// heldBy is what the client holds.
func (f *inflight) heldBy(client string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byClient[client]
}

// ---- charging before the body is read

// ticket is what a write holds while it is in flight: the bytes charged to the address's daily
// quota (in the quota window it was charged in) and the bytes reserved in the in-flight caps.
// release gives back what the request did not end up writing, for a failure of the server's own
// (a store error, a refusal that is not the client's doing); a charge the client earned, a body
// that arrived too slowly or was refused for its content, is made final with keep.
type ticket struct {
	s      *Service
	key    string
	client string
	net    string
	held   int64
	win    int64 // the quota window the charge went into (WindowQuota)
	hasWin bool
	flight int64
}

// admit runs the checks that need no body, in front of reading one: a declared size over the
// cap (413), a full store (503), too many bytes of bodies in flight for the client or its network
// (429) or in all (503), the address's daily bytes (429, the declared size, or the cap for a body
// of unknown length, is charged and settle gives the difference back). The caller must
// `defer t.release()` and call t.keep() once the write is stored (or the charge is the client's to
// pay). It answers 503 while the counters are unknown (a failed first reconcile).
func (s *Service) admit(w http.ResponseWriter, r *http.Request) (*ticket, bool) {
	if r.ContentLength > s.cfg.MaxDocBytes {
		http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	if !s.ensureCounters(r.Context()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(reconcileBackoff/time.Second)))
		http.Error(w, "the public offer is starting up; try again shortly", http.StatusServiceUnavailable)
		return nil, false
	}
	if s.bytes.full(s.cfg.MaxTotalBytes) {
		storeFull(w)
		return nil, false
	}
	n := r.ContentLength
	if n < 0 {
		n = s.cfg.MaxDocBytes
	}
	addr := s.clientIP(r)
	client, net := ClientKey(addr), NetKey(addr)
	switch s.flight.acquire(client, net, n) {
	case flightClient, flightNet:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many of your uploads in progress; try again shortly", http.StatusTooManyRequests)
		return nil, false
	case flightGlobal:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many uploads in progress; try again shortly", http.StatusServiceUnavailable)
		return nil, false
	}
	t := &ticket{s: s, key: "ip:" + client, client: client, net: net, held: n, flight: n}
	if n > 0 {
		var ok bool
		var retry time.Duration
		var err error
		if wq, isWin := s.quota.(WindowQuota); isWin {
			ok, t.win, retry, err = wq.TakeIn(r.Context(), t.key, n)
			t.hasWin = true
		} else {
			ok, retry, err = s.quota.Take(r.Context(), t.key, n)
		}
		if err != nil {
			t.held = 0
			t.release()
			s.fail(w, err)
			return nil, false
		}
		if !ok {
			t.held = 0
			t.release()
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
	if q, ok := t.s.quota.(WindowQuota); ok && t.hasWin {
		q.RefundIn(context.Background(), t.key, n, t.win)
	} else if q, ok := t.s.quota.(QuotaRefunder); ok {
		q.Refund(context.Background(), t.key, n)
	}
}

// settle trims the charge, and the in-flight claim, to the bytes actually read.
func (t *ticket) settle(actual int64) {
	if t.held > actual {
		t.refund(t.held - actual)
		t.held = actual
	}
	if t.flight > actual {
		t.s.flight.release(t.client, t.net, t.flight-actual)
		t.flight = actual
	}
}

// release gives back what is still held (a write that failed or was refused) and the in-flight
// claim. A nil ticket is fine.
func (t *ticket) release() {
	if t == nil {
		return
	}
	t.refund(t.held)
	t.held = 0
	t.s.flight.release(t.client, t.net, t.flight)
	t.flight = 0
}

// keep makes the quota charge final (the in-flight claim ends with release).
func (t *ticket) keep() { t.held = 0 }

func storeFull(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "300")
	http.Error(w, "the public offer's storage is full right now; try again later", http.StatusServiceUnavailable)
}

// ---- health

// Health is what /admin/health reports (and /public/health reduces to its OK): whether the
// promises the offer rests on are being kept. It holds no secrets and no per-document data, but
// it says how full the offer is, so it is for the operator.
type Health struct {
	OK       bool     `json:"ok"`
	Problems []string `json:"problems,omitempty"`
	// LastSweep is when Sweep last finished (the zero time: never).
	LastSweep time.Time `json:"lastSweep"`
	// StorageBytes and StorageMax are the byte counter and its budget.
	StorageBytes int64 `json:"storageBytes"`
	StorageMax   int64 `json:"storageMax"`
	// PriorityBursts is how many networks sent priority-category reports for more than
	// Config.PriorityBurstDocs different documents within the last hour: a flood of the path that
	// outranks other reports, or a real wave of abuse, either way for the operator to look at.
	// It is not a Problem (it does not turn /public/health to 503: that is for the offer's own
	// promises).
	PriorityBursts int `json:"priorityBursts"`
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
	h := Health{LastSweep: last.UTC(), StorageMax: s.cfg.MaxTotalBytes, PriorityBursts: s.prio.bursts(now, s.cfg.PriorityBurstDocs)}
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

// handleHealth serves GET /public/health, which anyone may call: 200 when everything holds and 503
// when not, with a body that says no more than that. The storage figures, the names of the problems
// and the time of the last sweep tell an attacker when the offer is full or its sweeper is down, so
// they are in the detailed report behind the admin guard (GET /admin/health) only.
func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	if s.Health().OK {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]bool{"ok": false})
}

// handleAdminHealth serves GET /admin/health: the detailed Health report, for the operator.
func (s *Service) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	if !s.adminAllowed(w, r) {
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
