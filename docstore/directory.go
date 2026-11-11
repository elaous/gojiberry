package docstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/auth"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DirectoryPrefix is the keyspace of the key directory. Its records are written only by the
// server's directory routes (publish, revoke): the document API treats the prefix as reserved,
// like "acl:" and "collab:", so no session can read, write, list or delete them there.
const DirectoryPrefix = "directory/"

// DirectoryLogPrefix is the keyspace of the directory's transparency log (see
// DirectoryService.Log): written only by the directory, reserved from /docs like DirectoryPrefix.
const DirectoryLogPrefix = "directory-log/"

// DirectoryEnrollPrefix is the keyspace of the owner enrollments (see DirectoryService.Enroll):
// one record per address that has enrolled an owner signing key. Written only by the directory,
// reserved from /docs like DirectoryPrefix, exempt from expiry and from encryption.
const DirectoryEnrollPrefix = "directory-enroll/"

// Errors a DirectoryService returns, so the routes can say what went wrong.
var (
	// ErrDirectoryRefused: the user may not publish.
	ErrDirectoryRefused = errors.New("docstore: the directory refuses this publisher")
	// ErrDirectoryBadCosignature: a witness signature is invalid.
	ErrDirectoryBadCosignature = errors.New("docstore: the cosignature is not valid")
	// ErrDirectoryBadKey: the key text is not a public key.
	ErrDirectoryBadKey = errors.New("docstore: not a public key")
	// ErrDirectoryProofRequired: a proof of possession is required.
	ErrDirectoryProofRequired = errors.New("docstore: proof of possession of the key is required")
	// ErrDirectoryEnrollmentRequired: the address must enroll first.
	ErrDirectoryEnrollmentRequired = errors.New("docstore: the address must enroll an owner key before it publishes keys")
	// ErrDirectoryBadProof: the proof of possession is invalid.
	ErrDirectoryBadProof = errors.New("docstore: the proof of possession is not valid")
	// ErrDirectoryBadAttestation: the attestation is invalid.
	ErrDirectoryBadAttestation = errors.New("docstore: the attestation is not valid")
	// ErrDirectoryNotFound: the address has no such key.
	ErrDirectoryNotFound = errors.New("docstore: no such published key")
	// ErrDirectoryRevoked: the key was revoked.
	ErrDirectoryRevoked = errors.New("docstore: this key was revoked and cannot be published again yet")
	// ErrDirectoryBadAddress: the e-mail address is invalid.
	ErrDirectoryBadAddress = errors.New("docstore: not an e-mail address")
	// ErrDirectoryOwnerRequired: an owner signature is required.
	ErrDirectoryOwnerRequired = errors.New("docstore: the owner of this address requires an owner signature")
	// ErrDirectoryBadOwnerSig: the owner signature is invalid.
	ErrDirectoryBadOwnerSig = errors.New("docstore: the owner signature is not valid")
	// ErrDirectoryEnrolled: the address is already enrolled.
	ErrDirectoryEnrolled = errors.New("docstore: this address already has an owner key")
	// ErrDirectoryNotEnrolled: the address is not enrolled.
	ErrDirectoryNotEnrolled = errors.New("docstore: this address has no owner key")
	// ErrDirectoryResetWait: an owner reset must wait.
	ErrDirectoryResetWait = errors.New("docstore: the owner key reset is not due")
	// ErrDirectoryBusy: the log is too busy.
	ErrDirectoryBusy = errors.New("docstore: the directory log is busy")
)

