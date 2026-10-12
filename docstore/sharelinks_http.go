package docstore

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/elaous/gojiberry/auth"
)

// createLinkRequest is the body of POST /docs/{id}/acl.
type createLinkRequest struct {
	Role Role `json:"role"`
	// Hours is how long the link lasts (DefaultLinkLife when 0; at most MaxLinkLife).
	Hours float64 `json:"hours"`
}

// createLinkResponse is what POST /docs/{id}/acl answers: the token is shown here and nowhere else.
type createLinkResponse struct {
	Token   string    `json:"token"`
	ID      string    `json:"id"`
	Role    Role      `json:"role"`
	Expires time.Time `json:"expires"`
}

// owned loads id's access list for a change by its owner. A document nobody shared yet gets one owned by
// the caller (the same first-use rule as PUT /docs/{id}/acl). It answers false after writing the error.
func (s *Server) owned(w http.ResponseWriter, r *http.Request, id string, me auth.User, signedIn bool) (ACL, bool) {
	if !signedIn {
		http.Error(w, "sign in required", http.StatusUnauthorized)
		return ACL{}, false
	}
	acl, err := s.loadACL(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		return ACL{Owner: ownerKey(me)}, true
	case err != nil:
		http.Error(w, "the access list cannot be read", http.StatusServiceUnavailable)
		return ACL{}, false
	case !acl.owns(personOf(me)):
		http.Error(w, "forbidden", http.StatusForbidden)
		return ACL{}, false
	}
	return acl, true
}

func (s *Server) saveACL(w http.ResponseWriter, r *http.Request, id string, acl ACL) bool {
	data, err := json.Marshal(acl)
	if err == nil {
		err = s.adapter.Save(r.Context(), aclKey(id), data)
	}
	if err != nil {
		serverError(w, err)
		return false
	}
	return true
}

// handleCreateLink serves POST /docs/{id}/acl: the owner makes a share link with a role and a lifetime.
func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request, id string, me auth.User, signedIn bool) {
	var body createLinkRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
		http.Error(w, "not a link request", http.StatusBadRequest)
		return
	}
	if body.Hours < 0 || body.Hours > MaxLinkLife.Hours() {
		http.Error(w, "a link lasts between a minute and 90 days", http.StatusBadRequest)
		return
	}
	token, err := NewShareToken()
	if err != nil {
		serverError(w, err)
		return
	}
	s.aclMu.Lock()
	defer s.aclMu.Unlock()
	acl, ok := s.owned(w, r, id, me, signedIn)
	if !ok {
		return
	}
	now := s.clock()
	linkID, link, err := acl.AddLink(token, body.Role, now, time.Duration(body.Hours*float64(time.Hour)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.saveACL(w, r, id, acl) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(createLinkResponse{Token: token, ID: linkID, Role: link.Role, Expires: link.Expires})
}

// handleRevokeLink serves DELETE /docs/{id}/acl?link={id}: the owner ends a link at once.
func (s *Server) handleRevokeLink(w http.ResponseWriter, r *http.Request, id string, me auth.User, signedIn bool) {
	linkID := r.URL.Query().Get("link")
	if linkID == "" {
		http.Error(w, "name the link to revoke (?link=)", http.StatusBadRequest)
		return
	}
	s.aclMu.Lock()
	defer s.aclMu.Unlock()
	acl, ok := s.owned(w, r, id, me, signedIn)
	if !ok {
		return
	}
	if !acl.RevokeLink(linkID) {
		http.Error(w, "no such link", http.StatusNotFound)
		return
	}
	if !s.saveACL(w, r, id, acl) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
