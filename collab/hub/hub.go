// Package hub is the server half of multi-user editing: a relay that holds
// each shared document as a crdt replica, sends every change to everyone
// else watching it, saves it through a docstore.Adapter (Postgres, SQLite,
// S3, files...), and checks what each person is allowed to change.
//
// The protocol is plain HTTP, so it needs nothing outside the standard
// library and works from a WASM client's fetch:
//
//	GET  /docs/{id}/events?replica=R   server-sent events: "hello" (role),
//	                                   "snapshot" (all ops), then "ops" and
//	                                   "presence" from others
//	POST /docs/{id}/ops?replica=R      a JSON array of crdt.Op; 204, or 403
//	                                   {"error": ...} if the role forbids them
//	POST /docs/{id}/presence           this replica's cursor (Presence)
//	GET  /docs/{id}/snapshot           all ops, for clients that cannot stream
//	GET  /docs/{id}/role               {"role": ...} for the caller
//
// See client for the Go client and collab.Session for tying it to a flow.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/guard"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Role, Presence and the role names are shared with clients (see wire).
type (
	Role     = wire.Role
	Presence = wire.Presence
)

const (
	Owner  = wire.Owner
	Editor = wire.Editor
	Viewer = wire.Viewer
)

// RoleFunc says what the caller of a request may do with document id.
type RoleFunc func(r *http.Request, id string) Role

