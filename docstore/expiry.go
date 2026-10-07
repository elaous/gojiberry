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
//
// The record is a single line, the deadline and nothing else, exactly the format every earlier
// build wrote and parses (it reads the whole body with time.Parse), so a rollback to an older
// build keeps working. The creation time (for MaxAge) is a sibling record,
// ExpiryCreatedPrefix+<id>, which older builds neither read nor write.
const ExpiryPrefix = "expiry:"

// ExpiryCreatedPrefix is the keyspace of the creation-time records ("expiry-created:<id>", RFC 3339,
// UTC) an ExpiringAdapter keeps for MaxAge. It is a separate key, not a second line of the
// deadline record, so a build that predates it still parses every deadline record. It is a system
// keyspace like ExpiryPrefix: reserved from the document API and never a document.
const ExpiryCreatedPrefix = "expiry-created:"

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
var systemPrefixes = []string{ExpiryPrefix, ExpiryCreatedPrefix, aclKeyPrefix, collabPrefix, catalogPrefix, versionsPrefix, DirectoryPrefix, DirectoryLogPrefix, DirectoryEnrollPrefix, RateLimitPrefix, passkeyPrefix, keysPrefix, fontsPrefix, FolderSecretPrefix, PublicPrefix, ReportPrefix}

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
	// MaxAge, when positive, is an absolute cap on a document's life, counted from its creation
	// (the first Save after it was absent, expired or swept): renewals (Save, SaveIf, Touch)
	// never move the deadline past creation + MaxAge, so a document that is edited forever still
	// expires. Zero (the default) means no cap. It bounds how long junk edits can keep a
	// document of a public offer alive. The creation time is kept in the deadline record
	// (a second line; records written before it existed read as "created when next renewed").
	// A Save of a document that has reached MaxAge starts a new document (what an expired one
	// would get): the old content, versions and access list are purged first.
	MaxAge time.Duration
	// OnSave, when set, is called after every successful Save and SaveIf with the id and the size
	// of what was written (not for Touch). It must be quick and must not call back into the
	// adapter. The public offer uses it to count version snapshots as they are written. Set it
	// before the adapter is in use.
	OnSave func(id string, size int)
	// Orphans, when set, makes Sweep also delete the shared-editing state ("collab:<id>") of a
	// document that does not exist in the store (see OrphanPolicy). Nil (the default) leaves
	// such entries alone.
	Orphans OrphanPolicy

	badMu sync.Mutex      // guards bad
	bad   map[string]bool // ids whose unreadable deadline record has been logged

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

// Renewer is what a collaborative editor calls to say "document id was edited": the deadline of
// a live document moves to now + TTL without the document itself being written. It is
// ErrNotFound for a document that does not exist or has expired (a Renewer never revives one).
// ExpiringAdapter implements it (Touch), and HTTPAdapter does over POST /docs/{id}/renew, so
// a collaboration hub in another process renews through the docstore server.
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

func (e *ExpiringAdapter) related(id string) ([]string, []string) {
	if e.Related != nil {
		return e.Related(id)
	}
	return DefaultRelated(id)
}

func isExpiryKey(id string) bool {
	return strings.HasPrefix(id, ExpiryPrefix) || strings.HasPrefix(id, ExpiryCreatedPrefix)
}

// expiryRecord is what is known about a document's life: the deadline ("expiry:<id>", one line)
// and when the document was created ("expiry-created:<id>"; zero when unknown: a record written
// by an older build, or by hand).
type expiryRecord struct {
	deadline time.Time
	created  time.Time
}

// errBadRecord marks a deadline record that cannot be parsed.
var errBadRecord = errors.New("docstore: unreadable expiry record")

// deadlineBytes is the body of "expiry:<id>": the deadline alone, the only format older builds read.
func (r expiryRecord) deadlineBytes() []byte {
	return []byte(r.deadline.UTC().Format(time.RFC3339Nano))
}

func (r expiryRecord) createdBytes() []byte {
	return []byte(r.created.UTC().Format(time.RFC3339Nano))
}

