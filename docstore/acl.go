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
	// Until is when a named person's access ends (by the same normalised identifier as Grants). A grant with
	// no entry here has no end. The server reads the list through Active, so an ended grant opens nothing.
	Until map[string]time.Time `json:"until,omitempty"`
	// Links are the share links of the document by the hash of their token (sharelinks.go).
	Links map[string]Link `json:"links,omitempty"`
}

// RoleOf returns the role name email has on this document: "owner",
// "editor" or "viewer", or "" for no access. These are the names
// canvas.Role uses, so a client can pass the answer to canvas.ParseRole.
func (a ACL) RoleOf(email string) string { return a.RoleOfPerson(user.Person{ID: email}) }

// RoleOfPerson is RoleOf for someone who can be found by more than one identifier
// (their ID or verified e-mail address; never their username, see
// user.Person.AccessIDs): the best role any of them has.
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

// canRead reports whether email (the empty string for an anonymous/
// unauthenticated caller) may GET this document -- the Owner, or
// anyone with either Role.
func (a ACL) canRead(p user.Person) bool { return a.RoleOfPerson(p) != "" }

// canWrite reports whether email may PUT/DELETE this document -- the
// Owner, or an Editor grant; a Viewer cannot.
func (a ACL) canWrite(p user.Person) bool {
	r := a.RoleOfPerson(p)
	return r == user.Owner || r == user.Editor
}

// owns reports whether the person is the owner.
func (a ACL) owns(p user.Person) bool { return a.RoleOfPerson(p) == user.Owner }

// personOf is the signed-in user as the access lists know them.
func personOf(u auth.User) user.Person { return u.Person("") }

// ownerKey is what an ACL records for an owner: the verified e-mail address when
// there is one (what people share by), else the stable ID.
func ownerKey(u auth.User) string {
	if u.Email != "" {
		return u.Email
	}
	return u.ID
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
	// Grants are looked up by normalised address; an ACL written with another
	// spelling still means the same people.
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
	// A grant that has ended opens nothing, whoever asks (the owner's list shows it gone).
	return acl.Active(s.clock()), nil
}

