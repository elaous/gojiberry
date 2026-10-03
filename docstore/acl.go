package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Role is a collaborator's own access level on a document, beyond its
// Owner (who always has full access, including managing the ACL
// itself).
type Role string

const (
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// ACL is one document's own access-control list. Stored as an
// ordinary document of its own, under aclKey(id), through the exact
// same Adapter every real document already goes through -- no new
// backend-specific code needed for any of Memory/LocalFS/Postgres/
// Mongo/S3 to support it, the same "adapters, not vendor-specific
// code, for anything storage-shaped" principle this package's own
// Adapter interface already follows.
//
// A document with no ACL stored at all (the common case -- nobody has
// ever shared it this way) is fully open: every route below behaves
// exactly as it did before this file existed. Enforcement only starts
// once an Owner actually sets one up, via PUT /docs/{id}/acl.
type ACL struct {
	Owner  string          `json:"owner"`
	Grants map[string]Role `json:"grants,omitempty"`
}

// canRead reports whether email (the empty string for an anonymous/
// unauthenticated caller) may GET this document -- the Owner, or
// anyone with either Role.
func (a ACL) canRead(email string) bool {
	if email == "" {
		return false
	}
	if email == a.Owner {
		return true
	}
	_, ok := a.Grants[email]
	return ok
}

// canWrite reports whether email may PUT/DELETE this document -- the
// Owner, or an Editor grant; a Viewer cannot.
func (a ACL) canWrite(email string) bool {
	if email == "" {
		return false
	}
	if email == a.Owner {
		return true
	}
	return a.Grants[email] == RoleEditor
}

// aclKeyPrefix marks a reserved region of the document-ID keyspace
// this package's own Adapter stores each ACL under -- handleDoc
// rejects any real document ID with this prefix outright (so a client
// can never write directly into it), and handleList filters it out of
// /docs's own listing, so an ACL never masquerades as a real document.
const aclKeyPrefix = "acl:"

func aclKey(id string) string {
	return aclKeyPrefix + id
}

// loadACL returns id's own ACL, or (ACL{}, ErrNotFound) if nobody has
// ever shared it this way.
func (s *Server) loadACL(ctx context.Context, id string) (ACL, error) {
	doc, err := s.adapter.Load(ctx, aclKey(id))
	if err != nil {
		return ACL{}, err
	}
	var acl ACL
	if err := json.Unmarshal(doc.Data, &acl); err != nil {
		return ACL{}, err
	}
	return acl, nil
}

// checkPermission enforces id's own ACL (if any) against r's caller
// for the given HTTP method (GET/PUT/DELETE) -- the gate every
// /docs/{id} handler in server.go runs its real work behind. Entirely
// a no-op (always allowed) when this Server has no auth.Manager
// configured at all: without one, no request can ever resolve to a
// real identity, so there's nothing meaningful to check an ACL
// against -- ACL enforcement is opt-in exactly the way AuthConfig
// itself already is (see its own doc comment). Same for a document
// with no ACL stored: fully open, unaffected by this feature.
func (s *Server) checkPermission(r *http.Request, id, method string) (allowed bool, status int) {
	if s.auth == nil {
		return true, 0
	}
	acl, err := s.loadACL(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		return true, 0 // never shared this way: open
	}
	if err != nil {
		// An ACL that cannot be read (a backend error, damaged data) must
		// not open the document.
		return false, http.StatusServiceUnavailable
	}
	user, signedIn := s.auth.CurrentUser(r)
	email := ""
	if signedIn {
		email = user.Email
	}
	switch method {
	case http.MethodGet:
		if acl.canRead(email) {
			return true, 0
		}
	case http.MethodPut, http.MethodDelete:
		if acl.canWrite(email) {
			return true, 0
		}
	}
	if !signedIn {
		return false, http.StatusUnauthorized
	}
	return false, http.StatusForbidden
}

// handleACL serves GET/PUT /docs/{id}/acl -- id already has both the
// aclKeyPrefix guard (handleDoc, before routing here) and its own
// "/acl" suffix stripped by the caller.
//
// GET returns an empty ACL{} (Owner "") for a document nobody has
// shared yet, rather than 404 -- the client's own "not shared" state
// is a real, first-class result here, not an error. PUT bootstraps
// ownership on first use: whatever Owner the request body carries is
// always overwritten with the caller's own identity, never trusted
// from the client, so nobody can PUT an ACL claiming someone else
// owns it. Once an ACL exists, only its own Owner may GET or replace
// it -- a Viewer/Editor grant lets someone read/write the document
// itself, not manage who else can.
func (s *Server) handleACL(w http.ResponseWriter, r *http.Request, id string) {
	if s.auth == nil {
		http.Error(w, "docstore: auth is not configured on this server", http.StatusNotImplemented)
		return
	}
	user, signedIn := s.auth.CurrentUser(r)
	if signedIn && user.Email == "" {
		// No (verified) e-mail address: there is nothing to own or share
		// a document by, and "" would match any other such account.
		http.Error(w, "your account has no verified e-mail address", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		acl, err := s.loadACL(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			if !signedIn {
				http.Error(w, "sign in required", http.StatusUnauthorized)
				return
			}
			writeACLJSON(w, ACL{})
			return
		}
		if err != nil {
			http.Error(w, "the access list cannot be read", http.StatusServiceUnavailable)
			return
		}
		if !signedIn || acl.Owner != user.Email {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeACLJSON(w, acl)
	case http.MethodPut:
		if !signedIn {
			http.Error(w, "sign in required", http.StatusUnauthorized)
			return
		}
		var body ACL
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		existing, err := s.loadACL(r.Context(), id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			// Not knowing who owns it is not a reason to let the caller own it.
			http.Error(w, "the access list cannot be read", http.StatusServiceUnavailable)
			return
		}
		if err == nil && existing.Owner != user.Email {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		body.Owner = user.Email
		data, err := json.Marshal(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.adapter.Save(r.Context(), aclKey(id), data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// writePermissionError reports checkPermission's own result --
// distinct messages for "you're not signed in at all" (401) versus
// "you are, but this ACL doesn't grant you access" (403), rather than
// a flat "forbidden" for both.
func writePermissionError(w http.ResponseWriter, status int) {
	if status == http.StatusUnauthorized {
		http.Error(w, "sign in required", status)
		return
	}
	http.Error(w, "forbidden", status)
}

func writeACLJSON(w http.ResponseWriter, acl ACL) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(acl) // a write failure here means the client disconnected -- nothing actionable to do about it
}

// isReservedDocID reports whether id falls inside the ACL keyspace
// this package reserves for its own bookkeeping (aclKeyPrefix) --
// checked by handleDoc so a client can never PUT/GET/DELETE a real
// document directly into/out of that space.
func isReservedDocID(id string) bool {
	return strings.HasPrefix(id, aclKeyPrefix)
}
