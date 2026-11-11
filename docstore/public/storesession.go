package public

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// SessionKeyPrefix is where StoreSessions keeps sessions.
const SessionKeyPrefix = docstore.PublicPrefix + "session/"

// DefaultSessionRefresh is how often StoreSessions rebuilds its counts.
const DefaultSessionRefresh = 30 * time.Second

// ErrSessionExists is returned by StoreSessions.Create for an existing ID.
var ErrSessionExists = errors.New("public: a session with this key exists")

// StoreSessions is a SessionStore that keeps sessions in the store.
type StoreSessions struct {
	Store docstore.Adapter
	// Max is the cap on live sessions.
	Max int
	// Refresh is how often the counts are rebuilt.
	Refresh time.Duration

	mu         sync.Mutex
	counts     sessionCounts
	counted    time.Time // when the counts were last rebuilt; zero: never
	refreshing bool
	during     []sessionDelta // changes this process made while a rebuild was reading
}

type sessionCounts struct {
	total                   int
	byClient, byNet, byWide map[string]int
}

type sessionDelta struct {
	s    Session
	sign int
}

func newSessionCounts() sessionCounts {
	return sessionCounts{byClient: map[string]int{}, byNet: map[string]int{}, byWide: map[string]int{}}
}

func (c *sessionCounts) add(s Session, sign int) {
	c.total = max(c.total+sign, 0)
	bump := func(m map[string]int, k string) {
		if k == "" {
			return
		}
		if m[k]+sign <= 0 {
			delete(m, k)
			return
		}
		m[k] += sign
	}
	bump(c.byClient, s.Client)
	bump(c.byNet, s.Net)
	bump(c.byWide, s.Wide)
}

// NewStoreSessions makes a SessionStore over store.
func NewStoreSessions(store docstore.Adapter, max int) (*StoreSessions, error) {
	if _, ok := store.(docstore.ConditionalAdapter); !ok {
		return nil, errors.New("public: a StoreSessions needs a store with conditional writes")
	}
	return &StoreSessions{Store: store, Max: max}, nil
}

var (
	_ SessionStore  = (*StoreSessions)(nil)
	_ SessionReaper = (*StoreSessions)(nil)
)

