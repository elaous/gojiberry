// Package sessionstore is an auth.SessionStore kept in a docstore.Adapter, so replicas that share
// the store share their sign-in sessions: a token issued by one is valid on the others, a logout on
// one ends the session on all, and an OpenID Connect login begun on one can finish on another.
package sessionstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// The keyspaces: "auth/session/<expiry hour>/<hash of the token>" and
// "auth/state/<expiry minute>/<hash of the token>". The token itself is never in a record name or
// body: someone who can list or read the store cannot sign in with what they see. The number in
// the name is the hour (minute, for a state) at which the record is certain to have expired, so the
// sweep deletes by name alone, without reading a record. The document API refuses both prefixes,
// expiry gives them no deadline and IsDocumentKey rejects them.
const (
	SessionPrefix    = docstore.SessionPrefix
	LoginStatePrefix = docstore.LoginStatePrefix
)

// ErrNotConditional is returned by New for a store that cannot write conditionally: without a
// compare-and-set no two replicas can agree on who consumed a login state.
var ErrNotConditional = errors.New("sessionstore: a shared session store needs a store with conditional writes (docstore.ConditionalAdapter)")

const (
	// DefaultMaxSessions bounds the live sessions (the in-memory store's bound).
	DefaultMaxSessions = 100000
	// DefaultMaxStates bounds the pending logins. Smaller than the in-memory bound: every
	// /auth/<provider>/login is an unauthenticated request that here is a write to the store.
	DefaultMaxStates = 10000

	sessionBucket = time.Hour
	stateBucket   = time.Minute
	// sweepGrace is how long past a record's bucket the sweep waits (replicas' clocks differ).
	sweepGrace = time.Minute

	recordVersion = 1
)

// Store is an auth.SessionStore over a docstore.Adapter.
//
//   - PutSession and PutState create one record with a conditional create (SaveIf with no previous
//     digest); a record that exists is a 128-bit collision: another token is tried, nothing is
//     overwritten.
//   - GetSession loads the record by the hash of the token and checks the stored Expires on this
//     replica's clock. There is no cache: every authenticated request is one read of the store, and
//     a logout (DeleteSession) is therefore seen by the very next request on any replica. A cache
//     would make a logged-out token work for as long as it lives.
//   - Expiry is fixed when the session is made (as in the in-memory store): a request does not
//     extend it, so Config.SessionTTL is also the longest a session can live.
//   - TakeState is single use across processes. It loads the record, then consumes it with one
//     conditional step on the digest it read: DeleteIf when the store is a
//     docstore.ConditionalDeleter, else SaveIf that replaces it with a "used" marker (deleted right
//     after, and by the sweep if that fails). Exactly one of any number of concurrent calls, on any
//     replica, gets ok=true; a replay finds nothing, or the marker, and gets ok=false.
//   - Sweep deletes by name the records whose expiry hour (minute) is past, reading none of them,
//     and resets the counts.
//   - The numbers are bounded: at MaxSessions (MaxStates) a Put returns auth.ErrStoreFull at once,
//     without touching the store; a live record is never evicted to make room. Each replica counts
//     from its last listing (the sweep) plus what it has written and removed, so replicas may
//     overshoot by what they write between two sweeps, never grow without limit.
//
// Under an encrypting adapter the records are encrypted like any document file (they are not in
// encrypt.DefaultSkip: they hold names, e-mail addresses and the PKCE verifier). The adapter has
// no DeleteIf for an encrypted id, so TakeState uses the marker path there.
//
// Clocks: each replica compares Expires with its own clock; a replica whose clock is ahead refuses
// a session a little early, one that is behind accepts it a little late, by the skew.
type Store struct {
	Store docstore.Adapter
	// MaxSessions and MaxStates are the bounds (DefaultMaxSessions and DefaultMaxStates if not
	// positive).
	MaxSessions, MaxStates int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	sessions, states atomic.Int64 // records: the last listing's count plus this process's Puts less its removals
	sweeping         sync.Mutex
}

// New makes an auth.SessionStore over store, which must be a docstore.ConditionalAdapter
// (ErrNotConditional otherwise); use docstore.SupportsConditional first to know the store really
// honours it. Run RunSweep for the expired records.
func New(store docstore.Adapter) (*Store, error) {
	if _, ok := store.(docstore.ConditionalAdapter); !ok {
		return nil, ErrNotConditional
	}
	return &Store{Store: store}, nil
}

var _ auth.SessionStore = (*Store)(nil)

func (s *Store) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) cas() (docstore.ConditionalAdapter, error) {
	c, ok := s.Store.(docstore.ConditionalAdapter)
	if !ok {
		return nil, ErrNotConditional
	}
	return c, nil
}

// kind is one of the two keyspaces.
type kind struct {
	prefix string
	bucket time.Duration
}

var (
	sessionKind = kind{SessionPrefix, sessionBucket}
	stateKind   = kind{LoginStatePrefix, stateBucket}
)

