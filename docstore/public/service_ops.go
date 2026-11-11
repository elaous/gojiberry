package public

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// blockedPrefix is where the SHA-256 of a taken-down document is kept.
const blockedPrefix = docstore.PublicPrefix + "blocked/"

type blockedRecord struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

// chainBlocklist asks each list in turn; the first that blocks wins.
type chainBlocklist []Blocklist

// Blocked implements Blocklist.
func (c chainBlocklist) Blocked(ctx context.Context, content []byte) (bool, error) {
	for _, b := range c {
		if bad, err := b.Blocked(ctx, content); bad || err != nil {
			return bad, err
		}
	}
	return false, nil
}

// loadTaken reads the digests of the documents taken down.
func (s *Service) loadTaken(ctx context.Context) error {
	metas, err := docstore.ListPrefix(ctx, s.store, blockedPrefix)
	if err != nil {
		return err
	}
	for _, m := range metas {
		s.taken.Add(strings.TrimPrefix(m.ID, blockedPrefix))
	}
	return nil
}

// learnHash remembers the content of a document that was taken down.
func (s *Service) learnHash(ctx context.Context, docID, reason string) {
	d, err := s.store.Load(ctx, docKey(docID))
	if err != nil {
		return
	}
	sum := sha256.Sum256(d.Data)
	h := hex.EncodeToString(sum[:])
	s.taken.Add(h)
	if err := s.putJSON(ctx, blockedPrefix+h, blockedRecord{At: s.now().UTC(), Reason: reason}); err != nil {
		log.Printf("public docstore: keeping the digest of a document taken down: %s (it is refused until a restart)", redact(err))
	}
}

// tell hands a kept report to Options.Notify.
func (s *Service) tell(rep Report) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("public docstore: the report notifier panicked: %v", r)
		}
	}()
	s.notify(rep)
}

// ReportListMax is the most reports one GET /admin/reports returns.
const ReportListMax = 200

// handleAdminReports serves GET /admin/reports.
func (s *Service) handleAdminReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if !s.adminAllowed(w, r) {
		return
	}
	ctx := r.Context()
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			http.Error(w, "limit must be a number from 1", http.StatusBadRequest)
			return
		}
		limit = min(n, ReportListMax)
	}
	category := r.URL.Query().Get("category")
	metas, err := docstore.ListPrefix(ctx, s.store, docstore.ReportPrefix)
	if err != nil {
		s.fail(w, err)
		return
	}
	var reps []Report
	for _, m := range metas {
		var rep Report
		if found, err := s.getJSON(ctx, m.ID, &rep); err != nil {
			s.fail(w, err)
			return
		} else if found && (category == "" || rep.Category == category) {
			reps = append(reps, rep)
		}
	}
	slices.SortStableFunc(reps, func(a, b Report) int {
		if a.Priority != b.Priority {
			if a.Priority {
				return -1
			}
			return 1
		}
		return b.At.Compare(a.At)
	})
	total := len(reps)
	if len(reps) > limit {
		reps = reps[:limit]
	}
	if reps == nil {
		reps = []Report{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "reports": reps})
}

// WebhookNotify returns an Options.Notify that posts to a webhook.
func WebhookNotify(url string) (func(Report), error) {
	u, err := neturl.Parse(url)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("public: the report webhook %q is not an address", url)
	}
	if u.Scheme != "https" {
		host := u.Hostname()
		if u.Scheme != "http" || (host != "localhost" && host != "127.0.0.1" && host != "::1") {
			return nil, errors.New("public: the report webhook must be https")
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	return func(rep Report) {
		b, _ := json.Marshal(map[string]any{"id": rep.ID, "view": rep.ViewID, "category": rep.Category, "priority": rep.Priority, "at": rep.At})
		resp, err := client.Post(url, "application/json", bytes.NewReader(b))
		if err != nil {
			log.Printf("public docstore: the report webhook: %s", redact(err))
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			log.Printf("public docstore: the report webhook answered %d", resp.StatusCode)
		}
	}, nil
}