type sessionRecord struct {
	ID      string    `json:"id"`
	DocID   string    `json:"doc,omitempty"`
	Client  string    `json:"client,omitempty"`
	Net     string    `json:"net,omitempty"`
	Wide    string    `json:"wide,omitempty"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

func (r sessionRecord) session() Session {
	return Session(r)
}

func recordOf(s Session) sessionRecord {
	return sessionRecord(s)
}

func sessionID(key string) string {
	if len(key) == 64 {
		if _, err := hex.DecodeString(key); err == nil && key == lower(key) {
			return SessionKeyPrefix + key
		}
	}
	return SessionKeyPrefix + TokenKey(key)
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func (m *StoreSessions) cas() (docstore.ConditionalAdapter, error) {
	c, ok := m.Store.(docstore.ConditionalAdapter)
	if !ok {
		return nil, errors.New("public: a StoreSessions needs a store with conditional writes")
	}
	return c, nil
}

func (m *StoreSessions) cap() int {
	if m.Max > 0 {
		return m.Max
	}
	return DefaultMaxSessions
}

func (m *StoreSessions) every() time.Duration {
	if m.Refresh > 0 {
		return m.Refresh
	}
	return DefaultSessionRefresh
}

func (m *StoreSessions) load(ctx context.Context, id string) (sessionRecord, string, bool, error) {
	doc, err := m.Store.Load(ctx, id)
	switch {
	case errors.Is(err, docstore.ErrNotFound):
		return sessionRecord{}, "", false, nil
	case err != nil:
		return sessionRecord{}, "", false, err
	}
	var rec sessionRecord
	if json.Unmarshal(doc.Data, &rec) != nil {
		return sessionRecord{}, "", false, nil // a damaged record is no session
	}
	return rec, docstore.Digest(doc.Data), true, nil
}

func (m *StoreSessions) recount(ctx context.Context, now time.Time, reap bool) (int, error) {
	m.mu.Lock()
	if m.refreshing {
		m.mu.Unlock()
		return 0, nil
	}
	m.refreshing, m.during = true, nil
	m.mu.Unlock()

	counts, deleted, err := m.scan(ctx, now, reap)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshing = false
	if err == nil {
		for _, d := range m.during {
			counts.add(d.s, d.sign)
		}
		m.counts, m.counted = counts, now
	}
	m.during = nil
	return deleted, err
}

func (m *StoreSessions) scan(ctx context.Context, now time.Time, reap bool) (sessionCounts, int, error) {
	counts, deleted := newSessionCounts(), 0
	metas, err := docstore.ListPrefix(ctx, m.Store, SessionKeyPrefix)
	if err != nil {
		return counts, 0, err
	}
	var errs []error
	for _, meta := range metas {
		if err := ctx.Err(); err != nil {
			return counts, deleted, err
		}
		rec, _, ok, err := m.load(ctx, meta.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok && now.Before(rec.Expires) {
			counts.add(rec.session(), 1)
			continue
		}
		if reap {
			switch err := m.Store.Delete(ctx, meta.ID); {
			case err == nil:
				deleted++
			case errors.Is(err, docstore.ErrNotFound):
			default:
				errs = append(errs, err)
			}
		}
	}
	return counts, deleted, errors.Join(errs...)
}

// Reap implements SessionReaper: it deletes expired sessions.
func (m *StoreSessions) Reap(ctx context.Context, now time.Time) (int, error) {
	return m.recount(ctx, now, true)
}

// Create implements SessionStore.
func (m *StoreSessions) Create(ctx context.Context, key string, s Session, now time.Time, caps SessionCaps) error {
	cas, err := m.cas()
	if err != nil {
		return err
	}
	m.mu.Lock()
	stale := m.counted.IsZero() || now.Sub(m.counted) >= m.every()
	m.mu.Unlock()
	if stale {
		if _, err := m.recount(ctx, now, false); err != nil {
			return err
		}
	}

	m.mu.Lock()
	c := m.counts
	known := func() bool {
		switch {
		case s.Wide != "":
			return c.byWide[s.Wide] > 0
		case s.Net != "":
			return c.byNet[s.Net] > 0
		}
		return false
	}
	var refuse error
	switch {
	case c.total >= m.cap():
		refuse = ErrSessionsFull
	case caps.PerClient > 0 && s.Client != "" && c.byClient[s.Client] >= caps.PerClient:
		refuse = ErrClientSessionsFull
	case caps.PerNet > 0 && s.Net != "" && c.byNet[s.Net] >= caps.PerNet:
		refuse = ErrNetSessionsFull
	case caps.PerWide > 0 && s.Wide != "" && c.byWide[s.Wide] >= caps.PerWide:
		refuse = ErrWideSessionsFull
	case caps.ReservePercent > 0 && known() && c.total >= m.cap()-m.cap()*min(caps.ReservePercent, 100)/100:
		refuse = ErrSessionsFull
	}
	if refuse != nil {
		m.mu.Unlock()
		return refuse
	}
	m.note(s, 1)
	m.mu.Unlock()

	data, _ := json.Marshal(recordOf(s))
	if err := cas.SaveIf(ctx, sessionID(key), data, ""); err != nil {
		m.mu.Lock()
		m.note(s, -1)
		m.mu.Unlock()
		if errors.Is(err, docstore.ErrConflict) {
			return ErrSessionExists
		}
		return err
	}
	return nil
}

func (m *StoreSessions) note(s Session, sign int) {
	m.counts.add(s, sign)
	if m.refreshing {
		m.during = append(m.during, sessionDelta{s, sign})
	}
}

// Get implements SessionStore.
func (m *StoreSessions) Get(ctx context.Context, key string, now time.Time) (Session, bool, error) {
	rec, _, ok, err := m.load(ctx, sessionID(key))
	if err != nil || !ok || !now.Before(rec.Expires) {
		return Session{}, false, err
	}
	return rec.session(), true, nil
}

func (m *StoreSessions) update(ctx context.Context, key string, now time.Time, change func(*sessionRecord) bool) (bool, error) {
	cas, err := m.cas()
	if err != nil {
		return false, err
	}
	id := sessionID(key)
	for range storeQuotaRetries {
		rec, prev, ok, err := m.load(ctx, id)
		if err != nil {
			return false, err
		}
		if !ok || !now.Before(rec.Expires) {
			return false, ErrNoSession
		}
		if !change(&rec) {
			return false, nil
		}
		data, _ := json.Marshal(rec)
		switch err := cas.SaveIf(ctx, id, data, prev); {
		case err == nil:
			return true, nil
		case errors.Is(err, docstore.ErrConflict):
			continue
		default:
			return false, err
		}
	}
	return false, errors.New("public: the session record is too busy to update")
}

// SetDoc implements SessionStore with a compare-and-swap.
func (m *StoreSessions) SetDoc(ctx context.Context, key, old, newDoc string, now time.Time) (bool, error) {
	return m.update(ctx, key, now, func(r *sessionRecord) bool {
		if r.DocID != old {
			return false
		}
		r.DocID = newDoc
		return true
	})
}

// Extend implements SessionStore.
func (m *StoreSessions) Extend(ctx context.Context, key string, expires, now time.Time) error {
	_, err := m.update(ctx, key, now, func(r *sessionRecord) bool {
		if !expires.After(r.Expires) {
			return false
		}
		r.Expires = expires
		return true
	})
	return err
}

// Delete implements SessionStore.
func (m *StoreSessions) Delete(ctx context.Context, key string) error {
	id := sessionID(key)
	rec, _, ok, err := m.load(ctx, id)
	if err != nil {
		return err
	}
	if err := m.Store.Delete(ctx, id); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	if ok {
		m.mu.Lock()
		m.note(rec.session(), -1)
		m.mu.Unlock()
	}
	return nil
}
