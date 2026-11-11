// Package audit logs store events without personal data: no free-text fields.
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

// The kinds of event; add new ones here, not at call sites.
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
	// TelemetryRefused: a usage report refused by the allowlist.
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

// Actor is the class of who acted, never their identity.
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

// Event is one log line with no free-form string fields.
type Event struct {
	Time    time.Time `json:"time"`
	Kind    Kind      `json:"kind"`
	Outcome Outcome   `json:"outcome"`
	Actor   Actor     `json:"actor"`
	Reason  Reason    `json:"reason,omitempty"`
	// Count is how many things the event covers; 0 is omitted.
	Count int `json:"count,omitempty"`
	// Ref is a Pseudonym, identifying a subject without naming it.
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

// Pseudonym is a keyed, stable, 8-byte HMAC-SHA256 of a subject, in hex.
func Pseudonym(key []byte, subject string) Ref {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(subject))
	return Ref(hex.EncodeToString(m.Sum(nil)[:8]))
}

// Logger writes events as JSON lines; safe for concurrent use.
type Logger struct {
	mu       sync.Mutex
	w        io.Writer
	now      func() time.Time
	rejected int
}

// NewLogger writes to w; untimed events use the logger's UTC clock.
func NewLogger(w io.Writer) *Logger { return &Logger{w: w, now: time.Now} }

// Log writes e, or returns ErrNotAllowed and writes nothing.
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

// Rejected counts events refused by the allowlist.
func (l *Logger) Rejected() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rejected
}