func (s *Store) count(k kind) *atomic.Int64 {
	if k.prefix == SessionPrefix {
		return &s.sessions
	}
	return &s.states
}

func (s *Store) max(k kind) int64 {
	if k.prefix == SessionPrefix {
		if s.MaxSessions > 0 {
			return int64(s.MaxSessions)
		}
		return DefaultMaxSessions
	}
	if s.MaxStates > 0 {
		return int64(s.MaxStates)
	}
	return DefaultMaxStates
}

func dec(c *atomic.Int64) {
	if c.Add(-1) < 0 {
		c.Store(0)
	}
}

// bucketOf is the number of the bucket in which expires falls, rounded up: the record is certain
// to have expired at bucketEnd(bucket).
func (k kind) bucketOf(expires time.Time) int64 {
	b := int64(k.bucket)
	n := expires.UnixNano()
	return (n + b - 1) / b
}

func (k kind) bucketEnd(bucket int64) time.Time { return time.Unix(0, bucket*int64(k.bucket)) }

// newToken is "<256 random bits>.<bucket>". The bucket is only a hint where the record is (and,
// for the sweep, when it is surely dead): the authority on expiry is the record's Expires.
func (k kind) newToken(bucket int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + strconv.FormatInt(bucket, 10), nil
}

// id is the record name of token, or "" if token is not one this store could have made.
func (k kind) id(token string) string {
	i := strings.LastIndexByte(token, '.')
	if i <= 0 || i == len(token)-1 || len(token) > 128 {
		return ""
	}
	bucket := token[i+1:]
	if n, err := strconv.ParseInt(bucket, 10, 64); err != nil || n < 0 || strconv.FormatInt(n, 10) != bucket {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return k.prefix + bucket + "/" + hex.EncodeToString(sum[:])
}

// idBucket is the bucket in a record name made by id, or false.
func (k kind) idBucket(id string) (int64, bool) {
	rest, ok := strings.CutPrefix(id, k.prefix)
	if !ok {
		return 0, false
	}
	b, _, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(b, 10, 64)
	return n, err == nil && n >= 0
}

// sessionRecord is what a session record holds: the user and when the session was made and ends.
// No secret: the token is not in it.
type sessionRecord struct {
	V        int       `json:"v"`
	User     auth.User `json:"user"`
	IssuedAt time.Time `json:"issuedAt"`
	Expires  time.Time `json:"expires"`
}

// stateRecord is a login state, or, once consumed by a store without DeleteIf, a marker that it
// was used.
type stateRecord struct {
	V       int             `json:"v"`
	State   auth.LoginState `json:"state"`
	Expires time.Time       `json:"expires"`
	Used    bool            `json:"used,omitempty"`
}

// put creates a record under a fresh token with a conditional create.
func (s *Store) put(ctx context.Context, k kind, expires time.Time, body func() ([]byte, error)) (string, error) {
	cas, err := s.cas()
	if err != nil {
		return "", err
	}
	c := s.count(k)
	if c.Load() >= s.max(k) {
		return "", auth.ErrStoreFull // the sweep (or a removal) makes room; never a scan in the request
	}
	data, err := body()
	if err != nil {
		return "", err
	}
	bucket := k.bucketOf(expires)
	for range 3 { // a name that exists is a collision: try another token, never overwrite
		token, err := k.newToken(bucket)
		if err != nil {
			return "", err
		}
		switch err := cas.SaveIf(ctx, k.id(token), data, ""); {
		case err == nil:
			c.Add(1)
			return token, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return "", err
		}
	}
	return "", docstore.ErrConflict
}

// PutSession implements auth.SessionStore.
func (s *Store) PutSession(ctx context.Context, u auth.User, ttl time.Duration) (string, error) {
	now := s.clock()
	expires := now.Add(ttl)
	return s.put(ctx, sessionKind, expires, func() ([]byte, error) {
		return json.Marshal(sessionRecord{V: recordVersion, User: u, IssuedAt: now, Expires: expires})
	})
}

// GetSession implements auth.SessionStore.
func (s *Store) GetSession(ctx context.Context, token string) (auth.User, bool, error) {
	id := sessionKind.id(token)
	if id == "" {
		return auth.User{}, false, nil
	}
	doc, err := s.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		return auth.User{}, false, nil
	}
	if err != nil {
		return auth.User{}, false, err
	}
	var rec sessionRecord
	if json.Unmarshal(doc.Data, &rec) != nil || rec.V != recordVersion || rec.User.ID == "" {
		return auth.User{}, false, nil // not ours: the sweep removes it
	}
	if !s.clock().Before(rec.Expires) {
		return auth.User{}, false, nil // expired; the sweep deletes it
	}
	return rec.User, true, nil
}

// DeleteSession implements auth.SessionStore.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	id := sessionKind.id(token)
	if id == "" {
		return nil
	}
	switch err := s.Store.Delete(ctx, id); {
	case err == nil:
		dec(&s.sessions)
		return nil
	case errors.Is(err, docstore.ErrNotFound):
		return nil
	default:
		return err
	}
}