// Config configures a Hub.
type Config struct {
	// Store keeps the documents between runs; nil keeps them in memory
	// only.
	Store docstore.Adapter
	// Roles decides each request's role; nil makes everyone an owner (for a
	// trusted network or a test).
	Roles RoleFunc
	// User names the caller for presence ("ana@example.com"); nil gives "".
	User func(r *http.Request) string
	// SaveDelay is how long after a change the document is saved (changes
	// in the meantime share one save); 0 means two seconds.
	SaveDelay time.Duration
	// KeepAlive is how often an idle event stream is pinged; 0 means 15s.
	KeepAlive time.Duration
	// PresenceInterval is the least time between two updates of one
	// replica's cursor sent to the others: updates that come faster are
	// merged and the latest goes out when the interval ends (so a cursor
	// dragged across the text costs a few messages a second, not hundreds).
	// 0 means 100ms; negative sends every update at once.
	PresenceInterval time.Duration
	// PresenceTTL is how long a cursor stays after its last update; 0
	// means 10 seconds.
	PresenceTTL time.Duration
	// Guard decides who may call the hub: guard.Origins(...) for pages from
	// listed origins, guard.Token(...) for callers holding a token, both with
	// guard.Chain, or your own. Nil means guard.Open() (any origin, no check;
	// the roles still apply).
	Guard guard.Guard
	// MaxBody limits a request body in bytes; 0 means 8 MiB.
	MaxBody int64
	// CookieOrigins lists the origins (exactly as the browser sends them,
	// "https://app.example") whose pages may open a WebSocket that carries
	// cookies. A socket from another origin that carries cookies is refused:
	// with cookie-based sign-in it would be a way for any site to act as the
	// signed-in user. Pages sending a bearer token carry no cookies for this
	// host and are not affected.
	CookieOrigins []string
	// TrustForwardedHost makes the same-origin test for such a socket compare the
	// Origin with X-Forwarded-Host as well as Host, for a hub behind a reverse proxy
	// that rewrites Host. Set it only when the proxy sets that header itself (a
	// client could otherwise send it).
	TrustForwardedHost bool
	// MaxRooms limits how many documents the hub keeps in memory; 0 means
	// 1000. Further documents are refused until the hub restarts.
	MaxRooms int
	// MaxPeers limits the cursors kept per document; 0 means 256.
	MaxPeers int
	// Renewer is told that a document was edited, so a store that expires documents
	// (docstore.ExpiringAdapter, or a docstore server through docstore.HTTPAdapter) counts
	// collaborators' edits as edits: the hub keeps its state under "collab:<id>", which
	// never expires by itself and never touches the document. Nil does nothing.
	//
	// Renew is called for the document id after an edit by an owner or editor was accepted
	// (comments by viewers, joining, watching and cursors never renew), at most once per
	// RenewInterval per document, in the background, so a slow store never delays an
	// acknowledgement. An error (a document that does not exist or has expired, a store that is
	// down) is logged and the next attempt waits for the interval again. Over HTTP the call
	// carries the Authorization header of the edit that caused it (docstore.WithAuthorization;
	// AuthFunc replaces it), so the docstore server checks the same write permission.
	//
	// Expired under an open room: once a renewal of a document has succeeded, a "not found" that
	// is repeated after GoneConfirmDelay means the document expired and was swept while a stale tab
	// kept its room (over HTTP, "not found" means only the docstore server's marked 404:
	// docstore.HTTPAdapter.Renew, a proxy's or an older server's 404 is ErrRenewUnsupported and
	// closes nothing). The room is then closed: further operations get 410 Gone (and so does any request for the id for an hour), it
	// stops saving, "collab:<id>" is deleted and the room leaves the hub. The first edit that
	// discovers it is still acknowledged, since renewals are asynchronous. A document that was
	// never saved to the store (it lives only in the hub) is always "not found", is never closed
	// this way and is not kept alive by this call; the store's sweeper removes its "collab:<id>"
	// state only with the orphan policy (docstore.ExpiringAdapter.Orphans).
	//
	// The hub's roles and the store's access rule must agree. A hub that lets everyone edit a
	// document without an access list (GOJIBERRY_COLLAB_NO_ACL_ROLE=owner or editor) over a
	// docstore that requires one (GOJIBERRY_DOCSTORE_REQUIRE_ACL=true) has every renewal refused
	// with 403, and the document expires under active editing. The first refusal per document is
	// logged as a warning and Health.RenewFailures (GET /healthz, behind the Guard) counts them.
	Renewer Renewer
	// RenewInterval is the least time between two renewals of one document; 0 means one
	// minute, negative renews on every accepted edit. With ExpiryTTL set it is clamped to a
	// quarter of the TTL (an interval at or above the TTL would let a document expire under
	// active editing), and an interval configured larger than the TTL is logged as a warning.
	RenewInterval time.Duration
	// ExpiryTTL is the lifetime the store gives a document, when the hub knows it (a hub in
	// the same process as the ExpiringAdapter does; cmd/collabhub passes it). Zero means unknown.
	ExpiryTTL time.Duration
	// AuthFunc returns the Authorization header value a renewal is sent with, called once per
	// renewal with the request of the edit that caused it. Nil forwards that request's own
	// Authorization header. A WebSocket carries the header of its upgrade request for as long as
	// it lives, and a token in it expires: AuthFunc is where a deployment looks up the
	// holder's current token (by session, user or cookie) so renewals outlive it. An empty result
	// sends none.
	AuthFunc func(r *http.Request) string
	// GoneConfirmDelay is how long the hub waits, after a renewal says "document gone" for a room
	// that had one success, before it asks once more: the room is closed (410 to its clients,
	// "collab:<id>" deleted, the id refused for an hour) only if the second answer is "gone"
	// too. A transient answer (a deploy, a failover, a document recreated) never kills a live
	// room. 0 means 2 seconds; negative asks again at once.
	GoneConfirmDelay time.Duration
	// Now is the clock (time.Now when nil); tests inject a fake one.
	Now func() time.Time
}

// DefaultGoneConfirmDelay is Config.GoneConfirmDelay's default.
const DefaultGoneConfirmDelay = 2 * time.Second

// Renewer is docstore.Renewer: what the hub calls to renew a document's expiry.
type Renewer = docstore.Renewer