// parseRecord reads a deadline record. The first line is the deadline. A second line (written by
// a pre-release build that kept the creation time there) is read as the creation time; it is
// never written any more.
func parseRecord(b []byte) (expiryRecord, error) {
	first, rest, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	var r expiryRecord
	var err error
	if r.deadline, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(first)); err != nil {
		return expiryRecord{}, fmt.Errorf("%w: %v", errBadRecord, err)
	}
	if rest = strings.TrimSpace(rest); rest != "" {
		if r.created, err = time.Parse(time.RFC3339Nano, rest); err != nil {
			r.created = time.Time{} // the creation time is advisory: ignore a damaged one
		}
	}
	return r, nil
}

// capped is deadline limited by MaxAge counted from created (no cap when either is unset).
func (e *ExpiringAdapter) capped(deadline, created time.Time) time.Time {
	if e.MaxAge > 0 && !created.IsZero() {
		if c := created.Add(e.MaxAge); c.Before(deadline) {
			return c
		}
	}
	return deadline
}

// effective is the instant a record's document expires: its deadline, or the MaxAge cap if that
// is sooner.
func (e *ExpiringAdapter) effective(r expiryRecord) time.Time { return e.capped(r.deadline, r.created) }

// readRecordFull returns the stored record of id. A deadline record that cannot be parsed is
// logged (once per id) and counts as absent: one damaged record must never fail the Load or Save
// of its document, nor abort a sweep; the next write of the document replaces it, and the first
// sweep adopts a document that has none.
func (e *ExpiringAdapter) readRecordFull(ctx context.Context, id string) (expiryRecord, bool, error) {
	d, err := e.Inner.Load(ctx, ExpiryPrefix+id)
	if errors.Is(err, ErrNotFound) {
		return expiryRecord{}, false, nil
	}
	if err != nil {
		return expiryRecord{}, false, err
	}
	r, err := parseRecord(d.Data)
	if err != nil {
		e.noteBadRecord(id, err)
		return expiryRecord{}, false, nil
	}
	if c, cerr := e.Inner.Load(ctx, ExpiryCreatedPrefix+id); cerr == nil {
		if t, perr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(c.Data))); perr == nil {
			r.created = t
		}
	} else if !errors.Is(cerr, ErrNotFound) {
		return expiryRecord{}, false, cerr
	}
	return r, true, nil
}

// noteBadRecord logs an unreadable deadline record once per id.
func (e *ExpiringAdapter) noteBadRecord(id string, err error) {
	e.badMu.Lock()
	if e.bad == nil {
		e.bad = map[string]bool{}
	}
	seen := e.bad[id]
	if !seen && len(e.bad) < 1024 {
		e.bad[id] = true
	}
	e.badMu.Unlock()
	if !seen {
		log.Printf("docstore: skipping the unreadable expiry record of %q (%v); it is replaced by the next write of the document", id, err)
	}
}

// readRecord returns the instant document id expires (its deadline, with MaxAge applied).
func (e *ExpiringAdapter) readRecord(ctx context.Context, id string) (time.Time, bool, error) {
	r, ok, err := e.readRecordFull(ctx, id)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return e.effective(r), true, nil
}