// PutState implements auth.SessionStore.
func (s *Store) PutState(ctx context.Context, st auth.LoginState, ttl time.Duration) (string, error) {
	expires := s.clock().Add(ttl)
	return s.put(ctx, stateKind, expires, func() ([]byte, error) {
		return json.Marshal(stateRecord{V: recordVersion, State: st, Expires: expires})
	})
}

// TakeState implements auth.SessionStore.
func (s *Store) TakeState(ctx context.Context, token string) (auth.LoginState, bool, error) {
	cas, err := s.cas()
	if err != nil {
		return auth.LoginState{}, false, err
	}
	id := stateKind.id(token)
	if id == "" {
		return auth.LoginState{}, false, nil
	}
	doc, err := s.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		return auth.LoginState{}, false, nil
	}
	if err != nil {
		return auth.LoginState{}, false, err
	}
	var rec stateRecord
	if json.Unmarshal(doc.Data, &rec) != nil || rec.V != recordVersion || rec.Used {
		return auth.LoginState{}, false, nil // consumed by another replica, or not ours: the sweep removes it
	}
	expired := !s.clock().Before(rec.Expires)
	won, err := s.consume(ctx, cas, id, docstore.Digest(doc.Data), rec)
	if err != nil {
		return auth.LoginState{}, false, err
	}
	if !won || expired {
		// An expired record is consumed too (deleted), so it is gone at once, but it is refused.
		return auth.LoginState{}, false, nil
	}
	return rec.State, true, nil
}

// consume removes the record whose content has digest prev with one conditional step and reports
// whether this caller was the one that did it.
func (s *Store) consume(ctx context.Context, cas docstore.ConditionalAdapter, id, prev string, rec stateRecord) (bool, error) {
	if d, ok := s.Store.(docstore.ConditionalDeleter); ok {
		switch err := d.DeleteIf(ctx, id, prev); {
		case err == nil:
			dec(&s.states)
			return true, nil
		case errors.Is(err, docstore.ErrNotFound), errors.Is(err, docstore.ErrConflict):
			return false, nil
		case !errors.Is(err, docstore.ErrConditionalUnsupported):
			return false, err
		}
	}
	// No conditional delete (an encrypting adapter has none for an encrypted id): claim it by
	// replacing it, conditionally, with a used marker that keeps nothing of the state.
	marker, _ := json.Marshal(stateRecord{V: recordVersion, Expires: rec.Expires, Used: true})
	switch err := cas.SaveIf(ctx, id, marker, prev); {
	case err == nil:
	case errors.Is(err, docstore.ErrConflict):
		return false, nil
	default:
		return false, err
	}
	dec(&s.states)
	// Only the winner gets here, and nobody else writes a marker: a plain delete is safe. If it
	// fails the marker stays refusing, and the sweep deletes it by its name.
	_ = s.Store.Delete(ctx, id)
	return true, nil
}

// Sweep deletes the records whose expiry bucket ended more than a minute ago, sessions and login
// states (used markers included: they carry their state's bucket), and sets the counts to what is
// left. It lists the two keyspaces and reads no record; a name that is not one this store made is
// left alone. It returns how many it deleted. Several sweeps at once (replicas) are harmless; in
// one process they run one at a time.
func (s *Store) Sweep(ctx context.Context) (int, error) {
	s.sweeping.Lock()
	defer s.sweeping.Unlock()
	now := s.clock()
	total := 0
	var errs []error
	for _, k := range []kind{sessionKind, stateKind} {
		metas, err := docstore.ListPrefix(ctx, s.Store, k.prefix)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		left := 0
		for _, m := range metas {
			if err := ctx.Err(); err != nil {
				return total, err
			}
			b, ok := k.idBucket(m.ID)
			if !ok || now.Before(k.bucketEnd(b).Add(sweepGrace)) {
				left++
				continue
			}
			switch err := s.Store.Delete(ctx, m.ID); {
			case err == nil || errors.Is(err, docstore.ErrNotFound):
				total++
			default:
				errs = append(errs, err)
				left++
			}
		}
		s.count(k).Store(int64(left))
	}
	return total, errors.Join(errs...)
}

// RunSweep calls Sweep at once and then every interval (a minute when not positive) until ctx
// ends; errors are logged. The first sweep is what sets the counts from the store at start-up.
func (s *Store) RunSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	sweep := func() {
		if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			log.Printf("sessionstore: sweeping shared sessions: %v", err)
		}
	}
	sweep()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

// ParseMode parses GOJIBERRY_DOCSTORE_SESSIONS: "" or "memory" (sessions in each process), "store"
// (kept in the store, shared by replicas; on a store without conditional writes the server warns
// and uses memory) or "store-required" (as "store", but the server does not start without
// conditional writes). It returns the canonical name.
func ParseMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store", "store-required":
		return m, nil
	}
	return "", errors.New("parsing GOJIBERRY_DOCSTORE_SESSIONS \"" + raw + "\": want \"memory\", \"store\" or \"store-required\"")
}
