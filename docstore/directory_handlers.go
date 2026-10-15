package docstore

import (
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/audit"
	"github.com/elaous/gojiberry/auth"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) directoryUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := s.auth.CurrentUser(r)
	if !ok {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return auth.User{}, false
	}
	return u, true
}

// directoryPublisher is directoryUser for the routes that act on the user's own address: only a
// provider session with a verified e-mail can vouch for one.
func (s *Server) directoryPublisher(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := s.directoryUser(w, r)
	if !ok {
		return auth.User{}, false
	}
	if !u.VerifiedEmail || strings.TrimSpace(u.Email) == "" || u.ID == "" || strings.HasPrefix(u.ID, "passkey:") {
		http.Error(w, "only a signed-in account with a verified e-mail can publish a key", http.StatusForbidden)
		return auth.User{}, false
	}
	return u, true
}

// directoryError maps what a DirectoryService returned to a status.
func directoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDirectoryRefused):
		http.Error(w, "the directory refuses this publisher", http.StatusForbidden)
	case errors.Is(err, ErrDirectoryBadCosignature):
		http.Error(w, "the cosignature is not valid", http.StatusForbidden)
	case errors.Is(err, ErrDirectoryBadKey):
		http.Error(w, "not a public key", http.StatusBadRequest)
	case errors.Is(err, ErrDirectoryBadAddress):
		http.Error(w, "not an e-mail address", http.StatusBadRequest)
	case errors.Is(err, ErrDirectoryProofRequired):
		http.Error(w, "proof of possession required: POST /directory/challenge first", http.StatusPreconditionRequired)
	case errors.Is(err, ErrDirectoryEnrollmentRequired):
		http.Error(w, "enroll an owner key for this address first", http.StatusPreconditionRequired)
	case errors.Is(err, ErrDirectoryBadProof):
		http.Error(w, "the proof of possession is not valid", http.StatusForbidden)
	case errors.Is(err, ErrDirectoryBadAttestation):
		http.Error(w, "the attestation is not valid", http.StatusBadRequest)
	case errors.Is(err, ErrDirectoryOwnerRequired):
		http.Error(w, "this address has an owner key: sign the operation with it", http.StatusPreconditionRequired)
	case errors.Is(err, ErrDirectoryBadOwnerSig):
		http.Error(w, "the owner signature is not valid", http.StatusForbidden)
	case errors.Is(err, ErrDirectoryEnrolled), errors.Is(err, ErrDirectoryNotEnrolled), errors.Is(err, ErrDirectoryResetWait):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrDirectoryNotFound):
		http.Error(w, "no such published key", http.StatusNotFound)
	case errors.Is(err, ErrDirectoryRevoked):
		http.Error(w, "this key was revoked and cannot be published again yet", http.StatusConflict)
	case errors.Is(err, ErrDirectoryBusy):
		w.Header().Set("Retry-After", "2")
		http.Error(w, "the directory is busy, try again", http.StatusServiceUnavailable)
	default:
		serverError(w, err)
	}
}

func readDirectoryBody(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDirectoryBody))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
		return false
	}
	if json.Unmarshal(data, v) != nil {
		http.Error(w, "expected a JSON body", http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	if ok, wait := s.limits().challenge.Allow(r.Context(), "c:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if !readDirectoryBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Key) == "" {
		http.Error(w, "expected {\"key\": \"<public key>\"}", http.StatusBadRequest)
		return
	}
	c, err := s.directory.Challenge(r.Context(), u, strings.TrimSpace(body.Key))
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, c)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
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
	var body DirectoryPublish
	if !readDirectoryBody(w, r, &body) {
		return
	}
	body.Key = strings.TrimSpace(body.Key)
	if body.Key == "" {
		http.Error(w, "expected {\"key\": \"<public key>\"}", http.StatusBadRequest)
		return
	}
	if body.Email != "" && NormalizeAddress(body.Email) != NormalizeAddress(u.Email) {
		http.Error(w, "a key can be published only for your own address", http.StatusForbidden)
		return
	}
	k, err := s.directory.Publish(r.Context(), u, body)
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, k)
}

