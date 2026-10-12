// Package audit is the operator's log of what happened to a store, written so that it cannot hold
// personal data: an Event has no free text. Every field is a number, a time or a member of a closed
// set, so a name, an address, a document id or a piece of content has no field to go in, and
// a value outside the sets is refused rather than written. The operator can still follow one
// subject through the log with Pseudonym, a keyed hash that only the holder of the key can
// compute and that cannot be turned back into the id.
package audit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Kind is what happened.
type Kind string

// The kinds of event. Adding one is a reviewed change here, not a string at a call site.
const (
	KeyEnrolled     Kind = "key.enrolled"
	KeyRevoked      Kind = "key.revoked"
	KeyRotated      Kind = "key.rotated"
	OwnerReset      Kind = "owner.reset"
	DocumentCreated Kind = "document.created"
	DocumentDeleted Kind = "document.deleted"
	DocumentExpired Kind = "document.expired"
	AccessGranted   Kind = "access.granted"
	AccessRevoked   Kind = "access.revoked"
	SignInFailed    Kind = "signin.failed"
	RateLimited     Kind = "ratelimit.hit"
	BackupMade      Kind = "backup.made"
	BackupRestored  Kind = "backup.restored"
	ConfigChanged   Kind = "config.changed"
	// TelemetryRefused is a usage report refused for being outside the allowlist (package telemetry).
	TelemetryRefused Kind = "telemetry.refused"
)

// Outcome is how it ended.
type Outcome string

// The outcomes.
const (
	OK     Outcome = "ok"
	Denied Outcome = "denied"
	Failed Outcome = "error"
)

// Actor is the class of the one who did it, never who they are.
type Actor string

// The actor classes.
const (
	Owner     Actor = "owner"
	Editor    Actor = "editor"
	Viewer    Actor = "viewer"
	Anonymous Actor = "anonymous"
	Operator  Actor = "operator"
	System    Actor = "system"
)

// Reason is a short code for why something was denied or failed.
type Reason string

// The reasons. The empty reason is allowed (nothing to add).
const (
	NoReason     Reason = ""
	BadSignature Reason = "bad_signature"
	Expired      Reason = "expired"
	OverQuota    Reason = "over_quota"
	OverRate     Reason = "over_rate"
	NotAllowed   Reason = "not_allowed"
	Conflict     Reason = "conflict"
	Unavailable  Reason = "unavailable"
)

// Event is one line of the log. It deliberately has no string that an application can fill freely.
type Event struct {
	Time    time.Time `json:"time"`
	Kind    Kind      `json:"kind"`
	Outcome Outcome   `json:"outcome"`
	Actor   Actor     `json:"actor"`
	Reason  Reason    `json:"reason,omitempty"`
	// Count is how many things the event covers (documents restored, requests refused); 0 is omitted.
	Count int `json:"count,omitempty"`
	// Ref is a Pseudonym, to follow one subject through the log without naming it.
	Ref Ref `json:"ref,omitempty"`
}

// Ref is a pseudonym (see Pseudonym): 16 hex characters, nothing else.
type Ref string

// ErrNotAllowed is returned for an event with a value outside the allowlist.
var ErrNotAllowed = errors.New("audit: the event has a value that is not on the allowlist")

var (
	kinds = set(KeyEnrolled, KeyRevoked, KeyRotated, OwnerReset, DocumentCreated, DocumentDeleted, DocumentExpired,
		AccessGranted, AccessRevoked, SignInFailed, RateLimited, BackupMade, BackupRestored, ConfigChanged, TelemetryRefused)
	outcomes = set(OK, Denied, Failed)
	actors   = set(Owner, Editor, Viewer, Anonymous, Operator, System)
	reasons  = set(NoReason, BadSignature, Expired, OverQuota, OverRate, NotAllowed, Conflict, Unavailable)
)

func set[T comparable](vs ...T) map[T]bool {
	m := make(map[T]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}

// Valid reports whether every value of e is on the allowlist.
func (e Event) Valid() bool {
	if !kinds[e.Kind] || !outcomes[e.Outcome] || !actors[e.Actor] || !reasons[e.Reason] || e.Count < 0 {
		return false
	}
	if e.Ref != "" {
		if len(e.Ref) != 16 {
			return false
		}
		if _, err := hex.DecodeString(string(e.Ref)); err != nil {
			return false
		}
	}
	return true
}

// Pseudonym is a stable stand-in for a subject (an address, a document id): the first 8 bytes of
// HMAC-SHA256 of it under key, in hex. The same subject gives the same Ref while the operator keeps
// the key; without the key it cannot be computed or checked, and it cannot be reversed.
func Pseudonym(key []byte, subject string) Ref {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(subject))
	return Ref(hex.EncodeToString(m.Sum(nil)[:8]))
}

// Logger writes events as JSON lines. It is safe for concurrent use.
type Logger struct {
	mu       sync.Mutex
	w        io.Writer
	now      func() time.Time
	rejected int
}

// NewLogger writes to w. The time of an event without one is the logger's clock, in UTC.
func NewLogger(w io.Writer) *Logger { return &Logger{w: w, now: time.Now} }

// Log writes e, or returns ErrNotAllowed and writes nothing (the refusal is counted, see Rejected).
func (l *Logger) Log(e Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !e.Valid() {
		l.rejected++
		return ErrNotAllowed
	}
	if e.Time.IsZero() {
		e.Time = l.now()
	}
	e.Time = e.Time.UTC().Truncate(time.Second)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = l.w.Write(append(b, '\n'))
	return err
}

// Rejected is how many events were refused: a call site that sends something off the allowlist shows
// here instead of leaking.
func (l *Logger) Rejected() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rejected
}
