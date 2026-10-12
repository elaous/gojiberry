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
	// ErrDirectoryRefused: the user may not publish (no verified e-mail, a passkey session, too
	// many keys for the address). Answered 403.
	ErrDirectoryRefused = errors.New("docstore: the directory refuses this publisher")
	// ErrDirectoryBadKey: the key text is not a public key. Answered 400.
	ErrDirectoryBadKey = errors.New("docstore: not a public key")
	// ErrDirectoryProofRequired: the directory wants proof of possession and the request has
	// none (see DirectoryService.Challenge). Answered 428.
	ErrDirectoryProofRequired = errors.New("docstore: proof of possession of the key is required")
	// ErrDirectoryEnrollmentRequired: the directory takes keys only for an address that has
	// enrolled an owner key, and this address has not (see DirectoryService.Enroll). Answered 428.
	ErrDirectoryEnrollmentRequired = errors.New("docstore: the address must enroll an owner key before it publishes keys")
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
	// ErrDirectoryBadAddress: the e-mail address is not a single local part and domain (exactly
	// one "@"). Answered 400.
	ErrDirectoryBadAddress = errors.New("docstore: not an e-mail address")
	// ErrDirectoryOwnerRequired: the address has an enrolled owner key and the request carries no
	// owner signature. Answered 428.
	ErrDirectoryOwnerRequired = errors.New("docstore: the owner of this address requires an owner signature")
	// ErrDirectoryBadOwnerSig: the owner signature does not verify, is stale, or names a log
	// position that does not exist. Answered 403.
	ErrDirectoryBadOwnerSig = errors.New("docstore: the owner signature is not valid")
	// ErrDirectoryEnrolled: the address has an owner key already (rotate it, or reset it with the
	// waiting period). Answered 409.
	ErrDirectoryEnrolled = errors.New("docstore: this address already has an owner key")
	// ErrDirectoryNotEnrolled: the address has no owner key. Answered 409.
	ErrDirectoryNotEnrolled = errors.New("docstore: this address has no owner key")
	// ErrDirectoryResetWait: a reset of the owner key was requested and its waiting period is not
	// over, or none was requested. Answered 409.
	ErrDirectoryResetWait = errors.New("docstore: the owner key reset is not due")
	// ErrDirectoryBusy: the log is too busy to take the entry now; try again. Answered 503 with
	// Retry-After.
	ErrDirectoryBusy = errors.New("docstore: the directory log is busy")
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
	// OwnerEnrolled: the address has an owner key (see DirectoryService.Enroll). OwnerSigned:
	// Verified, and the owner signed this publication (the strongest mark, the only one that
	// holds against the server itself); Owner is the evidence a client checks without trusting
	// the server's word (VerifyOwnerEvidence). OwnerResetPending: a reset of the owner key was
	// requested and has not completed (the waiting period is running).
	OwnerEnrolled     bool                    `json:"owner_enrolled,omitempty"`
	OwnerSigned       bool                    `json:"owner_signed,omitempty"`
	OwnerResetPending bool                    `json:"owner_reset_pending,omitempty"`
	Owner             *DirectoryOwnerEvidence `json:"owner,omitempty"`
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

// DirectoryOwnerSig is an owner's signature of one operation: the owner key's Ed25519 signature
// (base64) over "directory-op|<op>|<address>|<fingerprint>|<Time>|<Prev>", Time in Unix seconds
// (close to now) and Prev the log position the owner last saw ("<seq>:<entry hash>", from
// GET /directory/log/head; "0:" + 64 zeros for an empty log). encrypt.OwnerOpMessage builds the
// text.
//
// V is the version of the message: 0 or 1 is the one above; 2 (a publication) is
// "directory-op-v2|<op>|<address>|<fingerprint>|<label hash>|<Time>|<Prev>", which also covers the
// key's label (as the hash of the label the directory stores), so the server cannot change the
// label of a key the owner signed. The directory requires 2 for a new publication and still
// accepts 1 in the entries it already holds.
type DirectoryOwnerSig struct {
	Time int64  `json:"time"`
	Prev string `json:"prev"`
	Sig  string `json:"sig"`
	V    int    `json:"v,omitempty"`
}

// DirectoryOwnerEvidence is what a lookup row carries so a client can check an owner signature
// itself: the owner's public key (base64 of the 32 raw bytes) and the signature of the
// publication, which names the operation ("publish") and the fingerprint through the row.
type DirectoryOwnerEvidence struct {
	Pub  string `json:"pub"`
	Time int64  `json:"time"`
	Prev string `json:"prev"`
	Sig  string `json:"sig"`
	// V is the version of the signed message (see DirectoryOwnerSig); 2 covers the row's Label.
	V int `json:"v,omitempty"`
}

// DirectoryPublish is a publish request. Owner is required when the address has an enrolled owner
// key.
type DirectoryPublish struct {
	Key         string                `json:"key"`
	Label       string                `json:"label"`
	Email       string                `json:"email"`
	Proof       *DirectoryProof       `json:"proof,omitempty"`
	Attestation *DirectoryAttestation `json:"attestation,omitempty"`
	Owner       *DirectoryOwnerSig    `json:"owner,omitempty"`
}

