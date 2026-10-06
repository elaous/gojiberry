package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/elaous/gojiberry/auth"
)

// DirectoryPrefix is the keyspace of the key directory. Its records are written only by the
// server's directory routes (publish, revoke): the document API treats the prefix as reserved,
// like "acl:" and "collab:", so no session can read, write, list or delete them there.
const DirectoryPrefix = "directory/"

// DirectoryLogPrefix is the keyspace of the directory's transparency log (see
// DirectoryService.Log): written only by the directory, reserved from /docs like DirectoryPrefix.
const DirectoryLogPrefix = "directory-log/"

// Errors a DirectoryService returns, so the routes can say what went wrong.
var (
	// ErrDirectoryRefused: the user may not publish (no verified e-mail, a passkey session, too
	// many keys for the address). Answered 403.
	ErrDirectoryRefused = errors.New("docstore: the directory refuses this publisher")
	// ErrDirectoryBadKey: the key text is not a public key. Answered 400.
	ErrDirectoryBadKey = errors.New("docstore: not a public key")
	// ErrDirectoryProofRequired: the directory wants proof of possession and the request has
	// none (see DirectoryService.Challenge). Answered 428.
	ErrDirectoryProofRequired = errors.New("docstore: proof of possession of the key is required")
	// ErrDirectoryBadProof: the proof of possession is wrong, expired, for another user or key,
	// or already used. Answered 403.
	ErrDirectoryBadProof = errors.New("docstore: the proof of possession is not valid")
	// ErrDirectoryBadAttestation: the attestation does not verify, is stale, or the key cannot
	// attest. Answered 400.
	ErrDirectoryBadAttestation = errors.New("docstore: the attestation is not valid")
	// ErrDirectoryNotFound: the address has no such published key. Answered 404.
	ErrDirectoryNotFound = errors.New("docstore: no such published key")
	// ErrDirectoryRevoked: the key was revoked and may not be published again yet. Answered 409.
	ErrDirectoryRevoked = errors.New("docstore: this key was revoked and cannot be published again yet")
)