// Hub serves documents. Create one with New and mount Handler.
type Hub struct {
	cfg   Config
	mu    sync.Mutex
	rooms map[string]*room
	now   func() time.Time

	closing atomic.Bool // set by Stop: no more writes or new streams

	renewMu   sync.Mutex // guards renewing; renewIdle is signalled when it reaches zero
	renewing  int        // renewals in flight
	renewIdle *sync.Cond

	renewFailures atomic.Int64 // renewals that failed for a reason other than "no such document"
	renewDenied   atomic.Int64 // of those, answered 401 or 403
	deadRooms     atomic.Int64 // rooms closed because their document expired
	hubOnlyRooms  atomic.Int64 // rooms whose document is not in the store (counted once per room)
	revivedRooms  atomic.Int64 // closed rooms reopened because the document is live again

	stop     chan struct{} // closed by Stop
	stopOnce sync.Once

	gone map[string]time.Time // ids whose room was closed as expired -> when that stops being remembered (h.mu)
}

// New creates a hub.
func New(cfg Config) *Hub {
	if cfg.SaveDelay == 0 {
		cfg.SaveDelay = 2 * time.Second
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = 15 * time.Second
	}
	if cfg.PresenceInterval == 0 {
		cfg.PresenceInterval = 100 * time.Millisecond
	}
	if cfg.PresenceTTL == 0 {
		cfg.PresenceTTL = 10 * time.Second
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 8 << 20
	}
	if cfg.MaxRooms == 0 {
		cfg.MaxRooms = 1000
	}
	if cfg.MaxPeers == 0 {
		cfg.MaxPeers = 256
	}
	if cfg.RenewInterval == 0 {
		cfg.RenewInterval = time.Minute
	}
	if cfg.ExpiryTTL > 0 && cfg.RenewInterval > 0 {
		if cfg.RenewInterval > cfg.ExpiryTTL {
			log.Printf("collab hub: WARNING the renew interval %s is longer than the document lifetime %s: a document would expire while it is being edited", cfg.RenewInterval, cfg.ExpiryTTL)
		}
		if max := cfg.ExpiryTTL / 4; cfg.RenewInterval > max && max > 0 {
			log.Printf("collab hub: renew interval lowered from %s to %s (a quarter of the document lifetime %s)", cfg.RenewInterval, max, cfg.ExpiryTTL)
			cfg.RenewInterval = max
		}
	}
	h := &Hub{cfg: cfg, rooms: map[string]*room{}, now: time.Now, gone: map[string]time.Time{}, stop: make(chan struct{})}
	if cfg.Now != nil {
		h.now = cfg.Now
	}
	h.renewIdle = sync.NewCond(&h.renewMu)
	return h
}

// StoreKey is the docstore key a document is saved under.
func StoreKey(id string) string { return "collab:" + id }

type event struct {
	name string
	data []byte
}

type sub struct {
	replica string
	role    Role
	ch      chan event
}

type room struct {
	id     string
	mu     sync.Mutex
	doc    *crdt.Doc
	subs   map[*sub]bool
	pres   map[string]presenceEntry
	owners map[string]string // replica id -> the signed-in user it first spoke for
	dirty  bool
	timer  *time.Timer

	lastRenew   time.Time // when a renewal of the document's expiry was last started
	renewed     bool      // lastRenew is set
	renewedOnce bool      // a renewal of this document has succeeded: the store has it
	dead        bool      // the document expired and was deleted: no more ops, no more saves
	authWarned  bool      // the renewal was refused (401/403) and that has been logged
	notFoundLog bool      // "not in the store" has been logged and counted for this room
	failLogged  bool      // the first failure of another kind has been logged for this room

	saveMu  sync.Mutex    // one save at a time, in order
	loaded  chan struct{} // closed when the stored document has been read
	loadErr error
}

type presenceEntry struct {
	p    Presence
	seen time.Time

	// lastSent is when this cursor was last passed to the others, and timer
	// (when set) is a send of the latest one scheduled for the end of the
	// throttling interval.
	lastSent time.Time
	timer    *time.Timer
}

// dropPresence forgets a replica's cursor and any send still scheduled for it
// (rm.mu held).
func (rm *room) dropPresence(replica string) {
	if e, ok := rm.pres[replica]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(rm.pres, replica)
	}
}