// DirectoryRevoke is the optional body of DELETE /directory/keys/<fingerprint>: the owner's
// signature, required when the address has an enrolled owner key.
type DirectoryRevoke struct {
	Owner *DirectoryOwnerSig `json:"owner,omitempty"`
}

// DirectoryEnrollChallenge is what POST /directory/enroll/challenge answers: the time and log
// position the server wants the enrollment signatures to carry, and the token that binds them to
// the user and address and is returned with the enrollment (single use, short lived).
type DirectoryEnrollChallenge struct {
	Token   string `json:"token"`
	Time    int64  `json:"time"`
	Prev    string `json:"prev"`
	Expires int64  `json:"expires"`
}

// DirectoryEnroll is a request to enroll, rotate or reset the owner key of the signed-in user's
// address. OwnerPub is the new key (base64 of 32 raw bytes). Mode is "enroll" (the address has no
// owner key: Sig is the new key's signature over the "enroll" operation), "rotate" (Sig is the
// current owner key's signature over "rotate", Proof the new key's over "accept") or "reset"
// (the waiting period after RequestReset is over: only Proof, the new key's signature over
// "accept"). Token, Time and Prev come from the enroll challenge.
type DirectoryEnroll struct {
	Mode     string `json:"mode"`
	OwnerPub string `json:"owner_pub"`
	Token    string `json:"token"`
	Time     int64  `json:"time"`
	Prev     string `json:"prev"`
	Sig      string `json:"sig,omitempty"`
	Proof    string `json:"proof,omitempty"`
}

// DirectoryOwnerKey is one owner key of an address, as the enrollment shows it: Seq is the log
// entry that enrolled it and Until the one that replaced it (0 while it is the current key).
type DirectoryOwnerKey struct {
	Pub   string `json:"pub"`
	Seq   int64  `json:"seq"`
	Until int64  `json:"until,omitempty"`
}

// DirectoryEnrollment is the owner enrollment of an address: the chain of owner keys (the last is
// current) and, when a reset was requested and has not completed, when (Unix seconds) and the
// earliest time it can complete.
type DirectoryEnrollment struct {
	Enrolled   bool                `json:"enrolled"`
	Keys       []DirectoryOwnerKey `json:"keys,omitempty"`
	ResetSince int64               `json:"reset_since,omitempty"`
	ResetDue   int64               `json:"reset_due,omitempty"`
	Seq        int64               `json:"seq,omitempty"`
}

// DirectoryLogAddress is what GET /directory/log/address answers the signed-in owner: the
// pseudonym that names their address in the log (a keyed hash: only the directory can compute
// it, and it hands it to the owner only), and the pseudonyms of the same address under retired
// directory keys (entries made before a rotation).
type DirectoryLogAddress struct {
	Address  string   `json:"address"`
	Previous []string `json:"previous,omitempty"`
}

// DirectoryHead is the signed head of the log: the number and hash of the last entry, when the
// server said so (Unix seconds) and its signature (base64) by the key Kid. A client that
// remembers the head it saw detects a log that was cut back or rewritten.
type DirectoryHead struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
	At   int64  `json:"at"`
	Kid  string `json:"kid"`
	Sig  string `json:"sig"`
}

// DirectoryHealth is what the directory reports about itself at start-up, shown by /health.
type DirectoryHealth struct {
	// Records, Resigned, Skipped and UnknownKeys are the census of the last start-up check (see
	// encrypt.AdapterDirectory.Startup): UnknownKeys is how many records are named under keys the
	// server does not know (a wrong or missing directory key), which no lookup can find.
	Records     int   `json:"records"`
	Resigned    int   `json:"resigned"`
	Skipped     int   `json:"skipped"`
	UnknownKeys int   `json:"unknown_keys"`
	CheckedAt   int64 `json:"checked_at,omitempty"`
}

