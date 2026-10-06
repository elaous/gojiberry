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

// ExpiryPrefix is the keyspace of the deadline records an ExpiringAdapter keeps beside the
// documents: "expiry:<id>" holds the instant (RFC 3339, UTC) after which document <id> is gone.
// Server reserves it from the document API.
const ExpiryPrefix = "expiry:"

// ExpiryPolicy decides how long a document lives after a write. TTL answers for id at time now:
// the lifetime, and whether the document expires at all (false: it lives until deleted).
// Alternative policies (per tenant, per size, by id prefix) plug in here.
type ExpiryPolicy interface {
	TTL(id string, now time.Time) (time.Duration, bool)
}

// ExpiryPolicyFunc makes a function an ExpiryPolicy.
type ExpiryPolicyFunc func(id string, now time.Time) (time.Duration, bool)

// TTL implements ExpiryPolicy.
func (f ExpiryPolicyFunc) TTL(id string, now time.Time) (time.Duration, bool) { return f(id, now) }

// The keyspaces other parts of the program keep in the same store as the documents. They are
// listed here, in one place, and nowhere else in this file: IsDocumentKey is the rule and
// TestSystemPrefixesCoverEveryPrefixConstant keeps it complete (every Prefix constant in the
// repository must be listed here or be declared irrelevant there).
const (
	collabPrefix   = "collab:"   // shared-editing hub state (collab/hub.StoreKey)
	catalogPrefix  = "catalog:"  // catalogs and folder records (saver)
	versionsPrefix = "versions/" // version history (saver.VersionPrefix)
	passkeyPrefix  = "passkey/"  // passkey accounts and handles (auth/passkey)
	keysPrefix     = "keys:"     // per-document wrapped keys (encrypt.KeysPrefix); losing one makes the document unreadable
	fontsPrefix    = "fonts/"    // the font library (fonts.KeyPrefix, saver.FontPrefix)
)

// The keyspaces of the public (anonymous) offer, see package docstore/public. Neither expires by
// itself: the records under PublicPrefix that belong to one document (its metadata and view-id
// record) are deleted by the public service's own sweep once the document is gone, and its
// tombstones (PublicPrefix+"tomb/") and the reports (ReportPrefix) have a TTL of their own that
// the same sweep enforces. Both are reserved from the /docs API.
const (
	PublicPrefix = "public/" // document metadata, view-id records and tombstones of the public offer
	ReportPrefix = "report/" // abuse reports about public documents
)

// systemPrefixes are the keyspaces that are not documents: they never expire by themselves (the
// ones that belong to a document go with it, see DefaultRelated).
var systemPrefixes = []string{ExpiryPrefix, aclKeyPrefix, collabPrefix, catalogPrefix, versionsPrefix, DirectoryPrefix, DirectoryLogPrefix, DirectoryEnrollPrefix, RateLimitPrefix, passkeyPrefix, keysPrefix, fontsPrefix, FolderSecretPrefix, PublicPrefix, ReportPrefix}

// IsDocumentKey reports whether key is a plain document: what the saver writes ("<id>~<gen><ext>"
// generation keys) or a client PUTs under a plain id. It is an allowlist of the document
// keyspace: a key in any system keyspace (access lists, hub state, catalogs, versions, the
// directory, passkeys, wrapped keys, fonts, the deadline records) is not one, and neither is any
// key whose first path segment contains a colon ("name:..." is the namespace style the system
// keyspaces use, so an unknown one is assumed to be a system key). ExpiryPolicy implementations
// that give some documents a lifetime should answer "no TTL" for every key this rejects; FixedTTL does.
func IsDocumentKey(key string) bool {
	if key == "" {
		return false
	}
	lower := strings.ToLower(key) // a backend that folds case would alias them
	for _, p := range systemPrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	first, _, _ := strings.Cut(key, "/")
	return !strings.Contains(first, ":")
}

// FixedTTL is the policy "every document lives d after its last write". Only plain documents
// (IsDocumentKey) get a lifetime; every other key lives until deleted.
func FixedTTL(d time.Duration) ExpiryPolicy {
	return ExpiryPolicyFunc(func(id string, _ time.Time) (time.Duration, bool) {
		if !IsDocumentKey(id) {
			return 0, false
		}
		return d, d > 0
	})
}

