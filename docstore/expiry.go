package docstore

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"strings"
	"sync"
	"time"
)

// ExpiryPrefix is the keyspace of deadline records.
const ExpiryPrefix = "expiry:"

// ExpiryCreatedPrefix is the keyspace of creation-time records.
const ExpiryCreatedPrefix = "expiry-created:"

// ExpiryPolicy decides how long a document lives.
type ExpiryPolicy interface {
	TTL(id string, now time.Time) (time.Duration, bool)
}

// ExpiryPolicyFunc makes a function an ExpiryPolicy.
type ExpiryPolicyFunc func(id string, now time.Time) (time.Duration, bool)

// TTL implements ExpiryPolicy.
func (f ExpiryPolicyFunc) TTL(id string, now time.Time) (time.Duration, bool) { return f(id, now) }

// Keyspaces other parts of the program keep in the store.
const (
	collabPrefix     = "collab:"      // shared-editing hub state (collab/hub.StoreKey)
	collabGonePrefix = "collab-gone:" // the hub's marks of documents closed as expired (collab/hub)
	catalogPrefix    = "catalog:"     // catalogs and folder records (saver)
	versionsPrefix   = "versions/"    // version history (saver.VersionPrefix)
	passkeyPrefix    = "passkey/"     // passkey accounts and handles (auth/passkey)
	keysPrefix       = "keys:"        // per-document wrapped keys (encrypt.KeysPrefix); losing one makes the document unreadable
	fontsPrefix      = "fonts/"       // the font library (fonts.KeyPrefix, saver.FontPrefix)
)

// Keyspaces of the public (anonymous) offer.
const (
	PublicPrefix = "public/" // document metadata, view-id records and tombstones of the public offer
	ReportPrefix = "report/" // abuse reports about public documents
)

// Keyspaces of shared sign-in sessions.
const (
	SessionPrefix    = "auth/session/" // bearer sessions, "auth/session/<expiry hour>/<hash>"
	LoginStatePrefix = "auth/state/"   // OpenID Connect login states, "auth/state/<expiry minute>/<hash>"
)

// systemPrefixes are keyspaces that are not documents.
const RevokedSignerPrefix = "revoked-signer/"

var systemPrefixes = []string{ExpiryPrefix, ExpiryCreatedPrefix, aclKeyPrefix, collabPrefix, collabGonePrefix, catalogPrefix, versionsPrefix, DirectoryPrefix, DirectoryLogPrefix, DirectoryEnrollPrefix, RateLimitPrefix, ConfigPrefix, DeviceUsePrefix, SessionPrefix, LoginStatePrefix, passkeyPrefix, keysPrefix, fontsPrefix, FolderSecretPrefix, PublicPrefix, ReportPrefix, RevokedSignerPrefix}

// IsDocumentKey reports whether key is a plain document key.
func IsDocumentKey(key string) bool {
	if key == "" {
		return false
	}
	lower := strings.ToLower(key)
	// a backend that folds case would alias them
	for _, p := range systemPrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	first, _, _ := strings.Cut(key, "/")
	return !strings.Contains(first, ":")
}

// FixedTTL gives every document the same lifetime.
func FixedTTL(d time.Duration) ExpiryPolicy {
	return ExpiryPolicyFunc(func(id string, _ time.Time) (time.Duration, bool) {
		if !IsDocumentKey(id) {
			return 0, false
		}
		return d, d > 0
	})
}

// Expirer is an optional capability of an adapter.
type Expirer interface {
	// Expiry returns the instant document id expires.
	Expiry(ctx context.Context, id string) (time.Time, bool, error)
	// Expiries returns the deadline of every document.
	Expiries(ctx context.Context) (map[string]time.Time, error)
}

func logicalID(id string) string {
	if strings.Contains(id, "/") {
		return id
	}
	s := strings.TrimSuffix(id, path.Ext(id))
	if i := strings.LastIndex(s, "~"); i > 0 && i < len(s)-1 && strings.Trim(s[i+1:], "0123456789abcdef") == "" {
		s = s[:i]
	}
	if s == "" {
		return id
	}
	return s
}