var errTooManyRooms = errors.New("hub: too many documents")

// errGone is a document whose room was closed because the store's copy expired.
var errGone = errors.New("hub: the document has expired")

// goneMsg is what clients are told about a document that expired under their open room.
const goneMsg = "this document has expired and been deleted; copy your content elsewhere"

// room finds or loads a document. The store is read outside the hub's lock,
// so a slow load of one document does not hold up the others.
//
// A document whose room was closed as expired is refused (errGone) for goneMemory, unless the
// store says it is live again (a deleted and re-created document, a late renewal): an editor's
// request asks the Renewer, and a success clears the tombstone (see revived).
func (h *Hub) room(r *http.Request, id string, role Role) (*room, error) {
	rm, err := h.openRoom(r, id, role)
	// The store remembers a closed document across restarts and replicas (see goneKey): an editor
	// of a document that is live again clears that mark, once, and tries again.
	if errors.Is(err, errGone) && role.CanEdit() && h.storeTombstone(r.Context(), id) && h.revived(r, id) {
		h.clearStoreTombstone(id)
		return h.openRoom(r, id, role)
	}
	return rm, err
}

func (h *Hub) openRoom(r *http.Request, id string, role Role) (*room, error) {
	ctx := r.Context()
	h.mu.Lock()
	rm, ok := h.rooms[id]
	if !ok {
		if until, was := h.gone[id]; was {
			if h.now().Before(until) {
				h.mu.Unlock()
				if !role.CanEdit() || !h.revived(r, id) {
					return nil, errGone
				}
				h.clearStoreTombstone(id)
				h.mu.Lock()
				rm, ok = h.rooms[id]
			}
			delete(h.gone, id)
		}
	}
	if !ok {
		if len(h.rooms) >= h.cfg.MaxRooms {
			h.mu.Unlock()
			return nil, errTooManyRooms
		}
		rm = &room{id: id, doc: crdt.NewDocWithStrategy("hub", crdt.MergeCharCRDT), subs: map[*sub]bool{}, pres: map[string]presenceEntry{}, loaded: make(chan struct{})}
		h.rooms[id] = rm
	}
	h.mu.Unlock()
	if !ok {
		// Not the first requester's context: its client leaving must not fail
		// the load for everyone waiting on it.
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		rm.loadErr = h.load(lctx, rm)
		cancel()
		if rm.loadErr != nil {
			h.mu.Lock()
			delete(h.rooms, id) // the next request tries again
			h.mu.Unlock()
		}
		close(rm.loaded)
	}
	select {
	case <-rm.loaded:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if rm.loadErr != nil {
		return nil, rm.loadErr
	}
	return rm, nil
}

func (h *Hub) load(ctx context.Context, rm *room) error {
	if h.cfg.Store == nil {
		return nil
	}
	if h.storeTombstone(ctx, rm.id) {
		return errGone
	}
	stored, err := h.cfg.Store.Load(ctx, StoreKey(rm.id))
	switch {
	case err == nil:
		var ops []crdt.Op
		if err := json.Unmarshal(stored.Data, &ops); err != nil {
			return fmt.Errorf("hub: the saved document %q is unreadable: %w", rm.id, err)
		}
		rm.doc.Apply(ops...)
		return nil
	case errors.Is(err, docstore.ErrNotFound):
		return nil
	}
	return err
}

func (h *Hub) role(r *http.Request, id string) Role {
	if h.cfg.Roles == nil {
		return Owner
	}
	return h.cfg.Roles(r, id)
}

// save writes the document to the store. Saves of one document run one
// after another, each taking its snapshot when its turn comes, so an older
// snapshot never overwrites a newer one. A failed save leaves the document
// dirty and is tried again.
func (h *Hub) save(rm *room) error {
	if h.cfg.Store == nil {
		return nil
	}
	rm.saveMu.Lock()
	defer rm.saveMu.Unlock()
	rm.mu.Lock()
	if rm.dead { // the document expired: its state must not be written back
		rm.dirty = false
		rm.mu.Unlock()
		return nil
	}
	rm.dirty = false
	data, err := json.Marshal(rm.doc.Snapshot())
	rm.mu.Unlock()
	if err == nil {
		err = h.cfg.Store.Save(context.Background(), StoreKey(rm.id), data)
	}
	if err != nil {
		rm.mu.Lock()
		h.markDirty(rm) // try again after the delay
		rm.mu.Unlock()
	}
	return err
}

// markDirty schedules a save (rm.mu held).
func (h *Hub) markDirty(rm *room) {
	if h.cfg.Store == nil || rm.dirty || rm.dead {
		return
	}
	rm.dirty = true
	rm.timer = time.AfterFunc(h.cfg.SaveDelay, func() {
		if err := h.save(rm); err != nil {
			log.Printf("collab hub: saving %q: %v (will retry)", rm.id, err)
		}
	})
}

// Health is what GET /healthz reports.
type Health struct {
	OK            bool  `json:"ok"`
	Rooms         int   `json:"rooms"`
	RenewFailures int64 `json:"renewFailures"` // renewals that failed (a document that is simply not in the store does not count)
	RenewDenied   int64 `json:"renewDenied"`   // of those, refused with 401 or 403: see Config.Renewer
	ExpiredRooms  int64 `json:"expiredRooms"`  // rooms closed because their document expired
	HubOnlyRooms  int64 `json:"hubOnlyRooms"`  // rooms whose document is not in the store, counted once each (nothing to renew)
	RevivedRooms  int64 `json:"revivedRooms"`  // closed rooms reopened because the document is live again
}

// Health returns the hub's counters.
func (h *Hub) Health() Health {
	h.mu.Lock()
	n := len(h.rooms)
	h.mu.Unlock()
	return Health{OK: !h.closing.Load(), Rooms: n, RenewFailures: h.renewFailures.Load(), RenewDenied: h.renewDenied.Load(), ExpiredRooms: h.deadRooms.Load(), HubOnlyRooms: h.hubOnlyRooms.Load(), RevivedRooms: h.revivedRooms.Load()}
}

// Flush saves every document with unsaved changes now, and waits for the expiry renewals in
// flight.
func (h *Hub) Flush() error {
	defer h.waitRenewals()
	h.mu.Lock()
	rooms := make([]*room, 0, len(h.rooms))
	for _, rm := range h.rooms {
		rooms = append(rooms, rm)
	}
	h.mu.Unlock()
	var errs []error
	for _, rm := range rooms {
		// A save already running clears the dirty flag before it has
		// written; wait for it, or its edits would count as saved.
		rm.saveMu.Lock()
		rm.saveMu.Unlock() //nolint:staticcheck // waiting for the holder is the point
		rm.mu.Lock()
		dirty := rm.dirty
		if rm.timer != nil {
			rm.timer.Stop()
		}
		rm.mu.Unlock()
		if dirty {
			errs = append(errs, h.save(rm))
		}
	}
	return errors.Join(errs...)
}

// Stop refuses further writes and new event streams and ends the open ones
// (a graceful shutdown calls it first, so http.Server.Shutdown does not wait
// on them and nothing is acknowledged after the final Close).
func (h *Hub) Stop() {
	h.closing.Store(true)
	h.stopOnce.Do(func() { close(h.stop) })
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rm := range h.rooms {
		rm.mu.Lock()
		for s := range rm.subs {
			close(s.ch)
			delete(rm.subs, s)
		}
		rm.mu.Unlock()
	}
}

// Close saves everything and drops the event streams.
func (h *Hub) Close() error {
	err := h.Flush()
	h.mu.Lock()
	for _, rm := range h.rooms {
		rm.mu.Lock()
		for s := range rm.subs {
			close(s.ch)
			delete(rm.subs, s)
		}
		rm.mu.Unlock()
	}
	h.mu.Unlock()
	return err
}