// DirectoryFingerprintSegment is a fingerprint ("SHA256:ab+/c") as a path segment: "/" becomes
// "_", "+" becomes "-" and ":" becomes "-". It is how a record is named and the form
// DELETE /directory/keys/<fingerprint> is best called with (an encoded "/" would be cleaned out
// of the URL path by servers).
func DirectoryFingerprintSegment(fp string) string {
	return strings.NewReplacer("/", "_", "+", "-", ":", "-").Replace(fp)
}

// handleRevoke serves DELETE /directory/keys/<fingerprint>. The fingerprint is the one lookup
// shows ("SHA256:...") or its DirectoryFingerprintSegment form. The body is optional: the
// owner's signature ({"owner": {...}}), which an address with an owner key needs.
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	fp := strings.TrimPrefix(r.URL.Path, "/directory/keys/")
	if fp == "" || len(fp) > 200 {
		http.Error(w, "expected /directory/keys/<fingerprint>", http.StatusBadRequest)
		return
	}
	if ok, wait := s.limits().publish.Allow(r.Context(), "p:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	var body DirectoryRevoke
	if r.ContentLength != 0 && r.Body != nil {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDirectoryBody))
		if err != nil {
			http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
			return
		}
		if len(strings.TrimSpace(string(data))) > 0 && json.Unmarshal(data, &body) != nil {
			http.Error(w, "expected a JSON body", http.StatusBadRequest)
			return
		}
	}
	k, err := s.directory.Revoke(r.Context(), u, fp, body.Owner)
	if err != nil {
		s.audit(audit.KeyRevoked, audit.Failed, audit.Owner, audit.NotAllowed, 0, u.ID)
		directoryError(w, err)
		return
	}
	s.audit(audit.KeyRevoked, audit.OK, audit.Owner, audit.NoReason, 0, u.ID)
	writeJSONStatus(w, http.StatusOK, k)
}