// writeRecord stores r: the creation time first (only when it is known), then the deadline, so a
// crash between the two leaves a deadline record with its old or no creation time, never the
// reverse. A record without a creation time removes a stale sibling.
func (e *ExpiringAdapter) writeRecord(ctx context.Context, id string, r expiryRecord) error {
	defer e.invalidate()
	if r.created.IsZero() {
		if err := e.Inner.Delete(ctx, ExpiryCreatedPrefix+id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	} else if err := e.Inner.Save(ctx, ExpiryCreatedPrefix+id, r.createdBytes()); err != nil {
		return err
	}
	return e.Inner.Save(ctx, ExpiryPrefix+id, r.deadlineBytes())
}

// writeDeadline rewrites the deadline record only (the creation time is unchanged).
func (e *ExpiringAdapter) writeDeadline(ctx context.Context, id string, deadline time.Time) error {
	defer e.invalidate()
	return e.Inner.Save(ctx, ExpiryPrefix+id, expiryRecord{deadline: deadline}.deadlineBytes())
}

// deleteRecord removes the creation time first and the deadline record last: a crash in between
// leaves the deadline record, so the next sweep finishes, and a creation time can never outlive
// its deadline record to be inherited by a new document (a new record always writes its own).
func (e *ExpiringAdapter) deleteRecord(ctx context.Context, id string) error {
	defer e.invalidate()
	if err := e.Inner.Delete(ctx, ExpiryCreatedPrefix+id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
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
	out, _, err := e.readDeadlinesBad(ctx)
	return out, err
}

// readDeadlinesBad is readDeadlines that also returns the ids whose record is unreadable: they
// are skipped (logged once), never an error, so one damaged record cannot abort a sweep.
func (e *ExpiringAdapter) readDeadlinesBad(ctx context.Context) (out map[string]time.Time, bad []string, err error) {
	recs, err := ListPrefix(ctx, e.Inner, ExpiryPrefix)
	if err != nil {
		return nil, nil, err
	}
	out = make(map[string]time.Time, len(recs))
	for _, m := range recs {
		id := strings.TrimPrefix(m.ID, ExpiryPrefix)
		t, ok, err := e.readRecord(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			out[id] = t
		} else {
			bad = append(bad, id) // listed but unreadable (or deleted since the listing)
		}
	}
	return out, bad, nil
}

// healBad repairs the records readDeadlinesBad could not read, under the lock and after
// re-reading: a record whose document exists gets a fresh full lifetime (the same grace adoption
// gives), one with no document is deleted. Without it a document with a damaged record would
// never expire.
func (e *ExpiringAdapter) healBad(ctx context.Context, bad []string) error {
	var first error
	for _, id := range bad {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		err := func() error {
			if _, ok, err := e.readRecordFull(ctx, id); err != nil || ok {
				return err // fine now (written since), or the store failed
			}
			if _, err := e.Inner.Load(ctx, ExpiryPrefix+id); errors.Is(err, ErrNotFound) {
				return nil // deleted since the listing
			} else if err != nil {
				return err
			}
			if _, err := e.Inner.Load(ctx, id); errors.Is(err, ErrNotFound) {
				return e.deleteRecord(ctx, id) // a record with no document
			} else if err != nil {
				return err
			}
			now := e.now()
			ttl, ok := e.Policy.TTL(id, now)
			if !ok {
				return e.deleteRecord(ctx, id)
			}
			log.Printf("docstore: gave %q a new deadline in place of its unreadable expiry record", id)
			return e.writeRecord(ctx, id, expiryRecord{deadline: now.Add(ttl), created: now})
		}()
		e.mu.Unlock()
		if err != nil && first == nil {
			first = err
		}
	}
	return first
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
	prev, had, err := e.readRecordFull(ctx, id)
	if err != nil {
		return err
	}
	if had && !now.Before(e.effective(prev)) {
		if _, err := e.purgeLocked(ctx, id); err != nil {
			return err
		}
		had, prev = false, expiryRecord{}
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

// Save implements Adapter: it stores the document and renews its deadline.
func (e *ExpiringAdapter) Save(ctx context.Context, id string, data []byte) error {
	if err := e.renewed(ctx, id, func() error { return e.Inner.Save(ctx, id, data) }); err != nil {
		return err
	}
	e.saved(id, len(data))
	return nil
}

func (e *ExpiringAdapter) saved(id string, size int) {
	if f := e.OnSave; f != nil {
		f(id, size)
	}
}

// SaveIf implements ConditionalAdapter over an inner one; an expired document counts as absent.
func (e *ExpiringAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	c, ok := e.Inner.(ConditionalAdapter)
	if !ok {
		return ErrConditionalUnsupported
	}
	if err := e.renewed(ctx, id, func() error { return c.SaveIf(ctx, id, data, prev) }); err != nil {
		return err
	}
	e.saved(id, len(data))
	return nil
}

// Renew implements Renewer; it is Touch.
func (e *ExpiringAdapter) Renew(ctx context.Context, id string) error { return e.Touch(ctx, id) }

// Touch renews document id as a Save would, without writing the document: the deadline record
// becomes now + TTL (never past the MaxAge cap, never earlier than it was) and nothing else
// changes. It is for edits that are kept elsewhere (the collaboration hub keeps its state under
// "collab:<id>", which is not a document and never expires by itself).
//
// It is ErrNotFound when no such document exists or its deadline has passed: a dead document is
// never revived (nor purged here; Sweep does that), and a record with no document (the crash
// leftover described on ExpiringAdapter) does not count. id names a document key. Exact name
// first: if a live document has exactly that key, only it is renewed. Only when there is none
// are the saver's "<id>~<generation>.<ext>" keys of that logical id considered, and then only
// the newest live generation (by UpdatedAt, with every key of that same generation, such as the
// same save in two codecs) is renewed: the saver deletes the generations it replaces, and one
// that its failed cleanup left behind must not be kept alive for ever by renewals meant for the
// current one; it expires on its own deadline. A document the policy gives no lifetime is live
// and unchanged (nil).
//
// Order: the only write is the record, so there is nothing to undo; a crash or failure leaves
// either the old deadline or the new one, and the document is never touched. Like Save, it is
// serialized with Sweep and other writes of this ExpiringAdapter.
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

// generationOf is the saver's "~<generation>" token of a key ("" when it has none).
func generationOf(id string) string {
	s := strings.TrimSuffix(id, path.Ext(id))
	if i := strings.LastIndex(s, "~"); i > 0 && i < len(s)-1 && strings.Trim(s[i+1:], "0123456789abcdef") == "" {
		return s[i+1:]
	}
	return ""
}

// touchLocked moves the deadline record of the live document key to now + TTL (e.mu held).
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
		rec.created, newCreated = now, true // a record from before creation times were kept: the cap starts now
	}
	want := e.capped(now.Add(ttl), rec.created)
	if has && !want.After(e.effective(rec)) {
		return nil // never shorten a life by renewing it (the cap can only be that already)
	}
	rec.deadline = now.Add(ttl)
	if newCreated {
		return e.writeRecord(ctx, key, rec)
	}
	return e.writeDeadline(ctx, key, rec.deadline)
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
			err = e.writeRecord(ctx, m.ID, expiryRecord{deadline: base.Add(ttl), created: base})
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
// then their records; it also drops the orphan records of documents that are gone and, with
// Orphans set, the orphaned shared-editing state (counted in the result). It returns
// how many documents it deleted. A failure on one document does not stop the others: Sweep
// returns the first error after trying them all, and what was left is retried by the next one.
func (e *ExpiringAdapter) Sweep(ctx context.Context) (deleted int, err error) {
	if err := e.adopt(ctx); err != nil {
		return 0, err
	}
	deadlines, bad, err := e.readDeadlinesBad(ctx)
	if err != nil {
		return 0, err
	}
	var first error
	if herr := e.healBad(ctx, bad); herr != nil {
		first = herr
	}
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
	if cerr := e.sweepCreated(ctx); cerr != nil && first == nil {
		first = cerr
	}
	if e.Orphans != nil {
		n, oerr := e.sweepOrphans(ctx)
		deleted += n
		if oerr != nil && first == nil {
			first = oerr
		}
	}
	return deleted, first
}

// sweepCreated deletes the creation-time records that have no deadline record (a crash between
// the two writes of writeRecord). The check is repeated under the lock, which a Save holds
// across both writes.
func (e *ExpiringAdapter) sweepCreated(ctx context.Context) error {
	metas, err := ListPrefix(ctx, e.Inner, ExpiryCreatedPrefix)
	if err != nil {
		return err
	}
	var first error
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := strings.TrimPrefix(m.ID, ExpiryCreatedPrefix)
		if id == "" {
			continue
		}
		e.mu.Lock()
		_, derr := e.Inner.Load(ctx, ExpiryPrefix+id)
		if errors.Is(derr, ErrNotFound) {
			derr = e.Inner.Delete(ctx, m.ID)
			if errors.Is(derr, ErrNotFound) {
				derr = nil
			}
		}
		e.mu.Unlock()
		if derr != nil && first == nil {
			first = derr
		}
	}
	return first
}

// OrphanPolicy decides which entries of a system keyspace that belong to a document have outlived
// it. A collaboration hub that only ever holds a document (never saved to the store as a
// document) has nothing to renew and no deadline, so its "collab:<id>" state would stay for
// ever; Sweep asks the policy about each such entry whose document does not exist.
type OrphanPolicy interface {
	// Prefix is the keyspace swept ("collab:").
	Prefix() string
	// Expired reports whether an entry last written at updated, whose document would live ttl
	// after a write, is to be deleted at now.
	Expired(updated, now time.Time, ttl time.Duration) bool
}

// DefaultOrphanGrace is the grace period CollabOrphans uses when given none.
const DefaultOrphanGrace = time.Hour

type collabOrphans struct{ grace time.Duration }

// CollabOrphans is the OrphanPolicy for the hub's "collab:<id>" state: it is deleted once its
// own UpdatedAt is older than the document TTL plus grace (DefaultOrphanGrace when not positive)
// and no live document has that id (no exact key and no live saver generation). Every hub save
// bumps UpdatedAt, so a hub-only document that is being edited stays; one that nobody has
// touched for TTL + grace is removed. The grace period keeps a document that was just saved to
// the store, then expired and re-created, and clock skew from deleting live state.
func CollabOrphans(grace time.Duration) OrphanPolicy {
	if grace <= 0 {
		grace = DefaultOrphanGrace
	}
	return collabOrphans{grace}
}

func (collabOrphans) Prefix() string { return collabPrefix }
func (c collabOrphans) Expired(updated, now time.Time, ttl time.Duration) bool {
	return !updated.IsZero() && now.Sub(updated) > ttl+c.grace
}

// hasLiveDocument reports whether a live (not expired) document with exactly this key, or a
// live saver generation of this logical id, exists.
func (e *ExpiringAdapter) hasLiveDocument(ctx context.Context, id string) (bool, error) {
	metas, err := ListPrefix(ctx, e.Inner, id)
	if err != nil {
		return false, err
	}
	for _, m := range metas {
		if isExpiryKey(m.ID) || !(m.ID == id || (IsDocumentKey(m.ID) && logicalID(m.ID) == id)) {
			continue
		}
		t, ok, err := e.readRecord(ctx, m.ID)
		if err != nil {
			return false, err
		}
		if !ok || e.now().Before(t) {
			return true, nil
		}
	}
	return false, nil
}

// sweepOrphans deletes the entries Orphans names whose document is gone (see OrphanPolicy).
func (e *ExpiringAdapter) sweepOrphans(ctx context.Context) (deleted int, first error) {
	prefix := e.Orphans.Prefix()
	metas, err := ListPrefix(ctx, e.Inner, prefix)
	if err != nil {
		return 0, err
	}
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			return deleted, err
		}
		id := strings.TrimPrefix(m.ID, prefix)
		if id == "" {
			continue
		}
		now := e.now()
		ttl, ok := e.Policy.TTL(id, now)
		if !ok || !e.Orphans.Expired(m.UpdatedAt, now, ttl) {
			continue
		}
		ok, err := e.dropOrphan(ctx, prefix, id, ttl)
		if err != nil && first == nil {
			first = err
		}
		if ok {
			deleted++
		}
	}
	return deleted, first
}

// dropOrphan re-checks under the lock (a hub save or a new document since the listing keeps the
// entry) and deletes prefix+id.
func (e *ExpiringAdapter) dropOrphan(ctx context.Context, prefix, id string, ttl time.Duration) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, err := e.Inner.Load(ctx, prefix+id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !e.Orphans.Expired(cur.UpdatedAt, e.now(), ttl) {
		return false, nil
	}
	if live, err := e.hasLiveDocument(ctx, id); err != nil || live {
		return false, err
	}
	if err := e.Inner.Delete(ctx, prefix+id); err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	return true, nil
}

// WithConfiguredExpiry wraps adapter in an ExpiringAdapter when cfg asks for expiry
// (GOJIBERRY_DOCSTORE_EXPIRY_TTL), starts its sweeper (until ctx is done) and logs it; it returns
// the adapter to use and the ExpiringAdapter, which is nil, with adapter unchanged, when expiry
// is off. The docstore server and the collaboration hub program share it, so a hub in the same
// process can pass the ExpiringAdapter as its Renewer.
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

func orElse(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
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
