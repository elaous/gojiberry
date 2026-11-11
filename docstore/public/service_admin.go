package public

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// AdminHandler serves DELETE /admin/takedown/<id>.
func (s *Service) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/takedown/", s.handleTakedown)
	mux.HandleFunc("/admin/health", s.handleAdminHealth)
	mux.HandleFunc("/admin/reports", s.handleAdminReports)
	return s.headers(mux)
}

// token checked in constant time; passes are never counted
func (s *Service) adminAllowed(w http.ResponseWriter, r *http.Request) bool {
	if s.admin != nil && s.admin.Allow(r) {
		return true
	}
	now := s.now()
	s.adminFailed(now)
	s.adminMu.Lock()
	cool := s.adminCool
	s.adminMu.Unlock()
	if now.Before(cool) {
		tooMany(w, cool.Sub(now))
		return false
	}
	addr := s.clientIP(r)
	if !s.allow(w, r, s.lim.Admin, "admin", ClientKey(addr)) || !s.allow(w, r, s.lim.AdminNet, "adminnet", NetKey(addr)) {
		return false
	}
	if ok, _ := s.lim.AdminGlobal.Allow(r.Context(), "admin-all"); !ok {
		s.adminMu.Lock()
		first := !now.Before(s.adminCool)
		s.adminCool = now.Add(AdminCooldown)
		s.adminMu.Unlock()
		if first {
			log.Printf("public docstore: ALARM: failed admin authentications passed %v a minute in all: wrong tokens are refused for %s (the admin token is being guessed, or a client of the admin route is misconfigured)", s.cfg.AdminFailuresGlobalPerMinute, AdminCooldown)
		}
		tooMany(w, AdminCooldown)
		return false
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return false
}

const adminBurst = 10

func (s *Service) adminFailed(now time.Time) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if now.Sub(s.adminMin) >= time.Minute {
		s.adminMin, s.adminFails = now, 0
	}
	s.adminFails++
	if s.adminFails == adminBurst {
		log.Printf("public docstore: WARNING: %d failed admin authentications within a minute (the admin token may be being guessed); see the limits in section 7.2 item 18", s.adminFails)
	}
}

func (s *Service) handleTakedown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, "DELETE")
		return
	}
	if !s.adminAllowed(w, r) {
		return
	}
	ctx := r.Context()
	id := strings.TrimPrefix(r.URL.Path, "/admin/takedown/")
	if !validID(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var existing tombstone
	if gone, err := s.getJSON(ctx, tombPrefix+id, &existing); err != nil {
		s.fail(w, err)
		return
	} else if gone {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	reason := strings.ToValidUTF8(r.URL.Query().Get("reason"), "")
	if len(reason) > 200 {
		reason = strings.ToValidUTF8(reason[:200], "")
	}
	tomb := func(view string) error {
		return s.putJSON(ctx, tombPrefix+view, tombstone{At: s.now().UTC(), Reason: reason})
	}
	docID, view, found, err := s.resolve(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !found {
		if err := tomb(id); err != nil {
			s.fail(w, err)
			return
		}
		if err := tomb(viewIDOf(id)); err != nil {
			s.fail(w, err)
			return
		}
		if docID, view, found, err = s.resolve(ctx, id); err != nil {
			s.fail(w, err)
			return
		}
		if !found {
			log.Print("public docstore: takedown of an ID with no records: tombstoned (the document is gone or not yet stored)")
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}
	unlock := s.lock(docID)
	defer unlock()
	// tombstone first: view ID answers 410 whatever fails below
	if err := tomb(view); err != nil {
		s.fail(w, err)
		return
	}
	s.learnHash(ctx, docID, reason)
	if err := s.deleteKey(ctx, docKey(docID)); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.dropRecords(ctx, docID); err != nil {
		s.fail(w, err)
		return
	}
	s.bytes.release(docID)
	w.WriteHeader(http.StatusNoContent)
}