func relatedSets(id string) (own, ownPrefixes, group, groupPrefixes []string) {
	own = []string{aclKeyPrefix + id, collabPrefix + id, keysPrefix + id}
	logical := logicalID(id)
	if logical != id {
		ownPrefixes = []string{versionsPrefix + id + "/"}
		group = []string{aclKeyPrefix + logical, collabPrefix + logical, keysPrefix + logical}
	}
	return own, ownPrefixes, group, []string{versionsPrefix + logical + "/"}
}

// DefaultRelated lists what belongs to a document.
func DefaultRelated(id string) (exact, prefixes []string) {
	own, ownP, group, groupP := relatedSets(id)
	return append(own, group...), append(ownP, groupP...)
}

// ExpiringAdapter gives any Adapter documents that expire.
type ExpiringAdapter struct {
	Inner  Adapter
	Policy ExpiryPolicy
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Related names what is deleted along with a document.
	Related func(id string) (exact, prefixes []string)
	// OnError receives the errors of Run's sweeps.
	OnError func(error)
	// OnExpired, when set, is told how many documents expired.
	OnExpired func(deleted int)
	// AdoptUpdatedAt makes the first sweep adopt update times.
	AdoptUpdatedAt bool
	// DeadlineCacheTTL is how long listings may be reused.
	DeadlineCacheTTL time.Duration
	// MaxAge, when positive, caps a document's absolute age.
	MaxAge time.Duration
	// OnSave, when set, is called after every save.
	OnSave func(id string, size int)
	// Guard, when set, is asked before every write.
	Guard func(id string, size int) (done func(stored bool), err error)
	// OnDelete, when set, is called after a document is deleted.
	OnDelete func(id string, size int)
	// Orphans, when set, makes Sweep also delete orphaned records.
	Orphans OrphanPolicy

	badMu sync.Mutex      // guards bad
	bad   map[string]bool // ids whose unreadable deadline record has been logged

	mu      sync.Mutex // serializes writes and the per-document part of a sweep
	adopted bool       // the first sweep has given unrecorded documents a deadline

	cacheMu  sync.Mutex
	cache    map[string]time.Time
	cacheAt  time.Time
	cacheGen uint64
}

// NewExpiringAdapter wraps inner with the policy.
func NewExpiringAdapter(inner Adapter, policy ExpiryPolicy) *ExpiringAdapter {
	return &ExpiringAdapter{Inner: inner, Policy: policy}
}

type Renewer interface {
	Renew(ctx context.Context, docID string) error
}

var (
	_ Renewer            = (*ExpiringAdapter)(nil)
	_ ConditionalAdapter = (*ExpiringAdapter)(nil)
	_ Locker             = (*ExpiringAdapter)(nil)
	_ PrefixLister       = (*ExpiringAdapter)(nil)
	_ QuotaReporter      = (*ExpiringAdapter)(nil)
	_ Expirer            = (*ExpiringAdapter)(nil)
)

