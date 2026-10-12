package public

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// AdminHandler serves DELETE /admin/takedown/<id> and GET /admin/health.
func (s *Service) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/takedown/", s.handleTakedown)
	mux.HandleFunc("/admin/health", s.handleAdminHealth)
	return s.headers(mux)
}

// ---- takedown

// adminAllowed is the admin guard in front of the admin routes. The token is checked first (in
// constant time); a request that passes is never counted and never held back, so wrong guesses
// from an address, a network or everyone behind one shared address cannot lock the operator out.
// Only failures are counted: per client (a /64, Limiters.Admin), per network (a /48 or /24,
// Limiters.AdminNet) and in all (Limiters.AdminGlobal); past the first two a failure answers 429
// with Retry-After instead of 403, and once the global budget is used up every wrong token gets 429
// for AdminCooldown. A burst of failures is logged (adminFailed).
//
// What the limits are for: they slow a guesser down, bound the work it can ask for and make a
// burst visible. They cannot make guessing safe by themselves, because a right guess is answered
// at once however many wrong ones came before it, and wrong guesses cost the guesser nothing but
// a request: the safety is the token's length, which is why the server will not start with fewer
// than MinAdminTokenLength characters (Config.AllowShortAdminToken is the explicit override).
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

// adminBurst is how many failed admin authentications in a minute are worth a log line.
const adminBurst = 10

// adminFailed counts a failed admin authentication and logs once per minute in which there were
// adminBurst or more.
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

// handleTakedown serves DELETE /admin/takedown/<id> (a view ID or a document ID): the document,
// its view ID and its records are deleted and a tombstone is written, so the view ID answers 410
// from then on and the owner's session cannot create another document. The call is idempotent
// (a second one on a removed ID is 204 too). ?reason= is kept on the tombstone.
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
		// No record names this ID: the document expired and its records were swept (its session,
		// which keeps the document ID, must still be banned), or it is being created right now.
		// The ban is the tombstone of the view ID, which the session's check derives from its
		// document ID, so it is written for both readings of the ID (as a view ID, and as a
		// document ID whose view ID follows) before anything else, and then looked up once more:
		// a create in flight sees the tombstone when it finishes (createDoc), or has left records
		// this finds now.
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
	// The tombstone first: from this write on the view ID answers 410, whatever fails below
	// (a retry finishes the deletion).
	if err := tomb(view); err != nil {
		s.fail(w, err)
		return
	}
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