// DirectoryKey is a key found in the directory, as the lookup route answers it.
type DirectoryKey struct {
	Key         string `json:"key"`
	Label       string `json:"label,omitempty"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	// Verified: the server's signature on the record is valid (the provider says this account's
	// verified e-mail published the key).
	Verified bool `json:"verified"`
	// Held: whoever published the key decrypted a challenge sent to it (proof of possession at
	// publish time).
	Held bool `json:"held"`
	// Attested: Verified, and the key's own signature binds it to the address (ssh-ed25519
	// keys only). The signature itself is in Attestation, so a client can check it without
	// trusting the server's word.
	Attested    bool                  `json:"attested"`
	Attestation *DirectoryAttestation `json:"attestation,omitempty"`
	// Seq is the number of the log entry of the publication (0: none), Kid the ID of the server
	// key that signed the record.
	Seq int64  `json:"seq,omitempty"`
	Kid string `json:"kid,omitempty"`
	// Revoked is set (with the time, Unix seconds) only in a lookup that asked for revoked keys.
	Revoked   bool  `json:"revoked,omitempty"`
	RevokedAt int64 `json:"revoked_at,omitempty"`
}

// DirectoryProof is the answer to a challenge: the token the challenge came with and the nonce
// decrypted from it with the private key.
type DirectoryProof struct {
	Token string `json:"token"`
	Nonce string `json:"nonce"`
}

// DirectoryAttestation is the key holder's own statement that the key belongs to an address: a
// signature by the key over "directory-binding|<address>|<fingerprint>|<At>" (At in Unix
// seconds, close to now).
type DirectoryAttestation struct {
	At  int64  `json:"at"`
	Sig string `json:"sig"` // base64
}

// DirectoryChallenge is what POST /directory/challenge answers: a nonce encrypted to the key
// (Encrypted, base64 of an age file, which only the holder of the private key can open) and the
// token that binds it to the user and key and that is returned with the decrypted nonce.
type DirectoryChallenge struct {
	Token     string `json:"token"`
	Encrypted string `json:"encrypted"`
	Expires   int64  `json:"expires"` // Unix seconds
}

// DirectoryPublish is a publish request.
type DirectoryPublish struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Email       string                `json:"email"`
	Proof       *DirectoryProof       `json:"proof,omitempty"`
	Attestation *DirectoryAttestation `json:"attestation,omitempty"`
}

// DirectoryLogEntry is one entry of the directory's transparency log: a publication or a
// revocation, chained to the entry before it by hash and signed by the server.
type DirectoryLogEntry struct {
	Seq  int64  `json:"seq"`
	Prev string `json:"prev"` // Hash of entry Seq-1 (64 zeros for the first)
	At   int64  `json:"at"`
	Op   string `json:"op"` // "publish" or "revoke"
	// Address is the hex SHA-256 of "gojiberry-directory-log|<address>": a client finds the
	// entries about its own address by hashing it.
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	Kid         string `json:"kid"`
	Hash        string `json:"hash"`
	Sig         string `json:"sig"` // base64
}

// DirectoryLog is what GET /directory/log answers: entries after the one asked for, and the
// public keys (by key ID, base64) that verify them, as this server states them: an auditor pins
// those out of band rather than trusting this field.
type DirectoryLog struct {
	Entries []DirectoryLogEntry `json:"entries"`
	Keys    map[string]string   `json:"keys,omitempty"`
}

// DirectoryService is the key directory the server fronts (encrypt.DirectoryService makes one
// over an adapter; it lives in docstore/encrypt, which imports this package). All of them are
// called only after the routes checked the session (and limited the caller).
type DirectoryService interface {
	// Challenge encrypts a fresh nonce to key for user, for a later Publish.
	Challenge(ctx context.Context, user auth.User, key string) (DirectoryChallenge, error)
	// Publish records the key as a key of the user's own address.
	Publish(ctx context.Context, user auth.User, req DirectoryPublish) (DirectoryKey, error)
	// Revoke removes the key with this fingerprint from the user's own address and remembers
	// that it was revoked.
	Revoke(ctx context.Context, user auth.User, fingerprint string) (DirectoryKey, error)
	// Lookup lists the keys published for an address; with includeRevoked, the revoked ones too,
	// flagged.
	Lookup(ctx context.Context, email string, includeRevoked bool) ([]DirectoryKey, error)
	// Log returns up to limit log entries after seq since.
	Log(ctx context.Context, since int64, limit int) (DirectoryLog, error)
}

// DirectoryPolicy decides who may look up an address. The default allows every signed-in
// provider user (a public key is public, and finding it by address is the feature); a
// deployment that wants to bound probing for addresses narrows it (SameDomain, Contacts, AnyOf,
// or its own).
type DirectoryPolicy interface {
	CanLookup(user auth.User, email string) bool
}

// DirectoryPolicyFunc makes a function a DirectoryPolicy.
type DirectoryPolicyFunc func(user auth.User, email string) bool

// CanLookup implements DirectoryPolicy.
func (f DirectoryPolicyFunc) CanLookup(user auth.User, email string) bool { return f(user, email) }

// AllowAllLookups is the default policy: any signed-in provider user may look up any address.
var AllowAllLookups DirectoryPolicy = DirectoryPolicyFunc(func(auth.User, string) bool { return true })

// SameDomain allows a user to look up addresses of their own e-mail domain, which must be a
// verified one (User.VerifiedEmail): a company's directory that answers only its own people.
func SameDomain() DirectoryPolicy {
	return DirectoryPolicyFunc(func(u auth.User, email string) bool {
		d := addressDomain(u.Email)
		return u.VerifiedEmail && d != "" && d == addressDomain(email)
	})
}

// Contacts allows a user to look up the addresses contacts returns for them (their address book,
// the people they share documents with), compared case-insensitively.
func Contacts(contacts func(auth.User) []string) DirectoryPolicy {
	return DirectoryPolicyFunc(func(u auth.User, email string) bool {
		want := NormalizeAddress(email)
		for _, c := range contacts(u) {
			if NormalizeAddress(c) == want {
				return true
			}
		}
		return false
	})
}

// AnyOf allows a lookup that any of the policies allows (none: nothing is allowed).
func AnyOf(policies ...DirectoryPolicy) DirectoryPolicy {
	return DirectoryPolicyFunc(func(u auth.User, email string) bool {
		for _, p := range policies {
			if p.CanLookup(u, email) {
				return true
			}
		}
		return false
	})
}

// ParseDirectoryLookupMode parses GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP: "" or "any" (every
// signed-in provider user may look up any address) or "domain" (only addresses of the user's own
// verified e-mail domain). It returns the policy.
func ParseDirectoryLookupMode(raw string) (DirectoryPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "any":
		return AllowAllLookups, nil
	case "domain":
		return SameDomain(), nil
	}
	return nil, errors.New("parsing GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP \"" + raw + "\": want \"any\" or \"domain\"")
}

func addressDomain(email string) string {
	email = NormalizeAddress(email)
	i := strings.LastIndex(email, "@")
	if i < 0 || i == len(email)-1 {
		return ""
	}
	return email[i+1:]
}

// WithDirectory turns on the directory routes, mounted next to /docs when an auth.Manager is
// present (without sign-in there is nobody to vouch for an address, so they are not mounted):
//
//	POST   /directory/challenge     {"key"}  -> {token, encrypted, expires}: a nonce encrypted to the key
//	POST   /directory/publish       {"key", "label", "proof": {token, nonce}, "attestation": {at, sig}}
//	DELETE /directory/keys/<fingerprint>     -> the revoked key
//	GET    /directory/lookup?email=[&revoked=1] -> {"keys": [{key, type, fingerprint, verified, held, attested, ...}]}
//	GET    /directory/log?since=    -> {"entries": [...], "keys": {...}}: the transparency log
//
// All but the log need a signed-in identity-provider session (a passkey session gets 403); the
// log needs any session. Publishing and revoking need an explicitly verified e-mail
// (User.VerifiedEmail: the provider sent email_verified true, or is configured with
// auth.Provider.AssumeEmailVerified) and act on that address only: only the signed-in owner of an
// address can publish or revoke its keys, and an "email" in the body that is not the session's
// own is refused. What the directory proves, and what it does not, is in
// docs/encryption-and-passkeys.md (step 4b).
//
// Lookup reveals the public keys published for any address to every signed-in provider user
// that the DirectoryPolicy allows (all, by default), by design: a key is public, and finding a
// person's key by address is what the directory is for. Every route is limited (WithDirectoryLimits):
// publishes and revokes per user, lookups per user, per client IP (the connection's address:
// behind a proxy every client shares it, so the real limiting belongs to the gateway) and for the
// whole server. Bodies are small.
func (s *Server) WithDirectory(d DirectoryService) *Server {
	s.directory = d
	return s
}

// WithDirectoryLimits chooses where the directory's rate limits live: MemoryLimiters (the
// default, a budget per process) or StoreLimiters(store), which replicas sharing one store
// share. The names are "dir-publish", "dir-challenge", "dir-lookup" (per user), "dir-lookup-ip"
// and "dir-lookup-all".
func (s *Server) WithDirectoryLimits(f LimiterFactory) *Server {
	s.dirLimitFactory = f
	return s
}

// WithDirectoryPolicy narrows who may look up an address (see DirectoryPolicy); nil means
// AllowAllLookups.
func (s *Server) WithDirectoryPolicy(p DirectoryPolicy) *Server {
	s.dirPolicy = p
	return s
}

// The directory's budgets: per minute on average, and at once.
const (
	maxDirectoryBody   = 4 << 10
	maxLookupKeys      = 50
	maxLogEntries      = 200
	publishesPerMinute = 10
	challengesPerMin   = 20
	lookupsPerMinute   = 60
	lookupsPerIP       = 120
	lookupsInTotal     = 1200
)

type directoryLimits struct {
	publish, challenge, lookup, lookupIP, lookupAll Limiter
}

func (s *Server) limits() *directoryLimits {
	s.dirLimitOnce.Do(func() {
		f := s.dirLimitFactory
		if f == nil {
			f = MemoryLimiters()
		}
		s.dirLimits = &directoryLimits{
			publish:   f("dir-publish", publishesPerMinute, publishesPerMinute),
			challenge: f("dir-challenge", challengesPerMin, challengesPerMin),
			lookup:    f("dir-lookup", lookupsPerMinute, lookupsPerMinute),
			lookupIP:  f("dir-lookup-ip", lookupsPerIP, lookupsPerIP),
			lookupAll: f("dir-lookup-all", lookupsInTotal, lookupsInTotal),
		}
	})
	return s.dirLimits
}

// tooMany answers 429 with the Retry-After the limiter gave (at least a second).
func tooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

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
	case errors.Is(err, ErrDirectoryBadKey):
		http.Error(w, "not a public key", http.StatusBadRequest)
	case errors.Is(err, ErrDirectoryProofRequired):
		http.Error(w, "proof of possession required: POST /directory/challenge first", http.StatusPreconditionRequired)
	case errors.Is(err, ErrDirectoryBadProof):
		http.Error(w, "the proof of possession is not valid", http.StatusForbidden)
	case errors.Is(err, ErrDirectoryBadAttestation):
		http.Error(w, "the attestation is not valid", http.StatusBadRequest)
	case errors.Is(err, ErrDirectoryNotFound):
		http.Error(w, "no such published key", http.StatusNotFound)
	case errors.Is(err, ErrDirectoryRevoked):
		http.Error(w, "this key was revoked and cannot be published again yet", http.StatusConflict)
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
// shows ("SHA256:...") or its DirectoryFingerprintSegment form.
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
	k, err := s.directory.Revoke(r.Context(), u, fp)
	if err != nil {
		directoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, k)
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	u, ok := s.directoryUser(w, r)
	if !ok {
		return
	}
	// Only a provider session may look up: a passkey account's address was typed by whoever
	// made it, and nothing vouches for the account, so it is not a directory reader.
	if strings.HasPrefix(u.ID, "passkey:") {
		http.Error(w, "only a signed-in provider account can look up keys", http.StatusForbidden)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	lim := s.limits()
	for _, c := range []struct {
		l   Limiter
		key string
	}{{lim.lookupIP, "li:" + ip}, {lim.lookup, "l:" + u.ID}, {lim.lookupAll, "all"}} {
		if ok, wait := c.l.Allow(r.Context(), c.key); !ok {
			tooMany(w, wait)
			return
		}
	}
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if email == "" || len(email) > 320 {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	// The policy is asked after the budgets, so that probing a refused address costs budget too.
	// Its answer does not depend on whether the address has keys, so a refusal leaks nothing.
	policy := s.dirPolicy
	if policy == nil {
		policy = AllowAllLookups
	}
	if !policy.CanLookup(u, email) {
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

// handleLog serves GET /directory/log?since=<seq>: the entries after seq since (at most 200).
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := s.directoryUser(w, r); !ok {
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
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	lim := s.limits()
	if ok, wait := lim.lookupIP.Allow(r.Context(), "li:"+ip); !ok {
		tooMany(w, wait)
		return
	}
	if ok, wait := lim.lookupAll.Allow(r.Context(), "all"); !ok {
		tooMany(w, wait)
		return
	}
	l, err := s.directory.Log(r.Context(), since, maxLogEntries)
	if err != nil {
		serverError(w, err)
		return
	}
	if l.Entries == nil {
		l.Entries = []DirectoryLogEntry{}
	}
	writeJSONStatus(w, http.StatusOK, l)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // a write failure here means the client disconnected
}