// directoryReader is directoryUser for the routes that read the directory: only a provider
// session may (a passkey account's address was typed by whoever made it, and nothing vouches for
// the account, so it is not a directory reader).
func (s *Server) directoryReader(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := s.directoryUser(w, r)
	if !ok {
		return auth.User{}, false
	}
	if strings.HasPrefix(u.ID, "passkey:") {
		http.Error(w, "only a signed-in provider account can read the directory", http.StatusForbidden)
		return auth.User{}, false
	}
	return u, true
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// readBudget spends the budgets of a read of the directory (a lookup, or the log): the user's own
// first, and only then the shared ones (per client IP, and for the whole server or, with
// WithDirectoryGlobalPerDomain, the caller's e-mail domain), so a user who is over their own
// limit is refused without taking a token from the budget everybody shares. The log has budgets of its own,
// apart from the lookups'. It answers 429 itself and returns false when a budget is spent.
func (s *Server) readBudget(w http.ResponseWriter, r *http.Request, u auth.User, kind readKind) bool {
	lim := s.limits()
	all := "all"
	if s.dirDomainBudget {
		all = "all:" + addressDomain(u.Email)
	}
	own, ownKey, ip, ipKey, shared := lim.lookup, "l:", lim.lookupIP, "li:", lim.lookupAll[lookupShard(u.ID)]
	if kind == readLog {
		own, ownKey, ip, ipKey, shared = lim.logRead, "g:", lim.logIP, "gi:", lim.logAll[lookupShard(u.ID)]
	}
	for _, c := range []struct {
		l   Limiter
		key string
	}{{own, ownKey + u.ID}, {ip, ipKey + s.clientAddr(r)}, {shared, all}} {
		if ok, wait := c.l.Allow(r.Context(), c.key); !ok {
			tooMany(w, wait)
			return false
		}
	}
	return true
}

// lookupShard picks the sub-budget of the whole-server lookup budget a user spends from.
func lookupShard(userID string) int {
	h := fnv.New32a()
	h.Write([]byte(userID))
	return int(h.Sum32() % lookupShards)
}

func (s *Server) policy() DirectoryPolicy {
	if s.dirPolicy == nil {
		return AllowAllLookups
	}
	return s.dirPolicy
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryReader(w, r)
	if !ok {
		return
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	if !ValidAddress(email) {
		http.Error(w, "not an e-mail address", http.StatusBadRequest)
		return
	}
	if !s.readBudget(w, r, u, readLookup) {
		return
	}
	// The policy is asked after the budgets, so that probing a refused address costs budget too.
	// Its answer does not depend on whether the address has keys, so a refusal leaks nothing.
	if !s.policy().CanLookup(u, email) {
		http.Error(w, "this directory does not look up that address for you", http.StatusForbidden)
		return
	}
	revoked := r.URL.Query().Get("revoked") == "1"
	keys, err := s.directory.Lookup(r.Context(), email, revoked)
	if err != nil {
		serverError(w, err)
		return
	}
	if len(keys) > maxLookupKeys {
		keys = keys[:maxLookupKeys]
	}
	if keys == nil {
		keys = []DirectoryKey{}
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"keys": keys})
}

// logReader is what the log routes check: a provider session (like lookup), the policy (asked
// about the user's own address: the log names no address in the clear, so the question is whether
// this user is a reader of this directory at all) and the per-user, per-IP and global budgets.
func (s *Server) logReader(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := s.directoryReader(w, r)
	if !ok {
		return auth.User{}, false
	}
	if !s.readBudget(w, r, u, readLog) {
		return auth.User{}, false
	}
	if !s.policy().CanLookup(u, u.Email) {
		http.Error(w, "this directory does not show its log to you", http.StatusForbidden)
		return auth.User{}, false
	}
	return u, true
}

// handleLog serves GET /directory/log?since=<seq>: the entries after seq since (at most 200;
// ask again from the last number for more). The whole log goes to the operator and to the owner
// of an enrolled address; everybody else gets the entries about their own address only.
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "since is a log number", http.StatusBadRequest)
			return
		}
		since = n
	}
	u, ok := s.logReader(w, r)
	if !ok {
		return
	}
	full := s.dirAdmin != nil && s.dirAdmin(r)
	if !full && u.VerifiedEmail && ValidAddress(u.Email) && !strings.HasPrefix(u.ID, "passkey:") {
		// Only a verified address can claim an enrollment (an unverified e-mail is whatever the
		// provider's user typed).
		enr, err := s.directory.Enrollment(r.Context(), u.Email)
		if err != nil {
			directoryError(w, err)
			return
		}
		full = enr.Enrolled
	}
	if !full {
		s.handleOwnLog(w, r, u, since)
		return
	}
	l, err := s.directory.Log(r.Context(), since, maxLogEntries)
	if err != nil {
		directoryError(w, err)
		return
	}
	if len(l.Entries) > maxLogEntries {
		l.Entries = l.Entries[:maxLogEntries]
	}
	if l.Entries == nil {
		l.Entries = []DirectoryLogEntry{}
	}
	writeJSONStatus(w, http.StatusOK, l)
}

// handleOwnLog answers the log read of a caller who may see only their own address's entries.
func (s *Server) handleOwnLog(w http.ResponseWriter, r *http.Request, u auth.User, since int64) {
	f, ok := s.directory.(DirectoryLogFilterer)
	if !ok {
		http.Error(w, "this directory shows its whole log only to the owner of an enrolled address and to its operator", http.StatusForbidden)
		return
	}
	a, err := s.directory.LogAddress(r.Context(), u)
	if err != nil {
		if errors.Is(err, ErrDirectoryRefused) {
			http.Error(w, "this directory shows its whole log only to the owner of an enrolled address and to its operator", http.StatusForbidden)
			return
		}
		directoryError(w, err)
		return
	}
	l, err := f.LogFor(r.Context(), append([]string{a.Address}, a.Previous...), since, maxLogScan, maxLogEntries)
	if err != nil {
		directoryError(w, err)
		return
	}
	l.Filtered = true
	if l.Entries == nil {
		l.Entries = []DirectoryLogEntry{}
	}
	writeJSONStatus(w, http.StatusOK, l)
}