// DirectoryKey is a key found in the directory.
type DirectoryKey struct {
	Key         string `json:"key"`
	Label       string `json:"label,omitempty"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	// Verified: the server's signature on the key is valid.
	Verified bool `json:"verified"`
	Held     bool `json:"held"`
	// Attested: Verified, and the key's own attestation is valid.
	Attested    bool                  `json:"attested"`
	Attestation *DirectoryAttestation `json:"attestation,omitempty"`
	// OwnerEnrolled: the address has an owner key.
	OwnerEnrolled     bool                    `json:"owner_enrolled,omitempty"`
	OwnerSigned       bool                    `json:"owner_signed,omitempty"`
	OwnerResetPending bool                    `json:"owner_reset_pending,omitempty"`
	Owner             *DirectoryOwnerEvidence `json:"owner,omitempty"`
	// Seq is the number of the log entry.
	Seq int64  `json:"seq,omitempty"`
	Kid string `json:"kid,omitempty"`
	// Revoked is set, with the time, when the key was revoked.
	Revoked   bool  `json:"revoked,omitempty"`
	RevokedAt int64 `json:"revoked_at,omitempty"`
}

// DirectoryProof is the answer to a challenge.
type DirectoryProof struct {
	Token string `json:"token"`
	Nonce string `json:"nonce"`
}

// DirectoryAttestation is the key holder's own attestation.
type DirectoryAttestation struct {
	At  int64  `json:"at"`
	Sig string `json:"sig"` // base64
}

// DirectoryChallenge is what POST /directory/challenge returns.
type DirectoryChallenge struct {
	Token     string `json:"token"`
	Encrypted string `json:"encrypted"`
	Expires   int64  `json:"expires"` // Unix seconds
}

// DirectoryOwnerSig is an owner's signature.
type DirectoryOwnerSig struct {
	Time int64  `json:"time"`
	Prev string `json:"prev"`
	Sig  string `json:"sig"`
	V    int    `json:"v,omitempty"`
}

// DirectoryOwnerEvidence is what a lookup returns about owners.
type DirectoryOwnerEvidence struct {
	Pub  string `json:"pub"`
	Time int64  `json:"time"`
	Prev string `json:"prev"`
	Sig  string `json:"sig"`
	// V is the signed message version.
	V int `json:"v,omitempty"`
}

// DirectoryPublish is a publish request.
type DirectoryPublish struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Email       string                `json:"email"`
	Proof       *DirectoryProof       `json:"proof,omitempty"`
	Attestation *DirectoryAttestation `json:"attestation,omitempty"`
	Owner       *DirectoryOwnerSig    `json:"owner,omitempty"`
}

// DirectoryRevoke is the optional body of a revoke.
type DirectoryRevoke struct {
	Owner *DirectoryOwnerSig `json:"owner,omitempty"`
}

// DirectoryEnrollChallenge is an enrollment challenge.
type DirectoryEnrollChallenge struct {
	Token   string `json:"token"`
	Time    int64  `json:"time"`
	Prev    string `json:"prev"`
	Expires int64  `json:"expires"`
}

// DirectoryEnroll is a request to enroll.
type DirectoryEnroll struct {
	Mode     string `json:"mode"`
	OwnerPub string `json:"owner_pub"`
	Token    string `json:"token"`
	Time     int64  `json:"time"`
	Prev     string `json:"prev"`
	Sig      string `json:"sig,omitempty"`
	Proof    string `json:"proof,omitempty"`
}

// DirectoryOwnerKey is one owner key of an address.
type DirectoryOwnerKey struct {
	Pub   string `json:"pub"`
	Seq   int64  `json:"seq"`
	Until int64  `json:"until,omitempty"`
}

// DirectoryEnrollment is the owner enrollment.
type DirectoryEnrollment struct {
	Enrolled   bool                `json:"enrolled"`
	Keys       []DirectoryOwnerKey `json:"keys,omitempty"`
	ResetSince int64               `json:"reset_since,omitempty"`
	ResetDue   int64               `json:"reset_due,omitempty"`
	Seq        int64               `json:"seq,omitempty"`
}

// DirectoryLogAddress is what GET /directory/log/address returns.
type DirectoryLogAddress struct {
	Address  string   `json:"address"`
	Previous []string `json:"previous,omitempty"`
}

// DirectoryHead is the signed head of the log.
type DirectoryHead struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
	At   int64  `json:"at"`
	Kid  string `json:"kid"`
	Sig  string `json:"sig"`
}

// DirectoryCosignature is a witness's statement.
type DirectoryCosignature struct {
	Witness string `json:"witness"`
	Kid     string `json:"kid"`
	Seq     int64  `json:"seq"`
	Hash    string `json:"hash"`
	Sig     string `json:"sig"`
}

// DirectoryWitnessSig is one witness's signature.
type DirectoryWitnessSig struct {
	Witness string `json:"witness"`
	Sig     string `json:"sig"`
}

// DirectoryCosigned is an entry of the log with witness signatures.
type DirectoryCosigned struct {
	Kid       string                `json:"kid"`
	Seq       int64                 `json:"seq"`
	Hash      string                `json:"hash"`
	Witnesses []DirectoryWitnessSig `json:"witnesses"`
}

// DirectoryWitnessing is implemented by a directory with witnesses.
type DirectoryWitnessing interface {
	// AddCosignature records a witness's cosignature.
	AddCosignature(ctx context.Context, c DirectoryCosignature) error
	// Cosigned lists the cosigned entries.
	Cosigned(ctx context.Context) ([]DirectoryCosigned, error)
}

// DirectoryHealth is what the directory reports about itself.
type DirectoryHealth struct {
	// Records, Resigned, Skipped and Unknown count log entries.
	Records     int   `json:"records"`
	Resigned    int   `json:"resigned"`
	Skipped     int   `json:"skipped"`
	UnknownKeys int   `json:"unknown_keys"`
	CheckedAt   int64 `json:"checked_at,omitempty"`
}

// DirectoryLogEntry is one entry of the directory log.
type DirectoryLogEntry struct {
	Seq  int64  `json:"seq"`
	Prev string `json:"prev"` // Hash of entry Seq-1 (64 zeros for the first)
	At   int64  `json:"at"`
	// Op is the entry's operation name.
	Op string `json:"op"`
	// Address is a keyed hash of the e-mail address.
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Kid         string `json:"kid"`
	// Ref is the entry a void entry cancels.
	Ref int64 `json:"ref,omitempty"`
	// The owner's part: OwnerPub is the owner's public key.
	OwnerPub  string `json:"owner_pub,omitempty"`
	OwnerSig  string `json:"owner_sig,omitempty"`
	ProofSig  string `json:"proof_sig,omitempty"`
	OpTime    int64  `json:"op_time,omitempty"`
	OwnerPrev string `json:"owner_prev,omitempty"`
	// OwnerV is the owner message version.
	OwnerV    int    `json:"owner_v,omitempty"`
	LabelHash string `json:"label_hash,omitempty"`
	Hash      string `json:"hash"`
	Sig       string `json:"sig"` // base64
}

// DirectoryLog is what GET /directory/log answers.
type DirectoryLog struct {
	Entries  []DirectoryLogEntry `json:"entries"`
	Keys     map[string]string   `json:"keys,omitempty"`
	Filtered bool                `json:"filtered,omitempty"`
	Next     int64               `json:"next,omitempty"`
	More     bool                `json:"more,omitempty"`
}

// DirectoryLogFilterer is implemented by a directory that filters its log.
type DirectoryLogFilterer interface {
	LogFor(ctx context.Context, addresses []string, since int64, scan, limit int) (DirectoryLog, error)
}

// DirectoryOperator is implemented by a directory with operator tools.
type DirectoryOperator interface {
	OperatorResetEnrollment(ctx context.Context, addressPseudonym string) (DirectoryEnrollment, error)
}

// DirectoryUnmappedAddress is one unmapped log pseudonym.
type DirectoryUnmappedAddress struct {
	Address  string `json:"address"`
	FirstSeq int64  `json:"first_seq"`
	LastSeq  int64  `json:"last_seq"`
	FirstAt  int64  `json:"first_at"`
	LastAt   int64  `json:"last_at"`
	// AgeSeconds is how long ago the first entry was seen.
	AgeSeconds int64 `json:"age_seconds"`
}

// DirectoryUnmapped is what GET /admin/directory/unmapped answers.
type DirectoryUnmapped struct {
	Count      int                        `json:"count"`
	Addresses  []DirectoryUnmappedAddress `json:"addresses"`
	Truncated  bool                       `json:"truncated,omitempty"`
	ComputedAt int64                      `json:"computed_at"`
}

// DirectoryUnmappedLister is implemented by a directory that lists unmapped entries.
type DirectoryUnmappedLister interface {
	UnmappedAddresses(ctx context.Context) (DirectoryUnmapped, error)
}

// DirectoryService is the key directory the server uses.
type DirectoryService interface {
	// Challenge encrypts a fresh nonce to a key.
	Challenge(ctx context.Context, user auth.User, key string) (DirectoryChallenge, error)
	// Publish records the key as a key of an address.
	Publish(ctx context.Context, user auth.User, req DirectoryPublish) (DirectoryKey, error)
	// Revoke removes the key with this fingerprint.
	Revoke(ctx context.Context, user auth.User, fingerprint string, owner *DirectoryOwnerSig) (DirectoryKey, error)
	// Lookup lists the keys published for an address.
	Lookup(ctx context.Context, email string, includeRevoked bool) ([]DirectoryKey, error)
	// Log returns up to limit log entries.
	Log(ctx context.Context, since int64, limit int) (DirectoryLog, error)
	// Head is the signed head of the log.
	Head(ctx context.Context) (DirectoryHead, error)
	// LogAddress is the pseudonym of the user's own address in the log.
	LogAddress(ctx context.Context, user auth.User) (DirectoryLogAddress, error)
	// Enrollment is the owner enrollment of an address (Enrolled false if none).
	Enrollment(ctx context.Context, email string) (DirectoryEnrollment, error)
	// EnrollChallenge starts an enrollment, rotation or reset for the user's own address.
	EnrollChallenge(ctx context.Context, user auth.User) (DirectoryEnrollChallenge, error)
	// Enroll installs an owner key for the user's own address: the first one, a rotation signed
	// by the current key, or a reset after the waiting period (see DirectoryEnroll).
	Enroll(ctx context.Context, user auth.User, req DirectoryEnroll) (DirectoryEnrollment, error)
	// RequestReset starts the waiting period of a provider-session reset of the owner key; the
	// request is logged, where the owner (and everybody who watches the address) sees it.
	RequestReset(ctx context.Context, user auth.User) (DirectoryEnrollment, error)
	// CancelReset ends a pending reset; it needs the current owner key's signature.
	CancelReset(ctx context.Context, user auth.User, owner *DirectoryOwnerSig) (DirectoryEnrollment, error)
}

// DirectoryHealther is implemented by a DirectoryService that reports on itself (the start-up
// census): /health shows it under "directory".
type DirectoryHealther interface {
	DirectoryHealth() DirectoryHealth
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
//	POST   /directory/publish       {"key", "label", "proof": {token, nonce}, "attestation": {at, sig}, "owner": {time, prev, sig}}
//	DELETE /directory/keys/<fingerprint>   [{"owner": {time, prev, sig}}] -> the revoked key
//	GET    /directory/lookup?email=[&revoked=1] -> {"keys": [{key, type, fingerprint, verified, held, attested, owner_signed, ...}]}
//	GET    /directory/log?since=    -> {"entries": [...], "keys": {...}}: the transparency log (200 entries at most)
//	GET    /directory/log/head      -> {seq, hash, at, kid, sig}: the signed head of the log
//	GET    /directory/log/address   -> {address, previous}: the signed-in owner's pseudonym in the log
//	GET    /directory/enroll?email= -> the owner enrollment of an address
//	POST   /directory/enroll/challenge, POST /directory/enroll, POST /directory/enroll/reset,
//	POST   /directory/enroll/cancel -> owner key enrollment, rotation and reset (see DirectoryEnroll)
//	GET    /admin/directory/unmapped -> the log pseudonyms no record or key names (operator only, with the reset route)
//
// Every route needs a signed-in identity-provider session (a passkey session gets 403). Publishing
// and revoking, and the owner routes, need an explicitly verified e-mail (User.VerifiedEmail: the
// provider sent email_verified true, or is configured with auth.Provider.AssumeEmailVerified) and
// act on that address only: only the signed-in owner of an address can publish or revoke its
// keys, and an "email" in the body that is not the session's own is refused. What the directory
// proves, and what it does not, is in docs/encryption-and-passkeys.md (step 4b).
//
// Lookup reveals the public keys published for any address to every signed-in provider user
// that the DirectoryPolicy allows (all, by default), by design: a key is public, and finding a
// person's key by address is what the directory is for. The log, its head and the enrollment
// follow the same rule (the log asks the policy about the user's own address, since it names no
// address in the clear). Every route is limited (WithDirectoryLimits): publishes and revokes per
// user, lookups and log reads per user first and then per client IP and for the whole server (a
// user over their own limit does not spend the shared tokens). The IP is the connection's
// address unless WithDirectoryClientIP says otherwise: behind a proxy every client shares the
// proxy's, so the real per-client limiting, and any limiting of unauthenticated floods, belongs
// to the gateway in front of the server; the shared
// budgets only keep one tenant from draining the service. WithDirectoryGlobalPerDomain makes
// the whole-server lookup budget one per e-mail domain of the caller. Bodies are small.
func (s *Server) WithDirectory(d DirectoryService) *Server {
	s.directory = d
	return s
}

// WithDirectoryLimits chooses where the directory's rate limits live: MemoryLimiters (the
// default, a budget per process) or StoreLimiters(store), which replicas sharing one store
// share. The names are "dir-publish", "dir-challenge", "dir-lookup" and "dir-log" (per user),
// "dir-lookup-ip" and "dir-lookup-all-0" to "dir-lookup-all-15" (the whole-server budget, in shards).
func (s *Server) WithDirectoryLimits(f LimiterFactory) *Server {
	s.dirLimitFactory = f
	return s
}

// WithDirectoryAdmin names who is the operator: allow is asked of a request on the admin routes
// and on the log route. It does two things.
//
// The log. Without it, the whole log (GET /directory/log) is read only by the owner of an enrolled
// address (a provider session whose verified e-mail has an owner key: they need the whole chain to
// audit), and every other provider user gets only the entries about their own address
// (Filtered), so that under a SameDomain policy a colleague cannot read everybody's activity (the
// log names no address in the clear, but it shows who publishes, rotates and revokes, and when).
// An operator (allow true) reads all of it. The signed head (log/head) stays open to every
// provider session.
//
// The operator route POST /admin/directory/reset-enrollment/<address pseudonym> (see
// DirectoryOperator), mounted only when allow is set. A request that fails allow counts against
// a per-client budget of failures (10 a minute, "dir-admin"), past which it answers 429. The
// client is the connection's remote address unless WithDirectoryClientIP says otherwise: behind a
// reverse proxy that is the proxy's address for everybody, so one budget is then shared by every
// caller (an attacker's failures lock the operator out of the route too, for a minute at a time)
// until the proxy's addresses are given to WithDirectoryClientIP (docstorecmd does it from
// GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES). allow must compare a secret in constant time;
// docstorecmd's is the header X-Gojiberry-Admin-Token against
// GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN.
func (s *Server) WithDirectoryAdmin(allow func(*http.Request) bool) *Server {
	s.dirAdmin = allow
	return s
}

// WithDirectoryClientIP chooses how the directory names the client of a request for its
// per-client budgets (the lookup budget per address and the failed-admin budget): f returns the
// client's address, for instance passkey.TrustedProxies(...) behind a reverse proxy, which reads
// X-Forwarded-For only from the proxies it is given. nil (the default) is the connection's
// remote address, which behind a proxy is the proxy's.
func (s *Server) WithDirectoryClientIP(f func(*http.Request) string) *Server {
	s.dirClientIP = f
	return s
}

// clientAddr is the client of r for the per-client budgets.
func (s *Server) clientAddr(r *http.Request) string {
	if s.dirClientIP != nil {
		return s.dirClientIP(r)
	}
	return clientIP(r)
}

// WithDirectoryGlobalPerDomain makes the whole-server lookup budget one per e-mail domain of the
// caller instead of one for everybody: with the SameDomain policy that is one budget per policy
// domain, so one company's probing cannot spend another's. Off by default (one shared budget).
func (s *Server) WithDirectoryGlobalPerDomain(on bool) *Server {
	s.dirDomainBudget = on
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
	logReadsPerMinute  = 30
	challengesPerMin   = 20
	lookupsPerMinute   = 60
	lookupsPerIP       = 120
	lookupsInTotal     = 1200
	// Reads of the log have budgets of their own, with the same numbers as lookups, so a client that polls the
	// log hard cannot use up the lookups of everyone behind its address or in its shard, nor the reverse.
	logsPerIP   = 120
	logsInTotal = 1200
	// adminFailuresPerMinute is how many wrong admin credentials one client may try a minute.
	adminFailuresPerMinute = 10
	// maxLogScan is how many entries one filtered log read looks at.
	maxLogScan = 500
)

// lookupShards is how many sub-budgets the whole-server lookup budget is split into. One shared
// key would be one record every caller of every replica compare-and-sets (a StoreLimiter loses
// the race under load and answers 429 to callers who are within every budget); sixteen keys,
// chosen by the caller's user ID, cut that contention sixteen-fold. Each shard has the whole
// budget divided by sixteen, so the total is unchanged, and a user (at most lookupsPerMinute a
// minute, below one shard's budget) cannot spend more than their own shard. Reads of the log
// (GET /directory/log) have their own per-user, per-address and whole-server shard budgets (dir-log,
// dir-log-ip, dir-log-all-N), so polling the log never uses up lookups.
const lookupShards = 16

type directoryLimits struct {
	publish, challenge, lookup, lookupIP, logRead, logIP, adminFail, telemetry Limiter
	lookupAll, logAll                                                          [lookupShards]Limiter
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
			logRead:   f("dir-log", logReadsPerMinute, logReadsPerMinute),
			logIP:     f("dir-log-ip", logsPerIP, logsPerIP),
			adminFail: f("dir-admin", adminFailuresPerMinute, adminFailuresPerMinute),
			telemetry: f("telemetry", telemetryPerMinute, telemetryPerMinute),
		}
		for i := range s.dirLimits.lookupAll {
			s.dirLimits.lookupAll[i] = f(fmt.Sprintf("dir-lookup-all-%d", i), lookupsInTotal/lookupShards, lookupsInTotal/lookupShards)
			s.dirLimits.logAll[i] = f(fmt.Sprintf("dir-log-all-%d", i), logsInTotal/lookupShards, logsInTotal/lookupShards)
		}
	})
	return s.dirLimits
}

// tooMany answers 429 with the Retry-After the limiter gave (at least a second).
func tooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
	http.Error(w, "too many requests", http.StatusTooManyRequests)
}

// ValidAddress reports whether email is an address the directory accepts: at most 320 bytes,
// exactly one "@" with something on each side, and no space or control character. The directory
// names records by the address and takes its domain after the "@", so a malformed one ("a@b@c")
// would be read differently by different parts of the program.
func ValidAddress(email string) bool {
	email = NormalizeAddress(email)
	if email == "" || len(email) > 320 || strings.Count(email, "@") != 1 {
		return false
	}
	local, domain, _ := strings.Cut(email, "@")
	if local == "" || domain == "" {
		return false
	}
	for _, r := range email {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// readKind says which family of budgets a read of the directory spends.
type readKind int

const (
	readLookup readKind = iota // a lookup of an address
	readLog                    // a read of the log
)

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // a write failure here means the client disconnected
}