// methodRenew is checkPermission's name for POST /docs/{id}/renew: the permission of a write that
// never bootstraps an access list.
const methodRenew = "RENEW"

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
	if strings.HasPrefix(strings.ToLower(id), "catalog:") { // any case: a backend that folds case would alias them
		return s.checkCatalog(r, id)
	}
	acl, err := s.loadACL(r.Context(), id)
	if docID, isVersion := versionDocID(id); isVersion && errors.Is(err, ErrNotFound) {
		// the history of a document follows the document's access list (versionacl.go)
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
		// With RequireACL a document nobody owns is closed -- except to the
		// first signed-in writer, who becomes its owner.
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
		// An ACL that cannot be read (a backend error, damaged data) must
		// not open the document.
		return false, http.StatusServiceUnavailable
	}
	me, signedIn := s.auth.CurrentUser(r)
	who := user.Person{}
	if signedIn {
		who = personOf(me)
	}
	// A share link adds access for whoever holds it while it has not expired or been revoked: it can
	// read (viewer or editor link) or write (editor link), and it cannot delete.
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
	me, signedIn := s.auth.CurrentUser(r)
	if signedIn && ownerKey(me) == "" {
		// Neither a verified e-mail address nor an ID: there is nothing to own or
		// share a document by, and "" would match any other such account.
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
			// Not knowing who owns it is not a reason to let the caller own it.
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
		body.Links = nil // links are made and revoked by POST and DELETE; replacing the list keeps the ones it has
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

// UserCatalogPrefix is the part of the key space a person's catalog and folder
// records live in on a shared store: "catalog:u/" and a hash of their ID (the
// auth.User.ID), then "index" or "folder/{id}". saver.UserCatalogKey builds the
// catalog key from it, and a server with RequireACL lets a signed-in person use only
// their own.
func UserCatalogPrefix(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return "catalog:u/" + hex.EncodeToString(sum[:16]) + "/"
}

// checkCatalog decides access to the keys a saver keeps its catalog under. A catalog
// is not a document: it lists the documents one person sees, so with RequireACL each
// signed-in person may use only their own (UserCatalogPrefix), and the shared
// "catalog:index" is closed (otherwise the first writer would own everyone's list and
// everyone else would be refused). Without RequireACL the store is open, as for
// documents nobody has shared.
func (s *Server) checkCatalog(r *http.Request, id string) (allowed bool, status int) {
	if !s.requireACL {
		return true, 0
	}
	me, signedIn := s.auth.CurrentUser(r)
	if !signedIn || me.ID == "" {
		return false, http.StatusUnauthorized
	}
	if strings.HasPrefix(id, UserCatalogPrefix(me.ID)) { // exact, and ending in "/": a prefix of the hash is not the hash
		return true, 0
	}
	return false, http.StatusForbidden
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

// telemetryKeyspace is the reserved keyspace of the usage totals.
const telemetryKeyspace = "telemetry:"

// isReservedDocID reports whether id falls inside the ACL keyspace
// this package reserves for its own bookkeeping (aclKeyPrefix) --
// checked by handleDoc so a client can never PUT/GET/DELETE a real
// document directly into/out of that space.
func isReservedDocID(id string) bool {
	// "collab:" is the shared-editing hub's own keyspace; its access
	// control is the hub's, so these keys must not be reachable here.
	// "directory/" is the key directory's keyspace (see Server.WithDirectory): its records
	// are written only by the server's own publish route, so a signed-in session cannot forge
	// one through the document API. "directory-log/" (the key directory's transparency log),
	// "directory-enroll/" (owner enrollments) and "ratelimit/" (shared rate limit budgets), "auth/session/" and "auth/state/" (shared
	// sign-in sessions: a writer could forge a session) are likewise written only by the server itself.
	// "public/" and "report/" are the public offer's records and abuse reports (docstore/public).
	// "telemetry:" holds the usage totals (TelemetryKey), written only by the server's own receiver.
	// "config/" is the per-user configuration (package userconfig), reached through that package only.
	// Every reserved prefix is compared without case, since a backend that folds case would
	// alias "DIRECTORY-ENROLL/x" with the real record, as IsDocumentKey does for expiry.
	lower := strings.ToLower(id)
	for _, p := range []string{aclKeyPrefix, "collab:", DirectoryPrefix, DirectoryLogPrefix, DirectoryEnrollPrefix, RateLimitPrefix, ConfigPrefix, SessionPrefix, LoginStatePrefix, ExpiryPrefix, ExpiryCreatedPrefix, PublicPrefix, ReportPrefix, telemetryKeyspace, usageKeyspace} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// Grant gives email a role on the document (changing it if they have one). The
// owner's access cannot be changed, and an empty address or a role that is not
// RoleEditor or RoleViewer is refused.
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

// MaxGrantLife is the longest a person's access may be given for.
const MaxGrantLife = 365 * 24 * time.Hour

// GrantFor gives email a role that ends life after now (no end when life is zero). A life must be between
// MinLinkLife and MaxGrantLife. Giving a role again without a life (Grant) keeps the end already set;
// GrantFor with a zero life removes it.
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

// GrantEnds is when email's access ends; ok is false when it has no end.
func (a ACL) GrantEnds(email string) (t time.Time, ok bool) {
	t, ok = a.Until[NormalizeAddress(email)]
	return t, ok
}

// Active is the list as it stands at now: the grants whose end has passed are gone. It works on a copy.
// A grant is good strictly before its end, as a link is.
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

// Revoke takes away what email was given; the owner cannot be removed.
func (a *ACL) Revoke(email string) error {
	email = NormalizeAddress(email)
	if email == NormalizeAddress(a.Owner) {
		return errors.New("docstore: the owner cannot be removed")
	}
	delete(a.Grants, email)
	delete(a.Until, email)
	return nil
}

// People lists who has access: the owner first, then the others by address.
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

// NormalizeAddress is how an address is kept and compared in an ACL (see user.Normalize).
func NormalizeAddress(email string) string { return user.Normalize(email) }

// normalizedGrants is grants with each identifier as Normalize writes it, and only
// valid roles.
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

// normalizedUntil keeps the ends that belong to a grant that is kept, written with Normalize; an end
// further than MaxGrantLife away is cut to it.
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
