package docstore

import (
	"github.com/elaous/gojiberry/audit"
	"net/http"
	"strings"
)

// handleEnrollment serves GET /directory/enrollment.
func (s *Server) handleEnrollment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryReader(w, r)
	if !ok {
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if !ValidAddress(email) {
		http.Error(w, "not an e-mail address", http.StatusBadRequest)
		return
	}
	if !s.readBudget(w, r, u, readLookup) {
		return
	}
	if !s.policy().CanLookup(u, email) {
		http.Error(w, "this directory does not look up that address for you", http.StatusForbidden)
		return
	}
	e, err := s.directory.Enrollment(r.Context(), email)
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, e)
}

// handleEnrollChallenge serves POST /directory/enroll/challenge.
func (s *Server) handleEnrollChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	if ok, wait := s.limits().challenge.Allow(r.Context(), "e:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	c, err := s.directory.EnrollChallenge(r.Context(), u)
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, c)
}

// handleEnroll serves POST /directory/enroll.
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	if ok, wait := s.limits().publish.Allow(r.Context(), "p:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	var body DirectoryEnroll
	if !readDirectoryBody(w, r, &body) {
		return
	}
	e, err := s.directory.Enroll(r.Context(), u, body)
	if err != nil {
		s.audit(audit.KeyEnrolled, audit.Failed, audit.Owner, audit.NotAllowed, 0, u.ID)
		directoryError(w, err)
		return
	}
	s.audit(audit.KeyEnrolled, audit.OK, audit.Owner, audit.NoReason, 0, u.ID)
	writeJSONStatus(w, http.StatusOK, e)
}

// handleReset serves POST /directory/enroll/reset.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	if ok, wait := s.limits().publish.Allow(r.Context(), "p:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	e, err := s.directory.RequestReset(r.Context(), u)
	if err != nil {
		s.audit(audit.OwnerReset, audit.Failed, audit.Owner, audit.NotAllowed, 0, u.ID)
		directoryError(w, err)
		return
	}
	s.audit(audit.OwnerReset, audit.OK, audit.Owner, audit.NoReason, 0, u.ID)
	writeJSONStatus(w, http.StatusOK, e)
}

// handleCancelReset serves POST /directory/enroll/cancel.
func (s *Server) handleCancelReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	if ok, wait := s.limits().publish.Allow(r.Context(), "p:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	var body DirectoryRevoke
	if !readDirectoryBody(w, r, &body) {
		return
	}
	e, err := s.directory.CancelReset(r.Context(), u, body.Owner)
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, e)
}

// handleEnrollSub routes requests under /directory/enroll/.
func (s *Server) handleEnrollSub(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, "/directory/enroll/") {
	case "challenge":
		s.handleEnrollChallenge(w, r)
	case "reset":
		s.handleReset(w, r)
	case "cancel":
		s.handleCancelReset(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleEnrollRoot serves the enrollment root route.
func (s *Server) handleEnrollRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.handleEnrollment(w, r)
		return
	}
	s.handleEnroll(w, r)
}
