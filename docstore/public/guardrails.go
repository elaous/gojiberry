package public

import (
	"context"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// versionsKeyPrefix is the keyspace of version snapshots.
const versionsKeyPrefix = "versions/" + docKeyPrefix

// budgetEntry is what the counter holds for one document.
type budgetEntry struct{ doc, extra int64 }

func (e budgetEntry) bytes() int64 { return e.doc + e.extra }

// budgetTouch is what writes did to a document during a reconcile.
type budgetTouch struct {
	removed bool
	hasDoc  bool
	doc     int64
	extra   int64
}

// budget is the global counter of live public bytes.
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

// reservation is a write's claim on the budget.
type reservation struct {
	b     *budget
	doc   string
	size  int64 // the document's size once stored
	held  int64 // what was added to pending
	ended bool
}

// reserve claims growth of delta bytes.
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

// commit turns the reservation into the document's counted size.
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

// cancel gives the reservation back when a write fails.
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

// setLocked records doc's entry.
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

// addExtra counts n bytes of a version snapshot.
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

// holdExtra claims n bytes of a version snapshot.
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

// subExtra takes n bytes of a deleted version snapshot.
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
		t.extra -= n
		w[doc] = t
	}
}

// release removes document doc from the count.
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

// begin opens a reconcile window.
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

// set replaces the ledger with the listing.
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

// get is the bytes counted, reserved ones included.
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

// reportInfo is what the counters need about a report.
type reportInfo struct {
	reporter string
	doc      string
	priority bool
	at       time.Time
}

// reportWindow is what was added while a reconcile ran.
type reportWindow struct {
	n     int
	bytes int64
	rep   map[string]int
}

// reportStats counts the stored reports and their bytes.
type reportStats struct {
	mu        sync.Mutex
	n         int
	bytes     int64
	reporters map[string]int
	nextWin   uint64
	windows   map[uint64]*reportWindow
}

// add changes the counts; a negative n is an eviction.
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
	if n > 0 {
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

// set replaces the counts with a listing.
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

// held is how many reports are stored in all.
func (r *reportStats) held(reporter string) (all, mine int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n, r.reporters[reporter]
}

// reconcileBackoff is how long writes wait after a failed reconcile.
const reconcileBackoff = 15 * time.Second

// reconcileTimeout bounds the first reconcile.
const reconcileTimeout = 2 * time.Minute

// ensureCounters reconciles the counters with the store once.
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

// listBudget lists the public documents and their versions.
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

// reconcile recomputes the byte and report counters from the store.
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

// listReports lists the stored reports and reads each.
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

// recountReportsIn is recountReports for a window.
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

// isPriority reports whether a report category is priority.
func (s *Service) isPriority(category string) bool {
	n := categoryOf(category)
	for _, p := range s.cfg.ReportPriorityCategories {
		if n == p {
			return true
		}
	}
	return false
}

// evictReports deletes reports among metas until there is room.
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

// dropRef removes the reference to report reportID from a document.
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

// removeRef deletes the reference with the given report ID.
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

// keyPattern matches the store keys of this package.
var keyPattern = regexp.MustCompile(`(?i)(pubdoc|public|report|versions/pubdoc|expiry:pubdoc)/[A-Za-z0-9_/.\-]*`)

// redact is err's text with store keys removed.
func redact(err error) string {
	if err == nil {
		return ""
	}
	return keyPattern.ReplaceAllString(err.Error(), "$1/<id>")
}

// canListPrefix reports whether a listing by prefix is supported.
func canListPrefix(a docstore.Adapter) bool {
	if e, ok := a.(*docstore.ExpiringAdapter); ok {
		return canListPrefix(e.Inner)
	}
	_, ok := a.(docstore.PrefixLister)
	return ok
}

// RequireDocsClosed is the check behind closing the generic routes.
func RequireDocsClosed(srv *docstore.Server, allowOpen bool) error {
	if srv.DocsClosed() || allowOpen {
		return nil
	}
	return errors.New("public: the generic /docs API is open to anonymous readers and writers; close it (docstore.Server.CloseDocs), protect it with sign-in and required ACLs, or allow it explicitly with GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_OPEN_DOCS=true")
}
