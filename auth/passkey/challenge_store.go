package passkey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// ChallengePrefix is the keyspace of the ceremonies a StoreChallenges keeps:
// "passkey/challenge/<hash of the token>". It is inside "passkey/", so the document API hides it
// (Server.Hide), expiry never gives it a deadline (it is a system keyspace) and IsDocumentKey
// rejects it. The token itself is never in a record name: someone who can list the store cannot
// finish a ceremony with what they see.
const ChallengePrefix = "passkey/challenge/"

// ErrNotConditional is returned by NewStoreChallenges for a store that cannot write
// conditionally: without a compare-and-set no two replicas can agree on who consumed a challenge.
var ErrNotConditional = errors.New("passkey: a shared challenge store needs a store with conditional writes (docstore.ConditionalAdapter)")

// storeChallengeGrace is how long past its expiry a record is left before the sweep deletes it
// (replicas' clocks differ; Take already refuses an expired one on its own clock).
const storeChallengeGrace = time.Minute

// minFullSweep is the shortest time between two sweeps forced by a full store, so a flood
// of Puts at the cap costs one listing a second, not one each.
const minFullSweep = time.Second

// StoreChallenges is a ChallengeStore kept in a docstore.Adapter, so replicas that share the
// store share their ceremonies: a challenge begun on one finishes on another.
//
//   - Put creates one record with a conditional create (SaveIf with no previous digest).
//   - Take is single use across processes. It reads the record, then consumes it with one
//     conditional step on the digest it read: DeleteIf when the store is a
//     docstore.ConditionalDeleter, else SaveIf that replaces it with a "used" marker. Exactly one
//     of any number of concurrent Takes (here or on other replicas) gets nil from that step; the
//     others see a conflict or a missing record and report ok=false. The marker is deleted right
//     after, and by the sweep if that fails.
//   - Expiry is the Expires time stored in the record, checked on every Take, plus a sweep
//     (Sweep, RunChallengeSweep) that deletes what has expired, what was consumed and what cannot
//     be read.
//   - The number waiting is bounded like MemoryChallenges: at Max (10,000 by default) Put sweeps,
//     at most once a second, and returns ErrBusy if it is still full. The bound is per replica's
//     view of the store (each keeps a count from its last listing plus what it has written), so
//     replicas may overshoot it by what they write between two listings, never grow without limit.
//     A live challenge is never evicted to make room: that would let a flood cancel real
//     ceremonies.
//
// Clocks: each replica compares Expires with its own clock; a replica whose clock is ahead refuses
// a challenge a little early, one that is behind accepts it a little late, by the skew.
type StoreChallenges struct {
	Store docstore.Adapter
	// Max is how many ceremonies may wait (10,000 if not positive).
	Max int
	// Now is the clock (time.Now if nil), for tests.
	Now func() time.Time

	approx    atomic.Int64 // waiting records: the last listing's count plus this process's Puts less its Takes
	sweeping  sync.Mutex
	lastSweep atomic.Int64 // unix nanoseconds of the last sweep started
}

// NewStoreChallenges makes a ChallengeStore over store, holding at most max ceremonies (10,000 if
// max <= 0). store must be a docstore.ConditionalAdapter (ErrNotConditional otherwise); use
// docstore.SupportsConditional first to know the store really honours it.
func NewStoreChallenges(store docstore.Adapter, max int) (*StoreChallenges, error) {
	if _, ok := store.(docstore.ConditionalAdapter); !ok {
		return nil, ErrNotConditional
	}
	return &StoreChallenges{Store: store, Max: max}, nil
}

func (s *StoreChallenges) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *StoreChallenges) max() int {
	if s.Max > 0 {
		return s.Max
	}
	return maxChallenges
}

func (s *StoreChallenges) cas() (docstore.ConditionalAdapter, error) {
	c, ok := s.Store.(docstore.ConditionalAdapter)
	if !ok {
		return nil, ErrNotConditional
	}
	return c, nil
}

func challengeID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return ChallengePrefix + hex.EncodeToString(sum[:])
}

// storedChallenge is a record: the ceremony, or, once consumed by a store without DeleteIf, a
// marker that it was used.
type storedChallenge struct {
	Challenge
	Used bool `json:"used,omitempty"`
}

// Put implements ChallengeStore.
func (s *StoreChallenges) Put(ctx context.Context, c Challenge) (string, error) {
	cas, err := s.cas()
	if err != nil {
		return "", err
	}
	if s.approx.Load() >= int64(s.max()) {
		// Full by our count: drop what has expired (at most once a second), then look again.
		if now := s.clock().UnixNano(); now-s.lastSweep.Load() >= int64(minFullSweep) {
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() != nil {
				return "", ctx.Err()
			}
		}
		if s.approx.Load() >= int64(s.max()) {
			return "", ErrBusy
		}
	}
	data, err := json.Marshal(storedChallenge{Challenge: c})
	if err != nil {
		return "", err
	}
	for range 3 { // a name that exists is a 128-bit collision: try another token, never overwrite
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		token := base64.RawURLEncoding.EncodeToString(b)
		switch err := cas.SaveIf(ctx, challengeID(token), data, ""); {
		case err == nil:
			s.approx.Add(1)
			return token, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return "", err
		}
	}
	return "", docstore.ErrConflict
}