func (e *ExpiringAdapter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func isExpiryKey(id string) bool {
	return strings.HasPrefix(id, ExpiryPrefix) || strings.HasPrefix(id, ExpiryCreatedPrefix)
}

func (e *ExpiringAdapter) capped(deadline, created time.Time) time.Time {
	if e.MaxAge > 0 && !created.IsZero() {
		if c := created.Add(e.MaxAge); c.Before(deadline) {
			return c
		}
	}
	return deadline
}

func (e *ExpiringAdapter) effective(r expiryRecord) time.Time { return e.capped(r.deadline, r.created) }

func (e *ExpiringAdapter) liveSibling(ctx context.Context, id, logical string) (bool, error) {
	metas, err := ListPrefix(ctx, e.Inner, logical)
	if err != nil {
		return false, err
	}
	for _, m := range metas {
		if m.ID == id || !IsDocumentKey(m.ID) || logicalID(m.ID) != logical {
			continue
		}
		t, ok, err := e.readRecord(ctx, m.ID)
		if err != nil {
			return false, err
		}
		if !ok || !e.expired(t) {
			return true, nil
		}
	}
	return false, nil
}

func (e *ExpiringAdapter) relatedFor(ctx context.Context, id string) (exact, prefixes []string, err error) {
	if e.Related != nil {
		exact, prefixes = e.Related(id)
		return exact, prefixes, nil
	}
	own, ownP, group, groupP := relatedSets(id)
	if live, err := e.liveSibling(ctx, id, logicalID(id)); err != nil {
		return nil, nil, err
	} else if live {
		return own, ownP, nil
	}
	return append(own, group...), append(ownP, groupP...), nil
}

var errRenewed = errors.New("docstore: the document was renewed while it was being expired")

func (e *ExpiringAdapter) deleteExpired(ctx context.Context, id string) (found bool, err error) {
	cd, ok := e.Inner.(ConditionalDeleter)
	if !ok {
		if err := e.Inner.Delete(ctx, id); errors.Is(err, ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, nil
	}
	doc, err := e.Inner.Load(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if t, has, err := e.readRecord(ctx, id); err != nil {
		return false, err
	} else if has && !e.expired(t) {
		return false, errRenewed
	}
	switch err := cd.DeleteIf(ctx, id, Digest(doc.Data)); {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	case errors.Is(err, ErrConflict):
		return false, errRenewed
	case errors.Is(err, ErrConditionalUnsupported):
		if err := e.Inner.Delete(ctx, id); errors.Is(err, ErrNotFound) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, err
	}
}

// e.mu held; record goes last so a crash is retried
func (e *ExpiringAdapter) purgeLocked(ctx context.Context, id string, expired bool) (bool, error) {
	var found bool
	if expired {
		var err error
		if found, err = e.deleteExpired(ctx, id); err != nil {
			return false, err
		}
	} else {
		found = true
		if err := e.Inner.Delete(ctx, id); errors.Is(err, ErrNotFound) {
			found = false
		} else if err != nil {
			return false, err
		}
	}
	if IsDocumentKey(id) || e.Related != nil {
		exact, prefixes, err := e.relatedFor(ctx, id)
		if err != nil {
			return found, err
		}
		for _, r := range exact {
			if err := e.Inner.Delete(ctx, r); err != nil && !errors.Is(err, ErrNotFound) {
				return found, err
			}
		}
		for _, p := range prefixes {
			metas, err := ListPrefix(ctx, e.Inner, p)
			if err != nil {
				return found, err
			}
			for _, m := range metas {
				if strings.Contains(strings.TrimPrefix(m.ID, p), "/") {
					continue
				}
				if err := e.Inner.Delete(ctx, m.ID); err != nil && !errors.Is(err, ErrNotFound) {
					return found, err
				}
			}
		}
	}
	return found, e.deleteRecord(ctx, id)
}

func (e *ExpiringAdapter) renewed(ctx context.Context, id string, write func() error) error {
	if isExpiryKey(id) {
		return fmt.Errorf("docstore: %q is in the reserved expiry keyspace", id)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	prev, had, err := e.readRecordFull(ctx, id)
	if err != nil {
		return err
	}
	if had && !now.Before(e.effective(prev)) {
		switch _, err := e.purgeLocked(ctx, id, true); {
		case errors.Is(err, errRenewed):
			if prev, had, err = e.readRecordFull(ctx, id); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			had, prev = false, expiryRecord{}
		}
	}
	ttl, ok := e.Policy.TTL(id, now)
	if !ok {
		if had {
			if err := e.deleteRecord(ctx, id); err != nil {
				return err
			}
		}
		return write()
	}
	created := now
	if had && !prev.created.IsZero() {
		created = prev.created // a renewal keeps the document's age
	}
	if had && !prev.created.IsZero() {
		err = e.writeDeadline(ctx, id, now.Add(ttl)) // the creation time is already stored
	} else {
		err = e.writeRecord(ctx, id, expiryRecord{deadline: now.Add(ttl), created: created})
	}
	if err != nil {
		return err
	}
	if err := write(); err != nil {
		if had {
			_ = e.writeRecord(ctx, id, prev)
		} else {
			_ = e.deleteRecord(ctx, id)
		}
		return err
	}
	return nil
}

// Save implements Adapter; it stores the document with its deadline.
func (e *ExpiringAdapter) Save(ctx context.Context, id string, data []byte) error {
	done, err := e.guard(id, len(data))
	if err != nil {
		return err
	}
	if err := e.renewed(ctx, id, func() error { return e.Inner.Save(ctx, id, data) }); err != nil {
		done(false)
		return err
	}
	done(true)
	e.saved(id, len(data))
	return nil
}

// the done it returns is never nil
func (e *ExpiringAdapter) guard(id string, size int) (func(bool), error) {
	if e.Guard == nil {
		return func(bool) {}, nil
	}
	done, err := e.Guard(id, size)
	if err != nil {
		return nil, err
	}
	if done == nil {
		done = func(bool) {}
	}
	return done, nil
}

func (e *ExpiringAdapter) saved(id string, size int) {
	if f := e.OnSave; f != nil {
		f(id, size)
	}
}

// SaveIf implements ConditionalAdapter.
func (e *ExpiringAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	c, ok := e.Inner.(ConditionalAdapter)
	if !ok {
		return ErrConditionalUnsupported
	}
	done, err := e.guard(id, len(data))
	if err != nil {
		return err
	}
	if err := e.renewed(ctx, id, func() error { return c.SaveIf(ctx, id, data, prev) }); err != nil {
		done(false)
		return err
	}
	done(true)
	e.saved(id, len(data))
	return nil
}

// Renew implements Renewer; it is Touch.
func (e *ExpiringAdapter) Renew(ctx context.Context, id string) error { return e.Touch(ctx, id) }

// Touch renews document id as a Save would.
func (e *ExpiringAdapter) Touch(ctx context.Context, id string) error {
	if isExpiryKey(id) {
		return fmt.Errorf("docstore: %q is in the reserved expiry keyspace", id)
	}
	if id == "" {
		return ErrNotFound
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	metas, err := ListPrefix(ctx, e.Inner, id)
	if err != nil {
		return err
	}
	live := func(m Meta) (bool, error) {
		t, has, err := e.readRecord(ctx, m.ID)
		if err != nil {
			return false, err
		}
		return !has || now.Before(t), nil
	}
	var gens []Meta
	for _, m := range metas {
		if isExpiryKey(m.ID) {
			continue
		}
		if m.ID == id {
			ok, err := live(m)
			if err != nil {
				return err
			}
			if ok {
				return e.touchLocked(ctx, m.ID, now)
			}
			continue // an expired exact key does not shadow the live generations
		}
		if !IsDocumentKey(m.ID) || logicalID(m.ID) != id {
			continue
		}
		if ok, err := live(m); err != nil {
			return err
		} else if ok {
			gens = append(gens, m)
		}
	}
	if len(gens) == 0 {
		return ErrNotFound
	}
	newest := gens[0]
	for _, m := range gens[1:] {
		if m.UpdatedAt.After(newest.UpdatedAt) || (m.UpdatedAt.Equal(newest.UpdatedAt) && m.ID > newest.ID) {
			newest = m
		}
	}
	gen := generationOf(newest.ID)
	for _, m := range gens {
		if generationOf(m.ID) == gen {
			if err := e.touchLocked(ctx, m.ID, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func generationOf(id string) string {
	s := strings.TrimSuffix(id, path.Ext(id))
	if i := strings.LastIndex(s, "~"); i > 0 && i < len(s)-1 && strings.Trim(s[i+1:], "0123456789abcdef") == "" {
		return s[i+1:]
	}
	return ""
}

// e.mu held
func (e *ExpiringAdapter) touchLocked(ctx context.Context, key string, now time.Time) error {
	ttl, ok := e.Policy.TTL(key, now)
	if !ok {
		return nil
	}
	rec, has, err := e.readRecordFull(ctx, key)
	if err != nil {
		return err
	}
	newCreated := false
	if !has {
		rec.created, newCreated = now, true
	} else if rec.created.IsZero() {
		rec.created, newCreated = now, true
	}
	want := e.capped(now.Add(ttl), rec.created)
	if has && !want.After(e.effective(rec)) {
		return nil
	}
	rec.deadline = now.Add(ttl)
	if newCreated {
		return e.writeRecord(ctx, key, rec)
	}
	return e.writeDeadline(ctx, key, rec.deadline)
}

// DeleteIf implements ConditionalDeleter for the expiring store.
func (e *ExpiringAdapter) DeleteIf(ctx context.Context, id string, prev string) error {
	c, ok := e.Inner.(ConditionalDeleter)
	if !ok || IsDocumentKey(id) {
		return ErrConditionalUnsupported
	}
	return c.DeleteIf(ctx, id, prev)
}

// Load implements Adapter; it does not renew.
func (e *ExpiringAdapter) Load(ctx context.Context, id string) (Document, error) {
	if isExpiryKey(id) {
		return Document{}, ErrNotFound
	}
	t, ok, err := e.readRecord(ctx, id)
	if err != nil {
		return Document{}, err
	}
	if ok && e.expired(t) {
		return Document{}, ErrNotFound
	}
	return e.Inner.Load(ctx, id)
}

// LoadHead implements HeadReader with the expiry rules.
func (e *ExpiringAdapter) LoadHead(ctx context.Context, id string, n int) ([]byte, error) {
	if isExpiryKey(id) {
		return nil, ErrNotFound
	}
	t, ok, err := e.readRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	if ok && e.expired(t) {
		return nil, ErrNotFound
	}
	head, _, err := LoadHead(ctx, e.Inner, id, n)
	if err == nil && len(head) > n && n > 0 {
		head = head[:n]
	}
	return head, err
}

func (e *ExpiringAdapter) visible(ctx context.Context, metas []Meta) ([]Meta, error) {
	deadlines, err := e.deadlines(ctx)
	if err != nil {
		return nil, err
	}
	out := metas[:0:0]
	for _, m := range metas {
		if isExpiryKey(m.ID) {
			continue
		}
		if t, ok := deadlines[m.ID]; ok && e.expired(t) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// List implements Adapter, leaving out expired documents.
func (e *ExpiringAdapter) List(ctx context.Context) ([]Meta, error) {
	metas, err := e.Inner.List(ctx)
	if err != nil {
		return nil, err
	}
	return e.visible(ctx, metas)
}

// ListPrefix implements PrefixLister, leaving out expired documents.
func (e *ExpiringAdapter) ListPrefix(ctx context.Context, prefix string) ([]Meta, error) {
	if isExpiryKey(prefix) {
		return nil, nil
	}
	metas, err := ListPrefix(ctx, e.Inner, prefix)
	if err != nil {
		return nil, err
	}
	return e.visible(ctx, metas)
}

// Delete implements Adapter.
func (e *ExpiringAdapter) Delete(ctx context.Context, id string) error {
	if isExpiryKey(id) {
		return ErrNotFound
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok, err := e.readRecord(ctx, id)
	if err != nil {
		return err
	}
	expired := ok && e.expired(t)
	size := 0
	if e.OnDelete != nil && strings.HasPrefix(id, versionsPrefix) {
		if doc, err := e.Inner.Load(ctx, id); err == nil {
			size = len(doc.Data)
		}
	}
	found, err := e.purgeLocked(ctx, id, false)
	if err != nil {
		return err
	}
	if expired || !found {
		return ErrNotFound
	}
	if e.OnDelete != nil && strings.HasPrefix(id, versionsPrefix) {
		e.OnDelete(id, size)
	}
	return nil
}

func (e *ExpiringAdapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := e.Inner.(Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}

func (e *ExpiringAdapter) Usage(ctx context.Context) (Quota, error) {
	if q, ok := e.Inner.(QuotaReporter); ok {
		return q.Usage(ctx)
	}
	return Quota{}, errors.New("docstore: the store reports no usage")
}

func WithConfiguredExpiry(ctx context.Context, adapter Adapter, cfg Config) (Adapter, *ExpiringAdapter) {
	if cfg.ExpiryTTL <= 0 {
		return adapter, nil
	}
	ea := NewExpiringAdapter(adapter, FixedTTL(cfg.ExpiryTTL))
	ea.AdoptUpdatedAt = cfg.ExpiryAdoptUpdated
	ea.MaxAge = cfg.ExpiryMaxAge
	if cfg.ExpiryOrphans {
		ea.Orphans = CollabOrphans(cfg.ExpiryOrphanGrace)
		log.Printf("docstore: shared-editing state of documents that do not exist is deleted %s after its last save", cfg.ExpiryTTL+orElse(cfg.ExpiryOrphanGrace, DefaultOrphanGrace))
	}
	if cfg.ExpiryMaxAge > 0 {
		log.Printf("docstore: documents expire at most %s after their creation, however often they are edited", cfg.ExpiryMaxAge)
	}
	go ea.Run(ctx, cfg.ExpirySweep)
	log.Printf("docstore: documents expire %s after their last write (sweep every %s)", cfg.ExpiryTTL, cfg.ExpirySweep)
	return ea, ea
}