// DirectoryLogEntry is one entry of the directory's transparency log: a publication or a
// revocation, chained to the entry before it by hash and signed by the server.
type DirectoryLogEntry struct {
	Seq  int64  `json:"seq"`
	Prev string `json:"prev"` // Hash of entry Seq-1 (64 zeros for the first)
	At   int64  `json:"at"`
	// Op is "publish", "revoke", "enroll", "rotate", "reset-request", "reset", "reset-cancel",
	// "operator-reset" (the operator removed the owner enrollment of the address, Ref being the
	// enrollment entry it removed; it is signed by the directory key like every entry, and
	// visible to the address's watchers) or "void" (the entry Ref did not take effect: its
	// record could not be written; it must follow the entry it cancels, for the same address,
	// within a short window, or, for an owner-state entry, a later reconciliation).
	Op string `json:"op"`
	// Address is a keyed hash (HMAC under the directory's key material, domain-separated) of the
	// address: only the directory can compute it, and the owner asks for theirs
	// (GET /directory/log/address) to find the entries about their own address. A guessed
	// address cannot be tested against it.
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Kid         string `json:"kid"`
	// Ref is the entry a "void" entry cancels.
	Ref int64 `json:"ref,omitempty"`
	// The owner's part: OwnerPub is the key an enroll, rotate or reset entry installs (base64);
	// OwnerSig the owner key's signature of the operation (for "enroll", the new key's own; for
	// "rotate", the current key's; for publish, revoke and reset-cancel on an enrolled address,
	// the current key's), ProofSig the new key's acceptance of a rotate or reset, and OpTime and
	// OwnerPrev the time and log position the signatures name. All are in the hash, so a client
	// that knows the address checks them (encrypt.AuditAddress) without trusting the server.
	OwnerPub  string `json:"owner_pub,omitempty"`
	OwnerSig  string `json:"owner_sig,omitempty"`
	ProofSig  string `json:"proof_sig,omitempty"`
	OpTime    int64  `json:"op_time,omitempty"`
	OwnerPrev string `json:"owner_prev,omitempty"`
	// OwnerV is the version of the message the owner signed (2: it covers the key's label, whose
	// hash is LabelHash); 0 for entries made before.
	OwnerV    int    `json:"owner_v,omitempty"`
	LabelHash string `json:"label_hash,omitempty"`
	Hash      string `json:"hash"`
	Sig       string `json:"sig"` // base64
}

// DirectoryLog is what GET /directory/log answers: entries after the one asked for, and the
// public keys (by key ID, base64) that verify them, as this server states them: an auditor pins
// those out of band rather than trusting this field.
//
// Filtered says the answer holds only the entries about the caller's own address (the caller is
// neither an administrator nor the owner of an enrolled address, see Server.WithDirectoryAdmin):
// each entry's hash and signature can be checked, but the chain between them cannot, because the
// entries of other addresses are left out. Next is the number scanned up to (ask again with
// since=Next) and More says the scan stopped before the end of the log.
type DirectoryLog struct {
	Entries  []DirectoryLogEntry `json:"entries"`
	Keys     map[string]string   `json:"keys,omitempty"`
	Filtered bool                `json:"filtered,omitempty"`
	Next     int64               `json:"next,omitempty"`
	More     bool                `json:"more,omitempty"`
}

// DirectoryLogFilterer is implemented by a DirectoryService that can read the log for some
// addresses only (their pseudonyms): scanning at most scan entries after since and returning up
// to limit of them. The log routes use it for callers who may not read the whole log; a service
// without it shows those callers nothing (403).
type DirectoryLogFilterer interface {
	LogFor(ctx context.Context, addresses []string, since int64, scan, limit int) (DirectoryLog, error)
}

// DirectoryOperator is implemented by a DirectoryService that lets the operator recover an
// address whose owner enrollment is stuck (a crash left it half written, or the key and its
// reset are both lost): OperatorResetEnrollment removes the enrollment of the address with this
// log pseudonym and logs an "operator-reset" entry, which everybody who watches the address sees.
type DirectoryOperator interface {
	OperatorResetEnrollment(ctx context.Context, addressPseudonym string) (DirectoryEnrollment, error)
}

// DirectoryUnmappedAddress is one log pseudonym with owner-state entries and no e-mail the store
// knows it by (no enrollment record, no published key). Only the pseudonym (a hash) is held: no
// e-mail, no key.
type DirectoryUnmappedAddress struct {
	Address  string `json:"address"`
	FirstSeq int64  `json:"first_seq"`
	LastSeq  int64  `json:"last_seq"`
	FirstAt  int64  `json:"first_at"`
	LastAt   int64  `json:"last_at"`
	// AgeSeconds is how long ago the first of its entries was written, at ComputedAt.
	AgeSeconds int64 `json:"age_seconds"`
}

// DirectoryUnmapped is what GET /admin/directory/unmapped answers: Count addresses, the first
// len(Addresses) of them by first entry number (Truncated says the list was capped), and when it
// was computed (Unix seconds).
type DirectoryUnmapped struct {
	Count      int                        `json:"count"`
	Addresses  []DirectoryUnmappedAddress `json:"addresses"`
	Truncated  bool                       `json:"truncated,omitempty"`
	ComputedAt int64                      `json:"computed_at"`
}

// DirectoryUnmappedLister is implemented by a DirectoryService that can list the addresses the
// reconciliation cannot name (see DirectoryOperator for the reset that clears one).
type DirectoryUnmappedLister interface {
	UnmappedAddresses(ctx context.Context) (DirectoryUnmapped, error)
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
	// that it was revoked. owner is the owner's signature (nil if none was sent), required when
	// the address has an enrolled owner key.
	Revoke(ctx context.Context, user auth.User, fingerprint string, owner *DirectoryOwnerSig) (DirectoryKey, error)
	// Lookup lists the keys published for an address; with includeRevoked, the revoked ones too,
	// flagged.
	Lookup(ctx context.Context, email string, includeRevoked bool) ([]DirectoryKey, error)
	// Log returns up to limit log entries after seq since.
	Log(ctx context.Context, since int64, limit int) (DirectoryLog, error)
	// Head is the signed head of the log (its last entry).
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