// Expirer is an optional capability of an Adapter: it knows when documents expire. Server uses it
// to answer X-Gojiberry-Expires and the "expires" field of listings. ExpiringAdapter has it.
type Expirer interface {
	// Expiry returns the instant document id expires, and false if it does not.
	Expiry(ctx context.Context, id string) (time.Time, bool, error)
	// Expiries returns the deadline of every document that has one.
	Expiries(ctx context.Context) (map[string]time.Time, error)
}

// logicalID is the document a key belongs to: the key without its codec extension and without the
// saver's "~<generation>" suffix ("a.docx", "a.pdf" and "a~1f2e3d4c5b6a.docx" are all "a"). An id
// with a slash is its own logical id, so a nested id never reaches beyond itself.
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

// relatedSets splits what belongs to document id: the "own" records are keyed by the exact id, the
// "group" ones by the logical id, which siblings (a.docx and a.pdf, or two generations of one
// document) share.
func relatedSets(id string) (own, ownPrefixes, group, groupPrefixes []string) {
	own = []string{aclKeyPrefix + id, collabPrefix + id, keysPrefix + id}
	logical := logicalID(id)
	if logical != id {
		ownPrefixes = []string{versionsPrefix + id + "/"}
		group = []string{aclKeyPrefix + logical, collabPrefix + logical, keysPrefix + logical}
	}
	return own, ownPrefixes, group, []string{versionsPrefix + logical + "/"}
}

// DefaultRelated lists what belongs to document id and goes with it: its access list, shared-editing
// state and wrapped keys (acl:, collab:, keys:, under the exact id and, for a saver generation key
// or an id with an extension, under the id without them) and its version history
// ("versions/<id>/", and "versions/<id without extension>/", the saver's layout).
//
// Prefixes name direct children only ("versions/a/1.open", not "versions/a/b/1.open": the
// history of the document "a/b"), and an id with a slash never reaches beyond "versions/<id>/".
// DefaultRelated assumes nobody else owns the shared names; ExpiringAdapter (with Related unset)
// also checks that no other live document with the same logical id exists before it deletes them.
func DefaultRelated(id string) (exact, prefixes []string) {
	own, ownP, group, groupP := relatedSets(id)
	return append(own, group...), append(ownP, groupP...)
}

// ExpiringAdapter gives any Adapter documents that expire: a document lives for the TTL its
// Policy names after its last Save ("delete 24 hours after the last edit"). Save and SaveIf renew;
// Load and List do not, so reading cannot keep a document alive. An expired document is invisible
// at once (Load and Delete answer ErrNotFound, List leaves it out) even before Sweep deletes it.
//
// It is storage-agnostic: the deadline of document <id> is a small record "expiry:<id>" in the
// same store, written through the same adapter, so it works over memory, local files, SQLite and
// every backend, and over a stored deadline surviving a restart. Optional capabilities of the
// inner adapter are passed through: ConditionalAdapter (SaveIf, else ErrConditionalUnsupported),
// Locker (else a no-op lock), PrefixLister and QuotaReporter.
//
// Order and crash safety. Save writes the deadline record first, then the document; Sweep
// deletes the document (and what Related names), then the record last. So a crash can leave only
// two things, both harmless and both repaired by Sweep:
//   - a record whose document is missing (crash inside Save, or after the sweep's delete): it is
//     an orphan, dropped by the sweep once its deadline has passed (not before, since another
//     process may be in the middle of its Save);
//   - never a document that should expire but has no record, except documents that were in the
//     store before it was wrapped, or ones the policy did not cover when written. A document
//     without a record never expires on its own. The first Sweep after construction adopts every
//     such document (that the policy covers). By default it gets a full grace period (deadline
//     now + TTL), so turning expiry on over a non-empty store deletes nothing for one TTL; with
//     AdoptUpdatedAt it gets UpdatedAt + TTL instead and what is already past that is deleted by
//     that sweep. The number adopted is logged.
//
// A Save that fails after the record was written puts the previous record back, so a failed or
// conflicting write does not extend a document's life. Writes and sweeps of one ExpiringAdapter
// are serialized per process, so a renewal cannot be lost to a concurrent sweep; two processes
// sharing one store can still race at the instant of expiry (a Save at the very second of the
// deadline may be deleted), which for a 24-hour life is immaterial.
//
// Clocks. A deadline is compared with the clock of whichever process looks at it; processes
// sharing one store must keep their clocks within seconds of each other (NTP). Skew shifts a
// document's life by that much and nothing else.
//
// Caching. Listings (List, ListPrefix, Expiries) read the deadlines from an in-memory copy that
// this process's own writes invalidate and that is at most DeadlineCacheTTL old (5 seconds by
// default). A write by another process shows in this one's listings, and its renewal of a document
// this process saw expire, after at most that long; Load, Expiry, Save, Delete and Sweep always
// read the record itself, so the bound only concerns listings.
type ExpiringAdapter struct {
	Inner  Adapter
	Policy ExpiryPolicy
	// Now is the clock (time.Now when nil); tests inject a fake one.
	Now func() time.Time
	// Related names what is deleted along with a document (DefaultRelated when nil): exact ids
	// and id prefixes.
	Related func(id string) (exact, prefixes []string)
	// OnError receives the errors of Run's sweeps (the log when nil).
	OnError func(error)
	// AdoptUpdatedAt makes the first sweep give a document that has no deadline record
	// UpdatedAt + TTL, so old ones are deleted at once. The default is now + TTL: a full grace
	// period, which is what you want when expiry is switched on over documents people still use.
	AdoptUpdatedAt bool
	// DeadlineCacheTTL is how long listings may reuse the deadlines they read (5 seconds when
	// zero; negative: never reuse).
	DeadlineCacheTTL time.Duration

	mu      sync.Mutex // serializes writes and the per-document part of a sweep
	adopted bool       // the first sweep has given unrecorded documents a deadline

	cacheMu  sync.Mutex
	cache    map[string]time.Time
	cacheAt  time.Time
	cacheGen uint64 // bumped by every write of a record, so a slow read cannot store stale data
}

