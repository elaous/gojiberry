package docstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/audit"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/user"
)

// Role is a collaborator's access level on a document.
type Role string

const (
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

// ACL is one document's access-control list.
type ACL struct {
	Owner  string          `json:"owner"`
	Grants map[string]Role `json:"grants,omitempty"`
	// Until is when a named person's access ends.
	Until map[string]time.Time `json:"until,omitempty"`
	// Links are the document's share links.
	Links map[string]Link `json:"links,omitempty"`
}

// RoleOf returns the role email has on this document.
func (a ACL) RoleOf(email string) string { return a.RoleOfPerson(user.Person{ID: email}) }

// RoleOfPerson is RoleOf for a person.
func (a ACL) RoleOfPerson(p user.Person) string {
	ids := p.AccessIDs()
	owner := user.Normalize(a.Owner)
	role := ""
	for _, id := range ids {
		switch {
		case id == owner:
			return user.Owner
		case a.Grants[id] == RoleEditor:
			role = string(RoleEditor)
		case a.Grants[id] == RoleViewer && role == "":
			role = string(RoleViewer)
		}
	}
	return role
}

func (a ACL) canRead(p user.Person) bool { return a.RoleOfPerson(p) != "" }

func (a ACL) canWrite(p user.Person) bool {
	r := a.RoleOfPerson(p)
	return r == user.Owner || r == user.Editor
}

func (a ACL) owns(p user.Person) bool { return a.RoleOfPerson(p) == user.Owner }

func personOf(u auth.User) user.Person { return u.Person("") }

func ownerKey(u auth.User) string {
	if u.Email != "" {
		return u.Email
	}
	return u.ID
}

// reserved keyspace: never reachable through the document API
const aclKeyPrefix = "acl:"

func aclKey(id string) string {
	return aclKeyPrefix + id
}

func (s *Server) loadACL(ctx context.Context, id string) (ACL, error) {
	doc, err := s.adapter.Load(ctx, aclKey(id))
	if err != nil {
		return ACL{}, err
	}
	var acl ACL
	if err := json.Unmarshal(doc.Data, &acl); err != nil {
		return ACL{}, err
	}
	for k, v := range acl.Grants {
		if n := NormalizeAddress(k); n != k {
			delete(acl.Grants, k)
			acl.Grants[n] = v
		}
	}
	for k, v := range acl.Until {
		if n := NormalizeAddress(k); n != k {
			delete(acl.Until, k)
			acl.Until[n] = v
		}
	}
	return acl.Active(s.clock()), nil
}

const methodRenew = "RENEW"

func (s *Server) checkPermission(r *http.Request, id, method string) (allowed bool, status int) {
	if s.auth == nil {
		return true, 0
	}
	if strings.HasPrefix(strings.ToLower(id), "catalog:") {
		// a backend that folds case would alias them
		return s.checkCatalog(r, id)
	}
	acl, err := s.loadACL(r.Context(), id)
	if docID, isVersion := versionDocID(id); isVersion && errors.Is(err, ErrNotFound) {
		var found bool
		if acl, found, err = s.versionACL(r.Context(), docID); found {
			err = nil
		} else if err == nil {
			err = ErrNotFound
		}
	}
	if errors.Is(err, ErrNotFound) {
		if !s.requireACL {
			return true, 0 // never shared this way: open
		}
		me, signedIn := s.auth.CurrentUser(r)
		if !signedIn || ownerKey(me) == "" {
			return false, http.StatusUnauthorized
		}
		if method != http.MethodPut {
			return false, http.StatusForbidden
		}
		data, merr := json.Marshal(ACL{Owner: ownerKey(me)})
		if merr != nil || s.adapter.Save(r.Context(), aclKey(id), data) != nil {
			return false, http.StatusServiceUnavailable
		}
		s.audit(audit.DocumentCreated, audit.OK, audit.Owner, audit.NoReason, 0, id)
		return true, 0
	}
	if err != nil {
		return false, http.StatusServiceUnavailable
	}
	me, signedIn := s.auth.CurrentUser(r)
	who := user.Person{}
	if signedIn {
		who = personOf(me)
	}
	link := acl.LinkRole(r.Header.Get(ShareHeader), s.clock())
	switch method {
	case http.MethodGet:
		if acl.canRead(who) || link != "" {
			return true, 0
		}
	case http.MethodPut, methodRenew:
		if acl.canWrite(who) || link == RoleEditor {
			return true, 0
		}
	case http.MethodDelete:
		if acl.canWrite(who) {
			return true, 0
		}
	}
	if !signedIn {
		return false, http.StatusUnauthorized
	}
	return false, http.StatusForbidden
}

func (s *Server) handleACL(w http.ResponseWriter, r *http.Request, id string) {
	if s.auth == nil {
		http.Error(w, "docstore: auth is not configured on this server", http.StatusNotImplemented)
		return
	}
	me, signedIn := s.auth.CurrentUser(r)
	if signedIn && ownerKey(me) == "" {
		// no verified e-mail or ID: "" would match any such account
		http.Error(w, "your account has no identity to share a document by", http.StatusForbidden)
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
		if !signedIn || !acl.owns(personOf(me)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeACLJSON(w, acl)
	case http.MethodPut:
		if !signedIn {
			http.Error(w, "sign in required", http.StatusUnauthorized)
			return
		}
		s.aclMu.Lock()
		defer s.aclMu.Unlock()
		var body ACL
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		existing, err := s.loadACL(r.Context(), id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			http.Error(w, "the access list cannot be read", http.StatusServiceUnavailable)
			return
		}
		if err == nil && !existing.owns(personOf(me)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		body.Owner = ownerKey(me)
		if err == nil {
			body.Owner = existing.Owner // the owner stays the owner
		}
		body.Grants = normalizedGrants(body.Grants)
		body.Until = normalizedUntil(body.Until, body.Grants, s.clock())
		body.Links = nil
		if err == nil {
			body.Links = existing.Links
		}
		data, err := json.Marshal(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := s.adapter.Save(r.Context(), aclKey(id), data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.auditGrants(r, id, existing.Grants, body.Grants)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		s.handleCreateLink(w, r, id, me, signedIn)
	case http.MethodDelete:
		s.handleRevokeLink(w, r, id, me, signedIn)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// UserCatalogPrefix is the key prefix of a user's catalog.
func UserCatalogPrefix(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return "catalog:u/" + hex.EncodeToString(sum[:16]) + "/"
}

func (s *Server) checkCatalog(r *http.Request, id string) (allowed bool, status int) {
	if !s.requireACL {
		return true, 0
	}
	me, signedIn := s.auth.CurrentUser(r)
	if !signedIn || me.ID == "" {
		return false, http.StatusUnauthorized
	}
	if strings.HasPrefix(id, UserCatalogPrefix(me.ID)) {
		return true, 0
	}
	return false, http.StatusForbidden
}

func writePermissionError(w http.ResponseWriter, status int) {
	if status == http.StatusUnauthorized {
		http.Error(w, "sign in required", status)
		return
	}
	http.Error(w, "forbidden", status)
}

func writeACLJSON(w http.ResponseWriter, acl ACL) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(acl)
}

const telemetryKeyspace = "telemetry:"

func isReservedDocID(id string) bool {
	// hub and directory keyspaces: never reachable through the document API
	lower := strings.ToLower(id)
	for _, p := range []string{aclKeyPrefix, "collab:", DirectoryPrefix, DirectoryLogPrefix, DirectoryEnrollPrefix, RateLimitPrefix, ConfigPrefix, SessionPrefix, LoginStatePrefix, ExpiryPrefix, ExpiryCreatedPrefix, PublicPrefix, ReportPrefix, telemetryKeyspace, usageKeyspace} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// Grant gives email a role on the document.
func (a *ACL) Grant(email string, role Role) error {
	email = NormalizeAddress(email)
	switch {
	case strings.TrimSpace(email) == "":
		return errors.New("docstore: no address to share with")
	case email == NormalizeAddress(a.Owner):
		return errors.New("docstore: the owner's access cannot be changed")
	case role != RoleEditor && role != RoleViewer:
		return errors.New("docstore: a role is editor or viewer")
	}
	if a.Grants == nil {
		a.Grants = map[string]Role{}
	}
	a.Grants[email] = role
	return nil
}

// MaxGrantLife is the longest a person's access may last.
const MaxGrantLife = 365 * 24 * time.Hour

// GrantFor gives email a role that ends after life.
func (a *ACL) GrantFor(email string, role Role, now time.Time, life time.Duration) error {
	if life != 0 && (life < MinLinkLife || life > MaxGrantLife) {
		return errors.New("docstore: access can be given for between a minute and a year")
	}
	if err := a.Grant(email, role); err != nil {
		return err
	}
	email = NormalizeAddress(email)
	if life == 0 {
		delete(a.Until, email)
		return nil
	}
	if a.Until == nil {
		a.Until = map[string]time.Time{}
	}
	a.Until[email] = now.Add(life).UTC().Truncate(time.Second)
	return nil
}

func (a ACL) GrantEnds(email string) (t time.Time, ok bool) {
	t, ok = a.Until[NormalizeAddress(email)]
	return t, ok
}

// Active is the list as it stands now: unexpired grants.
func (a ACL) Active(now time.Time) ACL {
	if len(a.Until) == 0 {
		return a
	}
	out := a
	out.Grants = make(map[string]Role, len(a.Grants))
	out.Until = make(map[string]time.Time, len(a.Until))
	for k, v := range a.Grants {
		if end, ok := a.Until[k]; ok && !now.Before(end) {
			continue
		}
		out.Grants[k] = v
		if end, ok := a.Until[k]; ok {
			out.Until[k] = end
		}
	}
	if len(out.Until) == 0 {
		out.Until = nil
	}
	return out
}

// Revoke takes away what email was given.
func (a *ACL) Revoke(email string) error {
	email = NormalizeAddress(email)
	if email == NormalizeAddress(a.Owner) {
		return errors.New("docstore: the owner cannot be removed")
	}
	delete(a.Grants, email)
	delete(a.Until, email)
	return nil
}

// People lists who has access, the owner first.
func (a ACL) People() []user.Person {
	out := []user.Person{{ID: a.Owner, Role: user.Owner}}
	var rest []string
	for e := range a.Grants {
		rest = append(rest, e)
	}
	sort.Strings(rest)
	for _, e := range rest {
		out = append(out, user.Person{ID: e, Role: string(a.Grants[e])})
	}
	return out
}

// NormalizeAddress is how an address is kept and compared.
func NormalizeAddress(email string) string { return user.Normalize(email) }

func normalizedGrants(in map[string]Role) map[string]Role {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]Role, len(in))
	for k, v := range in {
		if n := NormalizeAddress(k); n != "" && (v == RoleEditor || v == RoleViewer) {
			out[n] = v
		}
	}
	return out
}

func normalizedUntil(in map[string]time.Time, grants map[string]Role, now time.Time) map[string]time.Time {
	var out map[string]time.Time
	for k, v := range in {
		n := NormalizeAddress(k)
		if _, ok := grants[n]; !ok || v.IsZero() {
			continue
		}
		if limit := now.Add(MaxGrantLife); v.After(limit) {
			v = limit
		}
		if out == nil {
			out = map[string]time.Time{}
		}
		out[n] = v.UTC().Truncate(time.Second)
	}
	return out
}