// handleOperatorReset serves POST /admin/directory/reset-enrollment/<address pseudonym>.
func (s *Server) handleOperatorReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	lim := s.limits().adminFail
	key := "ad:" + s.clientAddr(r)
	if !s.dirAdmin(r) {
		if ok, wait := lim.Allow(r.Context(), key); !ok {
			tooMany(w, wait)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	op, ok := s.directory.(DirectoryOperator)
	if !ok {
		http.NotFound(w, r)
		return
	}
	pseudonym := strings.TrimPrefix(r.URL.Path, "/admin/directory/reset-enrollment/")
	if len(pseudonym) != 64 || strings.Trim(pseudonym, "0123456789abcdef") != "" {
		http.Error(w, "the address pseudonym is 64 hex characters", http.StatusBadRequest)
		return
	}
	e, err := op.OperatorResetEnrollment(r.Context(), pseudonym)
	if err != nil {
		s.audit(audit.OwnerReset, audit.Failed, audit.Operator, audit.NotAllowed, 0, pseudonym)
		directoryError(w, err)
		return
	}
	s.audit(audit.OwnerReset, audit.OK, audit.Operator, audit.NoReason, 0, pseudonym)
	writeJSONStatus(w, http.StatusOK, e)
}

// handleUnmapped serves GET /admin/directory/unmapped: the log pseudonyms the reconciliation
// cannot name, for the operator (same credential and failure budget as the operator reset).
func (s *Server) handleUnmapped(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store") // on every answer, errors too
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.dirAdmin(r) {
		if ok, wait := s.limits().adminFail.Allow(r.Context(), "ad:"+s.clientAddr(r)); !ok {
			tooMany(w, wait)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	l, ok := s.directory.(DirectoryUnmappedLister)
	if !ok {
		http.NotFound(w, r)
		return
	}
	u, err := l.UnmappedAddresses(r.Context())
	if err != nil {
		directoryError(w, err)
		return
	}
	if u.Addresses == nil {
		u.Addresses = []DirectoryUnmappedAddress{}
	}
	writeJSONStatus(w, http.StatusOK, u)
}

// handleLogHead serves GET /directory/log/head: the signed head of the log.
func (s *Server) handleLogHead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.logReader(w, r); !ok {
		return
	}
	h, err := s.directory.Head(r.Context())
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, h)
}

// handleWitness serves POST /directory/log/witness: a witness's cosignature of a log entry. No
// sign-in: the witness is known by its signature, which the directory checks against the keys it
// was given. Rate limited per client address.
func (s *Server) handleWitness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wd, ok := s.directory.(DirectoryWitnessing)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ok, wait := s.limits().logIP.Allow(r.Context(), "w:"+s.clientAddr(r)); !ok {
		tooMany(w, wait)
		return
	}
	var c DirectoryCosignature
	if !readDirectoryBody(w, r, &c) {
		return
	}
	if err := wd.AddCosignature(r.Context(), c); err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleCosigned serves GET /directory/log/cosigned: the entries witnesses cosigned, newest first.
func (s *Server) handleCosigned(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wd, ok := s.directory.(DirectoryWitnessing)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, ok := s.logReader(w, r); !ok {
		return
	}
	c, err := wd.Cosigned(r.Context())
	if err != nil {
		directoryError(w, err)
		return
	}
	if c == nil {
		c = []DirectoryCosigned{}
	}
	writeJSONStatus(w, http.StatusOK, c)
}

// handleLogAddress serves GET /directory/log/address: the signed-in owner's pseudonym in the log.
func (s *Server) handleLogAddress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryPublisher(w, r)
	if !ok {
		return
	}
	if ok, wait := s.limits().logRead.Allow(r.Context(), "a:"+u.ID); !ok {
		tooMany(w, wait)
		return
	}
	a, err := s.directory.LogAddress(r.Context(), u)
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, a)
}