// NewExpiringAdapter wraps inner with the policy.
func NewExpiringAdapter(inner Adapter, policy ExpiryPolicy) *ExpiringAdapter {
	return &ExpiringAdapter{Inner: inner, Policy: policy}
}

var (
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

func (e *ExpiringAdapter) related(id string) ([]string, []string) {
	if e.Related != nil {
		return e.Related(id)
	}
	return DefaultRelated(id)
}

func isExpiryKey(id string) bool { return strings.HasPrefix(id, ExpiryPrefix) }

// readRecord returns the stored deadline of id.
func (e *ExpiringAdapter) readRecord(ctx context.Context, id string) (time.Time, bool, error) {
	d, err := e.Inner.Load(ctx, ExpiryPrefix+id)
	if errors.Is(err, ErrNotFound) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := parseDeadline(d.Data)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("docstore: expiry record of %q: %w", id, err)
	}
	return t, true, nil
}

func parseDeadline(b []byte) (time.Time, error) { return time.Parse(time.RFC3339Nano, string(b)) }

func (e *ExpiringAdapter) writeRecord(ctx context.Context, id string, t time.Time) error {
	defer e.invalidate()
	return e.Inner.Save(ctx, ExpiryPrefix+id, []byte(t.UTC().Format(time.RFC3339Nano)))
}

