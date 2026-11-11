package public

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type reportRef struct {
	ID       string    `json:"id"`
	Reporter string    `json:"by"`  // the reporter's hash (reporterHash)
	Key      string    `json:"key"` // hash of (reporter, category): the dedupe key
	Priority bool      `json:"pri,omitempty"`
	Size     int       `json:"size"`
	At       time.Time `json:"at"`
}

// Report is a stored abuse report.
type Report struct {
	ID     string    `json:"id"`
	ViewID string    `json:"view"`
	DocID  string    `json:"doc"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	// Category is one of ReportCategories.
	Category string `json:"category,omitempty"`
	Priority bool   `json:"priority,omitempty"`
	// Reporter is a keyed hash of the reporting client.
	Reporter string `json:"reporter,omitempty"`
}

const (
	maxReportBody    = 8 << 10
	maxReasonBytes   = 2000
	maxReportsOfDoc  = 20
	maxPriorityOfDoc = maxReportsOfDoc / 2
)

func (s *Service) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if !s.allow(w, r, s.lim.Report, "report", "") {
		return
	}
	ctx := r.Context()
	var body struct {
		ID       string `json:"id"`
		Reason   string `json:"reason"`
		Category string `json:"category"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxReportBody))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if json.Unmarshal(data, &body) != nil || strings.TrimSpace(body.Reason) == "" || strings.TrimSpace(body.ID) == "" {
		http.Error(w, `expected {"id": "<view id>", "reason": "<why>", "category": "csam|illegal|spam|other"}`, http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > maxReasonBytes {
		reason = strings.ToValidUTF8(reason[:maxReasonBytes], "")
	}
	category := categoryOf(body.Category)
	id := strings.TrimSpace(body.ID)
	if validID(id) {
		var t tombstone
		if gone, err := s.getJSON(ctx, tombPrefix+id, &t); err != nil {
			s.fail(w, err)
			return
		} else if gone {
			http.Error(w, "this document was already removed", http.StatusGone)
			return
		}
	}
	docID, view, found, err := s.resolve(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	addr := s.clientIP(r)
	reporter, err := s.reporterHash(ctx, ClientKey(addr))
	if err != nil {
		// no salt: refuse the report rather than take it half-way
		w.Header().Set("Retry-After", "10")
		http.Error(w, "reports cannot be taken right now; try again shortly", http.StatusServiceUnavailable)
		return
	}
	if s.isPriority(category) {
		s.noteBurst(ctx, NetKey(addr), docID)
	}
	reportID, err := randomString(16)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.ensureCounters(ctx)
	var rep Report
	notify := false
	needEvict, stored, err := func() (bool, string, error) {
		unlock := s.lock(docID)
		defer unlock()
		var meta docMeta
		if found, err := s.getJSON(ctx, docRecordPrefix+docID, &meta); err != nil || !found {
			return false, "", err
		}
		rep = Report{ID: reportID, ViewID: view, DocID: docID, Reason: reason, Category: category, At: s.now().UTC(), Reporter: reporter}
		id, kept, over, err := s.storeReport(ctx, &meta, rep)
		rep.Priority = s.isPriority(category)
		if kept {
			notify = true
		}
		return over, id, err
	}()
	if notify && s.notify != nil {
		go s.tell(rep)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if stored != "" {
		reportID = stored
	}
	if needEvict {
		if metas, info, err := s.listReports(context.WithoutCancel(ctx)); err == nil {
			s.evictReports(context.WithoutCancel(ctx), metas, info)
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"reportId": reportID})
}

// rewording must not split one reporter and category
func reportKey(reporter, category string) string {
	h := sha256.Sum256([]byte(reporter + "\x00" + categoryOf(category)))
	return base64.RawURLEncoding.EncodeToString(h[:9])
}

const saltRetryMax = 30 * time.Second

// never made up per process on a store failure
func (s *Service) reporterSalt(ctx context.Context) ([]byte, error) {
	s.saltMu.Lock()
	defer s.saltMu.Unlock()
	if s.salt != nil {
		return s.salt, nil
	}
	now := s.now()
	if now.Before(s.saltRetry) {
		return nil, errSaltUnavailable
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	salt, err := s.loadOrCreateSalt(ctx)
	if err != nil {
		s.saltFails++
		backoff := min(time.Second<<min(s.saltFails-1, 5), saltRetryMax)
		s.saltRetry = now.Add(backoff)
		log.Printf("public docstore: the reporter salt cannot be read or created: %s (reports are refused; next attempt in %s)", redact(err), backoff)
		return nil, errSaltUnavailable
	}
	s.salt, s.saltFails = salt, 0
	return salt, nil
}

var errSaltUnavailable = errors.New("public: the reporter salt is not available")

func (s *Service) loadOrCreateSalt(ctx context.Context) ([]byte, error) {
	valid := func(b []byte) ([]byte, bool) {
		var rec struct {
			Salt []byte `json:"salt"`
		}
		return rec.Salt, json.Unmarshal(b, &rec) == nil && len(rec.Salt) >= 16
	}
	for range 3 {
		d, err := s.store.Load(ctx, saltKey)
		prev := ""
		switch {
		case err == nil:
			if salt, ok := valid(d.Data); ok {
				return salt, nil
			}
			prev = docstore.Digest(d.Data)
		case errors.Is(err, docstore.ErrNotFound):
		default:
			return nil, err
		}
		fresh := make([]byte, 32)
		if _, err := rand.Read(fresh); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(map[string][]byte{"salt": fresh})
		c, ok := s.store.(docstore.ConditionalAdapter)
		if !ok {
			return s.saveSaltUnconditionally(ctx, raw)
		}
		switch err := c.SaveIf(ctx, saltKey, raw, prev); {
		case err == nil:
			return fresh, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		case errors.Is(err, docstore.ErrConditionalUnsupported):
			return s.saveSaltUnconditionally(ctx, raw)
		default:
			return nil, err
		}
	}
	return nil, errors.New("the reporter salt kept changing while it was being created")
}

// read back after saving: raced replicas converge on the last writer
func (s *Service) saveSaltUnconditionally(ctx context.Context, raw []byte) ([]byte, error) {
	if err := s.store.Save(ctx, saltKey, raw); err != nil {
		return nil, err
	}
	d, err := s.store.Load(ctx, saltKey)
	if err != nil {
		return nil, err
	}
	var rec struct {
		Salt []byte `json:"salt"`
	}
	if json.Unmarshal(d.Data, &rec) != nil || len(rec.Salt) < 16 {
		return nil, errors.New("the stored reporter salt is unreadable")
	}
	return rec.Salt, nil
}

// HMAC under ReportKey, never stored: no address is held
func (s *Service) reporterHash(ctx context.Context, client string) (string, error) {
	salt, err := s.reporterSalt(ctx)
	if err != nil {
		return "", err
	}
	m := hmac.New(sha256.New, s.reportKey)
	_, _ = m.Write([]byte("gojiberry/public/reporter/v1\x00"))
	_, _ = m.Write(salt)
	_, _ = m.Write([]byte{0})
	_, _ = m.Write([]byte(client))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:12]), nil
}

func (s *Service) refLive(ctx context.Context, ref reportRef) (bool, error) {
	_, err := s.store.Load(ctx, docstore.ReportPrefix+ref.ID)
	if errors.Is(err, docstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Service) pruneRefs(ctx context.Context, meta *docMeta) (bool, error) {
	kept := meta.ReportRefs[:0:0]
	for _, ref := range meta.ReportRefs {
		live, err := s.refLive(ctx, ref)
		if err != nil {
			return false, err
		}
		if live {
			kept = append(kept, ref)
		}
	}
	if len(kept) == len(meta.ReportRefs) {
		return false, nil
	}
	meta.ReportRefs = kept
	meta.Reports = len(kept)
	return true, nil
}

func (s *Service) storeReport(ctx context.Context, meta *docMeta, rep Report) (id string, stored, over bool, err error) {
	pruned := false
	defer func() {
		if pruned && !stored {
			if e := s.putJSON(ctx, docRecordPrefix+rep.DocID, *meta); e != nil {
				log.Printf("public docstore: updating a document's report list: %s", redact(e))
			}
		}
	}()
	key := reportKey(rep.Reporter, rep.Category)
	for i, ref := range meta.ReportRefs {
		if ref.Key == key {
			live, err := s.refLive(ctx, ref)
			if err != nil {
				return "", false, false, err
			}
			if live {
				return ref.ID, false, false, nil
			}
			meta.ReportRefs = append(meta.ReportRefs[:i:i], meta.ReportRefs[i+1:]...)
			meta.Reports = len(meta.ReportRefs)
			pruned = true
			break
		}
	}
	if len(meta.ReportRefs) >= maxReportsOfDoc-max(s.cfg.ReportReservedPerDoc, 0) {
		changed, err := s.pruneRefs(ctx, meta)
		if err != nil {
			return "", false, false, err
		}
		pruned = pruned || changed
	}
	priority := false
	if s.isPriority(rep.Category) {
		held := 0
		for _, ref := range meta.ReportRefs {
			if ref.Priority {
				held++
			}
		}
		priority = held < maxPriorityOfDoc && s.allowPriority(ctx, rep.Reporter)
	}
	rep.Priority = priority
	n := max(len(meta.ReportRefs), meta.Reports)
	if n >= maxReportsOfDoc {
		if !priority || !s.evictOrdinaryRef(ctx, meta) {
			return "", false, false, nil
		}
		pruned = true
	} else if !priority {
		if reserved := s.cfg.ReportReservedPerDoc; reserved > 0 && n >= maxReportsOfDoc-reserved {
			for _, ref := range meta.ReportRefs {
				if ref.Reporter == rep.Reporter {
					return "", false, false, nil
				}
			}
		}
		if pct := s.cfg.ReportReservePercent; pct > 0 {
			all, mine := s.reports.held(rep.Reporter)
			if all >= s.cfg.MaxReports-s.cfg.MaxReports*pct/100 && mine > 0 {
				return "", false, false, nil
			}
		}
	}
	raw, _ := json.Marshal(rep)
	if err := s.store.Save(ctx, docstore.ReportPrefix+rep.ID, raw); err != nil {
		if priority && s.prioQuota != nil {
			if r, ok := s.prioQuota.(QuotaRefunder); ok {
				r.Refund(ctx, "prio|"+rep.Reporter, 1)
			}
		}
		return "", false, false, err
	}
	s.reports.add(1, int64(len(raw)), rep.Reporter)
	if priority && s.prioQuota == nil {
		s.prio.record(rep.Reporter, s.now())
	}
	over = s.reports.over(s.cfg.MaxReports, s.cfg.MaxReportBytes)
	meta.ReportRefs = append(meta.ReportRefs, reportRef{ID: rep.ID, Reporter: rep.Reporter, Key: key, Priority: priority, Size: len(raw), At: rep.At})
	meta.Reports = len(meta.ReportRefs)
	if err := s.putJSON(ctx, docRecordPrefix+rep.DocID, *meta); err != nil {
		return "", false, over, err
	}
	return rep.ID, true, over, nil
}

func (s *Service) evictOrdinaryRef(ctx context.Context, meta *docMeta) bool {
	oldest := -1
	for i, ref := range meta.ReportRefs {
		if !ref.Priority && (oldest < 0 || ref.At.Before(meta.ReportRefs[oldest].At)) {
			oldest = i
		}
	}
	if oldest < 0 {
		return false
	}
	ref := meta.ReportRefs[oldest]
	if err := s.deleteKey(ctx, docstore.ReportPrefix+ref.ID); err != nil {
		log.Printf("public docstore: evicting a report: %s", redact(err))
		return false
	}
	s.reports.add(-1, -int64(ref.Size), ref.Reporter)
	meta.ReportRefs = append(meta.ReportRefs[:oldest:oldest], meta.ReportRefs[oldest+1:]...)
	meta.Reports = len(meta.ReportRefs)
	return true
}

func (s *Service) allowPriority(ctx context.Context, reporter string) bool {
	limit := s.cfg.MaxPriorityReportsPerReporter
	if limit < 0 {
		return true
	}
	if s.prioQuota != nil {
		ok, _, err := s.prioQuota.Take(ctx, "prio|"+reporter, 1)
		return ok && err == nil
	}
	return s.prio.allow(reporter, s.now(), limit)
}