// Take implements ChallengeStore.
func (s *StoreChallenges) Take(ctx context.Context, token string) (Challenge, bool, error) {
	cas, err := s.cas()
	if err != nil {
		return Challenge{}, false, err
	}
	id := challengeID(token)
	doc, err := s.Store.Load(ctx, id)
	if errors.Is(err, docstore.ErrNotFound) {
		return Challenge{}, false, nil
	}
	if err != nil {
		return Challenge{}, false, err
	}
	prev := docstore.Digest(doc.Data)
	var sc storedChallenge
	if json.Unmarshal(doc.Data, &sc) != nil || sc.Used {
		return Challenge{}, false, nil // consumed by another replica, or not ours: the sweep removes it
	}
	expired := s.clock().After(sc.Expires)
	won, err := s.consume(ctx, cas, id, prev, sc)
	if err != nil {
		return Challenge{}, false, err
	}
	if !won || expired {
		// An expired record is consumed too (deleted), so it is gone at once, but it is refused.
		return Challenge{}, false, nil
	}
	return sc.Challenge, true, nil
}

// consume removes the record whose content has digest prev with one conditional step and reports
// whether this caller was the one that did it.
func (s *StoreChallenges) consume(ctx context.Context, cas docstore.ConditionalAdapter, id, prev string, sc storedChallenge) (bool, error) {
	if d, ok := s.Store.(docstore.ConditionalDeleter); ok {
		switch err := d.DeleteIf(ctx, id, prev); {
		case err == nil:
			s.dec()
			return true, nil
		case errors.Is(err, docstore.ErrNotFound), errors.Is(err, docstore.ErrConflict):
			return false, nil
		case !errors.Is(err, docstore.ErrConditionalUnsupported):
			return false, err
		}
	}
	// No conditional delete: claim it by replacing it, conditionally, with a used marker.
	marker, _ := json.Marshal(storedChallenge{Challenge: Challenge{Expires: sc.Expires}, Used: true})
	switch err := cas.SaveIf(ctx, id, marker, prev); {
	case err == nil:
	case errors.Is(err, docstore.ErrConflict):
		return false, nil
	default:
		return false, err
	}
	s.dec()
	// Only the winner gets here, and nobody writes a marker: a plain delete is safe. If it fails
	// the marker stays refusing, and the sweep deletes it.
	_ = s.Store.Delete(ctx, id)
	return true, nil
}

func (s *StoreChallenges) dec() {
	if s.approx.Add(-1) < 0 {
		s.approx.Store(0)
	}
}

// Sweep deletes the records that are expired (past a minute of grace), already used, or cannot be
// read and are over an hour old, each with a conditional delete so a record that changed in
// between is kept, and resets the count of what is waiting to what is left. It returns how many it
// deleted. Several sweeps at once (replicas, or Put) are harmless; in one process they run one at
// a time.
func (s *StoreChallenges) Sweep(ctx context.Context) (int, error) {
	s.sweeping.Lock()
	defer s.sweeping.Unlock()
	now := s.clock()
	s.lastSweep.Store(now.UnixNano())
	metas, err := docstore.ListPrefix(ctx, s.Store, ChallengePrefix)
	if err != nil {
		return 0, err
	}
	n, left := 0, 0
	var errs []error
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		doc, err := s.Store.Load(ctx, m.ID)
		if errors.Is(err, docstore.ErrNotFound) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			left++
			continue
		}
		var sc storedChallenge
		stale := false
		if json.Unmarshal(doc.Data, &sc) != nil {
			stale = now.Sub(doc.UpdatedAt) > time.Hour
		} else {
			stale = sc.Used || now.After(sc.Expires.Add(storeChallengeGrace))
		}
		if !stale {
			left++
			continue
		}
		switch gone, err := s.deleteIfSame(ctx, m.ID, doc.Data); {
		case err != nil:
			errs = append(errs, err)
			left++
		case gone:
			n++
		default:
			left++
		}
	}
	s.approx.Store(int64(left))
	return n, errors.Join(errs...)
}

func (s *StoreChallenges) deleteIfSame(ctx context.Context, id string, data []byte) (bool, error) {
	prev := docstore.Digest(data)
	if d, ok := s.Store.(docstore.ConditionalDeleter); ok {
		switch err := d.DeleteIf(ctx, id, prev); {
		case err == nil || errors.Is(err, docstore.ErrNotFound):
			return true, nil
		case errors.Is(err, docstore.ErrConflict):
			return false, nil
		case !errors.Is(err, docstore.ErrConditionalUnsupported):
			return false, err
		}
	}
	// Claim with a conditional write that keeps the record dead (used, long expired), then delete.
	if c, ok := s.Store.(docstore.ConditionalAdapter); ok {
		marker, _ := json.Marshal(storedChallenge{Used: true})
		switch err := c.SaveIf(ctx, id, marker, prev); {
		case errors.Is(err, docstore.ErrConflict):
			return false, nil
		case err != nil && !errors.Is(err, docstore.ErrConditionalUnsupported):
			return false, err
		}
	}
	if err := s.Store.Delete(ctx, id); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return false, err
	}
	return true, nil
}

// RunChallengeSweep calls Sweep every interval (a minute when not positive) until ctx ends;
// errors are logged.
func (s *StoreChallenges) RunChallengeSweep(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				log.Printf("passkey: sweeping shared challenges: %v", err)
			}
		}
	}
}

// ParseChallengeMode parses GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES: "" or "memory" (ceremonies in
// each process) or "store" (kept in the store, shared by replicas). It returns the canonical name.
func ParseChallengeMode(raw string) (string, error) {
	switch m := strings.ToLower(strings.TrimSpace(raw)); m {
	case "", "memory":
		return "memory", nil
	case "store":
		return "store", nil
	}
	return "", errors.New("parsing GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES \"" + raw + "\": want \"memory\" or \"store\"")
}