func (e *ExpiringAdapter) deleteRecord(ctx context.Context, id string) error {
	defer e.invalidate()
	if err := e.Inner.Delete(ctx, ExpiryPrefix+id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// expired reports whether a record's deadline has passed.
func (e *ExpiringAdapter) expired(t time.Time) bool { return !e.now().Before(t) }

// Expiry implements Expirer.
func (e *ExpiringAdapter) Expiry(ctx context.Context, id string) (time.Time, bool, error) {
	if isExpiryKey(id) {
		return time.Time{}, false, nil
	}
	t, ok, err := e.readRecord(ctx, id)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return t, true, nil
}

// invalidate drops the cached deadlines (a record is about to change or has changed).
func (e *ExpiringAdapter) invalidate() {
	e.cacheMu.Lock()
	e.cache, e.cacheGen = nil, e.cacheGen+1
	e.cacheMu.Unlock()
}

// readDeadlines reads every deadline record: one listing and one Load per record.
func (e *ExpiringAdapter) readDeadlines(ctx context.Context) (map[string]time.Time, error) {
	recs, err := ListPrefix(ctx, e.Inner, ExpiryPrefix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(recs))
	for _, m := range recs {
		id := strings.TrimPrefix(m.ID, ExpiryPrefix)
		t, ok, err := e.readRecord(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = t
		}
	}
	return out, nil
}

// deadlines returns the deadlines for a listing: the cached copy while it is fresh. The map is
// shared and must not be modified.
func (e *ExpiringAdapter) deadlines(ctx context.Context) (map[string]time.Time, error) {
	ttl := e.DeadlineCacheTTL
	if ttl == 0 {
		ttl = 5 * time.Second
	}
	e.cacheMu.Lock()
	if ttl > 0 && e.cache != nil && e.now().Sub(e.cacheAt) < ttl && !e.now().Before(e.cacheAt) {
		m := e.cache
		e.cacheMu.Unlock()
		return m, nil
	}
	gen := e.cacheGen
	e.cacheMu.Unlock()
	m, err := e.readDeadlines(ctx)
	if err != nil {
		return nil, err
	}
	e.cacheMu.Lock()
	if e.cacheGen == gen && ttl > 0 {
		e.cache, e.cacheAt = m, e.now()
	}
	e.cacheMu.Unlock()
	return m, nil
}

// Expiries implements Expirer, from the cache described on ExpiringAdapter. Documents already
// past their deadline are included, since they are what the next Sweep deletes; List and Load
// already hide them.
func (e *ExpiringAdapter) Expiries(ctx context.Context) (map[string]time.Time, error) {
	m, err := e.deadlines(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

// liveSibling reports whether a document other than id with the same logical id is live (not
// past its deadline): it shares the version history and the per-document records with id.
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

// relatedFor is what goes with document id: Related when set, else the default rule, in which
// the records of the logical id are kept while another live document shares it.
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

// purgeLocked deletes document id, what Related names and, last, its record. e.mu is held.
// It reports whether the document itself was there. The record goes last on purpose: a crash or
// failure anywhere before it leaves the record, so the next Sweep finishes the job.
func (e *ExpiringAdapter) purgeLocked(ctx context.Context, id string) (bool, error) {
	found := true
	if err := e.Inner.Delete(ctx, id); errors.Is(err, ErrNotFound) {
		found = false
	} else if err != nil {
		return false, err
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
					continue // another document's history ("versions/a/b/..." is not that of "a")
				}
				if err := e.Inner.Delete(ctx, m.ID); err != nil && !errors.Is(err, ErrNotFound) {
					return found, err
				}
			}
		}
	}
	return found, e.deleteRecord(ctx, id)
}

// renewed runs write (the inner Save) with the new deadline recorded before it. An expired
// document that was never swept is purged first, so a new document does not inherit the old
// one's versions. If write fails the previous record is put back.
func (e *ExpiringAdapter) renewed(ctx context.Context, id string, write func() error) error {
	if isExpiryKey(id) {
		return fmt.Errorf("docstore: %q is in the reserved expiry keyspace", id)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	prev, had, err := e.readRecord(ctx, id)
	if err != nil {
		return err
	}
	if had && !now.Before(prev) {
		if _, err := e.purgeLocked(ctx, id); err != nil {
			return err
		}
		had = false
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
	if err := e.writeRecord(ctx, id, now.Add(ttl)); err != nil {
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

// Save implements Adapter: it stores the document and renews its deadline.
func (e *ExpiringAdapter) Save(ctx context.Context, id string, data []byte) error {
	return e.renewed(ctx, id, func() error { return e.Inner.Save(ctx, id, data) })
}

// SaveIf implements ConditionalAdapter over an inner one; an expired document counts as absent.
func (e *ExpiringAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	c, ok := e.Inner.(ConditionalAdapter)
	if !ok {
		return ErrConditionalUnsupported
	}
	return e.renewed(ctx, id, func() error { return c.SaveIf(ctx, id, data, prev) })
}

// DeleteIf implements ConditionalDeleter for the keys that are not documents (system records
// such as "ratelimit/", which have no deadline of their own to clean up) over an inner adapter
// that has it; a document key is ErrConditionalUnsupported, since deleting one also removes its
// deadline and related records.
func (e *ExpiringAdapter) DeleteIf(ctx context.Context, id string, prev string) error {
	c, ok := e.Inner.(ConditionalDeleter)
	if !ok || IsDocumentKey(id) {
		return ErrConditionalUnsupported
	}
	return c.DeleteIf(ctx, id, prev)
}

// Load implements Adapter. It does not renew; an expired document is ErrNotFound.
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

// visible drops the deadline records and the expired documents from metas.
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

// List implements Adapter, leaving out expired documents and the deadline records. It does not renew.
func (e *ExpiringAdapter) List(ctx context.Context) ([]Meta, error) {
	metas, err := e.Inner.List(ctx)
	if err != nil {
		return nil, err
	}
	return e.visible(ctx, metas)
}

// ListPrefix implements PrefixLister (through the inner one's when it has it).
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

// Delete implements Adapter. An expired document is already gone (ErrNotFound; it is purged).
// Deleting a live document removes what Sweep would remove with it (its access list,
// shared-editing state, wrapped keys and versions, by the same rule), so a new document with the
// same id does not inherit the old one's access list or history.
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
	found, err := e.purgeLocked(ctx, id)
	if err != nil {
		return err
	}
	if expired || !found {
		return ErrNotFound
	}
	return nil
}

// Lock implements Locker by the inner store's, if it has one (else it does nothing, which is
// what a store with no shared lock gets from an encrypting adapter too).
func (e *ExpiringAdapter) Lock(ctx context.Context) (func(), error) {
	if l, ok := e.Inner.(Locker); ok {
		return l.Lock(ctx)
	}
	return func() {}, nil
}

// Usage implements QuotaReporter by the inner store's; without one it is an error, which Server
// reports as "not available".
func (e *ExpiringAdapter) Usage(ctx context.Context) (Quota, error) {
	if q, ok := e.Inner.(QuotaReporter); ok {
		return q.Usage(ctx)
	}
	return Quota{}, errors.New("docstore: the store reports no usage")
}

// adopt gives every document without a record that the policy covers a deadline: now + TTL, or
// UpdatedAt + TTL with AdoptUpdatedAt. It runs once, in the first successful Sweep, and logs how
// many it adopted.
func (e *ExpiringAdapter) adopt(ctx context.Context) error {
	e.mu.Lock()
	done := e.adopted
	e.mu.Unlock()
	if done {
		return nil
	}
	metas, err := e.Inner.List(ctx)
	if err != nil {
		return err
	}
	deadlines, err := e.readDeadlines(ctx)
	if err != nil {
		return err
	}
	n := 0
	for _, m := range metas {
		if isExpiryKey(m.ID) {
			continue
		}
		if _, has := deadlines[m.ID]; has {
			continue
		}
		now := e.now()
		ttl, ok := e.Policy.TTL(m.ID, now)
		if !ok {
			continue
		}
		base := now
		if e.AdoptUpdatedAt && !m.UpdatedAt.IsZero() {
			base = m.UpdatedAt
		}
		e.mu.Lock()
		// A Save since the listing has written a record of its own: keep it.
		_, has, err := e.readRecord(ctx, m.ID)
		if err == nil && !has {
			err = e.writeRecord(ctx, m.ID, base.Add(ttl))
			if err == nil {
				n++
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.adopted = true
	e.mu.Unlock()
	if n > 0 {
		how := "a full grace period from now"
		if e.AdoptUpdatedAt {
			how = "last update + TTL"
		}
		log.Printf("docstore: expiry adopted %d existing documents without a deadline (%s)", n, how)
	}
	return nil
}

// Sweep deletes the documents whose deadline has passed, with what Related names for them, and
// then their records; it also drops the orphan records of documents that are gone. It returns
// how many documents it deleted. A failure on one document does not stop the others: Sweep
// returns the first error after trying them all, and what was left is retried by the next one.
func (e *ExpiringAdapter) Sweep(ctx context.Context) (deleted int, err error) {
	if err := e.adopt(ctx); err != nil {
		return 0, err
	}
	deadlines, err := e.readDeadlines(ctx)
	if err != nil {
		return 0, err
	}
	var first error
	for id, t := range deadlines {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		if !e.expired(t) {
			continue
		}
		e.mu.Lock()
		// Re-read under the lock: a Save may have renewed it since the listing.
		cur, ok, rerr := e.readRecord(ctx, id)
		if rerr == nil && ok && e.expired(cur) {
			var found bool
			found, rerr = e.purgeLocked(ctx, id)
			if rerr == nil && found {
				deleted++
			}
		}
		e.mu.Unlock()
		if rerr != nil && first == nil {
			first = rerr
		}
	}
	return deleted, first
}

// Run sweeps now and then every interval until ctx is done. Errors go to OnError (the log).
func (e *ExpiringAdapter) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := e.Sweep(ctx); err != nil && ctx.Err() == nil {
			if e.OnError != nil {
				e.OnError(err)
			} else {
				log.Printf("docstore: expiry sweep: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
