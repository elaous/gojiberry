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
	collabPrefix     = "collab:"      // shared-editing hub state (collab/hub.StoreKey)
	collabGonePrefix = "collab-gone:" // the hub's marks of documents closed as expired (collab/hub)
	catalogPrefix    = "catalog:"     // catalogs and folder records (saver)
	versionsPrefix   = "versions/"    // version history (saver.VersionPrefix)
	passkeyPrefix    = "passkey/"     // passkey accounts and handles (auth/passkey)
	keysPrefix       = "keys:"        // per-document wrapped keys (encrypt.KeysPrefix); losing one makes the document unreadable
	fontsPrefix      = "fonts/"       // the font library (fonts.KeyPrefix, saver.FontPrefix)
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

// The keyspaces of the shared sign-in sessions (auth/sessionstore). They hold the signed-in user of
// each session and the OpenID Connect login states, under "<prefix><bucket>/<hash of the token>".
// Reserved from the /docs API in every mode (a client that could write one could forge a session),
// no expiry deadline (the store sweeps its own), not documents. Not in encrypt.DefaultSkip: they
// carry names and e-mail addresses, so an encrypting adapter encrypts them.
const (
	SessionPrefix    = "auth/session/" // bearer sessions, "auth/session/<expiry hour>/<hash>"
	LoginStatePrefix = "auth/state/"   // OpenID Connect login states, "auth/state/<expiry minute>/<hash>"
)

// systemPrefixes are the keyspaces that are not documents: they never expire by themselves (the
// ones that belong to a document go with it, see DefaultRelated).
var systemPrefixes = []string{ExpiryPrefix, ExpiryCreatedPrefix, aclKeyPrefix, collabPrefix, collabGonePrefix, catalogPrefix, versionsPrefix, DirectoryPrefix, DirectoryLogPrefix, DirectoryEnrollPrefix, RateLimitPrefix, ConfigPrefix, DeviceUsePrefix, SessionPrefix, LoginStatePrefix, passkeyPrefix, keysPrefix, fontsPrefix, FolderSecretPrefix, PublicPrefix, ReportPrefix}

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
	// Guard, when set, is asked before every Save and SaveIf (nothing is renewed or written if it
	// refuses): a non-nil error refuses the write and is returned as it is. A non-nil done is called
	// when the write ended, with whether it was stored. It must be quick and must not call back
	// into the adapter. The public offer uses it to refuse a version snapshot that would pass its
	// byte budget, and to count the ones it lets through. Set it before the adapter is in use.
	Guard func(id string, size int) (done func(stored bool), err error)
	// OnDelete, when set, is called after a version snapshot ("versions/...") was deleted by Delete,
	// with its id and the size it had (0 if the store could not say). It must be quick and must not
	// call back into the adapter. The public offer uses it to subtract the snapshot at once.
	OnDelete func(id string, size int)
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

func isExpiryKey(id string) bool {
	return strings.HasPrefix(id, ExpiryPrefix) || strings.HasPrefix(id, ExpiryCreatedPrefix)
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

// errRenewed is purgeLocked's answer when another process renewed or changed the document while it
// was being expired: nothing else was deleted, and the document and its record are as that process
// left them.
var errRenewed = errors.New("docstore: the document was renewed while it was being expired")

// deleteExpired deletes the expired document id. On a store with conditional deletes it deletes what
// it read: the content is loaded, the deadline record is read again (a process that renewed the
// document before the load shows here), and the delete applies only to the digest loaded, so a
// save by another process after that is kept (errRenewed). The in-process lock cannot do that:
// it serializes one process, not two. A store without DeleteIf deletes unconditionally, as before.
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
	default:
		return false, err
	}
}

// purgeLocked deletes document id, what Related names and, last, its record. e.mu is held.
// It reports whether the document itself was there. The record goes last on purpose: a crash or
// failure anywhere before it leaves the record, so the next Sweep finishes the job. With expired
// set the document is known to be past its deadline and is deleted on the content that was read
// (deleteExpired): if another process changed it meanwhile, errRenewed, and nothing else is touched.
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
		switch _, err := e.purgeLocked(ctx, id, true); {
		case errors.Is(err, errRenewed):
			// another process renewed it while this one was clearing it: it is live now, so this
			// write extends it like any other
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

// Save implements Adapter: it stores the document and renews its deadline.
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

// guard asks Guard; the done it returns is never nil.
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

// SaveIf implements ConditionalAdapter over an inner one; an expired document counts as absent.
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

// LoadHead implements HeadReader with the rules of Load: a deadline record is not found, an
// expired document is not found (the deadline is read first), and only then the inner store is
// asked for the first n bytes (by its own LoadHead, or by Load and a cut).
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
