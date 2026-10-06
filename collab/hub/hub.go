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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/guard"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
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
	// Expired under an open room: once a renewal of a document has succeeded, a later "not found"
	// means the document expired and was swept while a stale tab kept its room. The room is then
	// closed: further operations get 410 Gone (and so does any request for the id for an hour), it
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
	// Now is the clock (time.Now when nil); tests inject a fake one.
	Now func() time.Time
}

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

	gone map[string]time.Time // ids whose room was closed as expired -> when that stops being remembered (h.mu)
}

// goneMemory is how long the hub refuses a document whose room it closed as expired, so a stale
// tab that reconnects does not start a new room and write the content back.
const goneMemory = time.Hour

// maxGone bounds the table of closed documents.
const maxGone = 4096

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
	h := &Hub{cfg: cfg, rooms: map[string]*room{}, now: time.Now, gone: map[string]time.Time{}}
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
func (h *Hub) room(ctx context.Context, id string) (*room, error) {
	h.mu.Lock()
	rm, ok := h.rooms[id]
	if !ok {
		if until, was := h.gone[id]; was {
			if h.now().Before(until) {
				h.mu.Unlock()
				return nil, errGone
			}
			delete(h.gone, id)
		}
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

// renewDue says whether an accepted edit should now renew the document's expiry, and if so
// starts the interval (rm.mu held).
func (h *Hub) renewDue(rm *room) bool {
	if h.cfg.Renewer == nil {
		return false
	}
	now := h.now()
	if rm.renewed && h.cfg.RenewInterval > 0 && now.Sub(rm.lastRenew) < h.cfg.RenewInterval && !now.Before(rm.lastRenew) {
		return false
	}
	rm.lastRenew, rm.renewed = now, true
	return true
}

// renew calls the Renewer for the room's document in the background; r is the request whose
// edit caused it (its Authorization, or AuthFunc's answer for it, authorizes the renewal).
// A panicking Renewer or AuthFunc is logged and recovered: it must not take the process down.
func (h *Hub) renew(rm *room, r *http.Request) {
	h.renewMu.Lock()
	h.renewing++
	h.renewMu.Unlock()
	go func() {
		defer func() {
			h.renewMu.Lock()
			h.renewing--
			if h.renewing == 0 {
				h.renewIdle.Broadcast()
			}
			h.renewMu.Unlock()
		}()
		defer func() {
			if p := recover(); p != nil {
				h.renewFailures.Add(1)
				log.Printf("collab hub: renewing the expiry of %q panicked: %v", rm.id, p)
			}
		}()
		auth := r.Header.Get("Authorization")
		if h.cfg.AuthFunc != nil {
			auth = h.cfg.AuthFunc(r)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if auth != "" {
			ctx = docstore.WithAuthorization(ctx, auth)
		}
		h.renewResult(rm, h.cfg.Renewer.Renew(ctx, rm.id))
	}()
}

// renewResult acts on the outcome of one renewal.
//
// ErrNotFound after an earlier success means the document expired and was swept while this room
// stayed open in a stale tab: the room is closed (see closeDead). Before any success it means
// nothing (a document that lives only in the hub was never saved to the store, so it is always
// not found), and is only logged. A 401 or 403 is the store refusing this editor's credentials,
// most often because the hub's role rule and the docstore's access rule disagree (see
// Config.Renewer): it is counted and logged as a warning once per document.
func (h *Hub) renewResult(rm *room, err error) {
	switch {
	case err == nil:
		rm.mu.Lock()
		rm.renewedOnce = true
		rm.mu.Unlock()
	case errors.Is(err, docstore.ErrNotFound):
		rm.mu.Lock()
		was := rm.renewedOnce
		rm.mu.Unlock()
		if was {
			log.Printf("collab hub: %q expired in the store while it was open: closing it", rm.id)
			h.closeDead(rm)
		} else {
			log.Printf("collab hub: renewing the expiry of %q: %v", rm.id, err)
		}
	default:
		h.renewFailures.Add(1)
		var se *docstore.StatusError
		if errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden) {
			h.renewDenied.Add(1)
			rm.mu.Lock()
			warned := rm.authWarned
			rm.authWarned = true
			rm.mu.Unlock()
			if !warned {
				log.Printf("collab hub: WARNING the store refused to renew %q (%d): the editor's credentials were rejected, or the store requires an access list on this document while the hub's roles allow editing without one (GOJIBERRY_COLLAB_NO_ACL_ROLE=owner/editor against GOJIBERRY_DOCSTORE_REQUIRE_ACL=true: the two settings must agree). Edits will not keep the document alive and it will expire. Further refusals are counted in /healthz renewFailures only.", rm.id, se.Status)
			}
			return
		}
		log.Printf("collab hub: renewing the expiry of %q: %v", rm.id, err)
	}
}

// closeDead retires a room whose document expired in the store: it rejects further operations
// with 410 Gone, stops saving, deletes the hub state "collab:<id>" (a save may have written it
// after the sweeper removed it), ends its streams and removes it from the hub; the id is then
// refused with 410 for a while (goneMemory) so a stale tab that reconnects cannot start a new
// room that writes the content back.
func (h *Hub) closeDead(rm *room) {
	// Wait for a save in flight, so that the delete below is the last write.
	rm.saveMu.Lock()
	defer rm.saveMu.Unlock()
	rm.mu.Lock()
	if rm.dead {
		rm.mu.Unlock()
		return
	}
	rm.dead, rm.dirty = true, false
	if rm.timer != nil {
		rm.timer.Stop()
	}
	for s := range rm.subs {
		close(s.ch)
		delete(rm.subs, s)
	}
	for id := range rm.pres {
		rm.dropPresence(id)
	}
	rm.mu.Unlock()
	h.deadRooms.Add(1)

	h.mu.Lock()
	if h.rooms[rm.id] == rm {
		delete(h.rooms, rm.id)
	}
	if len(h.gone) >= maxGone {
		now := h.now()
		for id, until := range h.gone {
			if !now.Before(until) {
				delete(h.gone, id)
			}
		}
		for id := range h.gone { // still full: drop an arbitrary one
			if len(h.gone) < maxGone {
				break
			}
			delete(h.gone, id)
		}
	}
	h.gone[rm.id] = h.now().Add(goneMemory)
	h.mu.Unlock()

	if h.cfg.Store != nil {
		if err := h.cfg.Store.Delete(context.Background(), StoreKey(rm.id)); err != nil && !errors.Is(err, docstore.ErrNotFound) {
			log.Printf("collab hub: deleting the state of the expired %q: %v", rm.id, err)
		}
	}
}

func (h *Hub) isDead(rm *room) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.dead
}

// Health is what GET /healthz reports.
type Health struct {
	OK            bool  `json:"ok"`
	Rooms         int   `json:"rooms"`
	RenewFailures int64 `json:"renewFailures"` // renewals that failed (a document that is simply not in the store does not count)
	RenewDenied   int64 `json:"renewDenied"`   // of those, refused with 401 or 403: see Config.Renewer
	ExpiredRooms  int64 `json:"expiredRooms"`  // rooms closed because their document expired
}

// Health returns the hub's counters.
func (h *Hub) Health() Health {
	h.mu.Lock()
	n := len(h.rooms)
	h.mu.Unlock()
	return Health{OK: !h.closing.Load(), Rooms: n, RenewFailures: h.renewFailures.Load(), RenewDenied: h.renewDenied.Load(), ExpiredRooms: h.deadRooms.Load()}
}

func (h *Hub) handleHealth(w http.ResponseWriter, r *http.Request) {
	hl := h.Health()
	status := http.StatusOK
	if !hl.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, hl)
}

// waitRenewals blocks until the renewals in flight have finished.
func (h *Hub) waitRenewals() {
	h.renewMu.Lock()
	for h.renewing > 0 {
		h.renewIdle.Wait()
	}
	h.renewMu.Unlock()
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

// Handler returns the routes.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /docs/{id}/events", h.handleEvents)
	mux.HandleFunc("GET /docs/{id}/snapshot", h.handleSnapshot)
	mux.HandleFunc("GET /docs/{id}/ws", h.handleWS)
	mux.HandleFunc("POST /docs/{id}/ops", h.handleOps)
	mux.HandleFunc("POST /docs/{id}/presence", h.handlePresence)
	mux.HandleFunc("GET /docs/{id}/role", h.handleRole)
	mux.HandleFunc("GET /healthz", h.handleHealth)
	return tokenFromQuery(guard.Or(h.cfg.Guard).Wrap(mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func denied(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
}

// access finds the document and the caller's role, writing the error
// response itself when there is no access.
func (h *Hub) access(w http.ResponseWriter, r *http.Request) (*room, Role, bool) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\") {
		http.Error(w, "bad document id", http.StatusBadRequest)
		return nil, "", false
	}
	if h.closing.Load() {
		http.Error(w, "the hub is shutting down", http.StatusServiceUnavailable)
		return nil, "", false
	}
	role := h.role(r, id)
	if !role.CanRead() {
		denied(w, "you do not have access to this document")
		return nil, "", false
	}
	rm, err := h.room(r.Context(), id)
	switch {
	case errors.Is(err, errTooManyRooms):
		http.Error(w, "too many documents", http.StatusServiceUnavailable)
		return nil, "", false
	case errors.Is(err, errGone):
		http.Error(w, goneMsg, http.StatusGone)
		return nil, "", false
	case err != nil:
		log.Printf("collab hub: opening %q: %v", id, err)
		http.Error(w, "the document could not be opened", http.StatusInternalServerError)
		return nil, "", false
	}
	return rm, role, true
}

func (h *Hub) handleRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	writeJSON(w, http.StatusOK, map[string]Role{"role": h.role(r, id)})
}

func (h *Hub) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	rm, _, ok := h.access(w, r)
	if !ok {
		return
	}
	rm.mu.Lock()
	ops := rm.doc.Snapshot()
	rm.mu.Unlock()
	writeJSON(w, http.StatusOK, ops)
}

// broadcast sends an event to every subscriber but from (rm.mu held). A
// subscriber that cannot keep up is dropped; its client reconnects and
// gets a fresh snapshot.
func (rm *room) broadcast(from string, e event) {
	for s := range rm.subs {
		if s.replica == from && from != "" {
			continue
		}
		select {
		case s.ch <- e:
		default:
			close(s.ch)
			delete(rm.subs, s)
		}
	}
}

func jsonEvent(name string, v any) event {
	b, _ := json.Marshal(v)
	return event{name: name, data: b}
}

// bindReplica ties a replica id to the signed-in user who first used it in
// this document (rm.mu held), so another user cannot speak under it: their
// ops would otherwise be hidden from the real owner of the id, and their
// presence would overwrite its cursor. Anonymous callers (no User function, or
// no one signed in) cannot be bound.
func (h *Hub) bindReplica(rm *room, replica string, r *http.Request) bool {
	if replica == "" || h.cfg.User == nil {
		return true
	}
	user := h.cfg.User(r)
	if user == "" {
		return true
	}
	if owner, ok := rm.owners[replica]; ok {
		return owner == user
	}
	if rm.owners == nil {
		rm.owners = map[string]string{}
	}
	if len(rm.owners) < maxBoundReplicas {
		rm.owners[replica] = user
	}
	return true
}

// maxBoundReplicas bounds the replica-to-user table of one document.
const maxBoundReplicas = 4096

func (h *Hub) handleOps(w http.ResponseWriter, r *http.Request) {
	rm, role, ok := h.access(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.cfg.MaxBody+1))
	if err != nil || int64(len(body)) > h.cfg.MaxBody {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	var ops []crdt.Op
	if err := json.Unmarshal(body, &ops); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch status, msg := h.acceptOps(rm, role, r, r.URL.Query().Get("replica"), ops); status {
	case http.StatusNoContent:
		w.WriteHeader(http.StatusNoContent)
	case http.StatusForbidden:
		denied(w, msg)
	default:
		http.Error(w, msg, status)
	}
}

// acceptOps checks and applies operations from replica, whatever the
// transport carried them, and tells the others. It returns 204, or the status
// and message the transport should report (403: refused).
//
// An accepted batch from an owner or editor renews the document's expiry (see
// Config.Renewer); nothing else does.
func (h *Hub) acceptOps(rm *room, role Role, r *http.Request, replica string, ops []crdt.Op) (int, string) {
	status, msg, renew := h.applyOps(rm, role, r, replica, ops)
	if renew {
		h.renew(rm, r)
	}
	return status, msg
}

func (h *Hub) applyOps(rm *room, role Role, r *http.Request, replica string, ops []crdt.Op) (status int, msg string, renew bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.dead {
		return http.StatusGone, goneMsg, false
	}
	if !h.bindReplica(rm, replica, r) {
		return http.StatusForbidden, "that replica id belongs to another user", false
	}
	if err := authorize(rm.doc, role, ops); err != nil {
		return http.StatusForbidden, err.Error(), false
	}
	if !role.CanEdit() && h.cfg.User != nil {
		// A commenter may only write comments and replies under their own name.
		if err := commentAuthors(ops, h.cfg.User(r)); err != nil {
			return http.StatusForbidden, err.Error(), false
		}
	}
	rm.doc.Apply(ops...)
	rm.broadcast(replica, jsonEvent("ops", ops))
	h.markDirty(rm)
	return http.StatusNoContent, "", role.CanEdit() && len(ops) > 0 && h.renewDue(rm)
}

// commentAuthors checks that every new comment or reply record in ops names
// user as its author (case-insensitively). With nobody signed in (user "")
// there is nothing to check against.
func commentAuthors(ops []crdt.Op, user string) error {
	if user == "" {
		return nil
	}
	for _, op := range ops {
		id := op.Block.ID
		if op.Block.Deleted || (!strings.HasPrefix(id, "comment:") && !strings.HasPrefix(id, "reply:")) {
			continue
		}
		var rec struct {
			Author string `json:"author"`
		}
		if json.Unmarshal([]byte(op.Block.Content), &rec) != nil || !strings.EqualFold(rec.Author, user) {
			return errors.New("a comment must be written under your own name")
		}
	}
	return nil
}

const (
	// maxClockJump is how far past the document's clock a commenter's
	// timestamps may be.
	maxClockJump = 1 << 16
	// maxCommentSize is the largest comment or reply record a commenter may
	// add, in bytes.
	maxCommentSize = 16 << 10
)

// authorize decides whether role may make these changes to doc. Owners and
// editors may change anything. Anyone else who can read may comment: mark a
// range of the text as commented (and nothing else about the text), and add
// new comment and reply records -- but not change or delete existing ones.
func authorize(doc *crdt.Doc, role Role, ops []crdt.Op) error {
	if role.CanEdit() {
		return nil
	}
	if !role.CanComment() {
		return errors.New("you do not have access to this document")
	}
	const viewOnly = "you can only view and comment on this document"
	// A Lamport counter far ahead of anything the document has seen would
	// win every later conflict on what it touches.
	limit := doc.Clock() + maxClockJump
	if limit < doc.Clock() { // wrapped: the clock is already at the top
		limit = ^uint64(0)
	}
	for _, op := range ops {
		blk := op.Block
		if op.TS.Counter > limit {
			return errors.New(viewOnly)
		}
		existing, exists := doc.Get(blk.ID)
		if crdt.LWWKind(blk.Kind) || (exists && crdt.LWWKind(existing.Kind)) {
			isNew := !exists && !blk.Deleted
			isComment := strings.HasPrefix(blk.ID, "comment:") || strings.HasPrefix(blk.ID, "reply:")
			if !isNew || !isComment || !crdt.LWWKind(blk.Kind) || len(blk.Content) > maxCommentSize || len(blk.ID) > 256 {
				return errors.New(viewOnly)
			}
			continue
		}
		// A change to the text: only comment marks, leaving the block as it is.
		if !exists || len(op.CharOps) == 0 || blk.Kind != existing.Kind || blk.Order != existing.Order || blk.Deleted != existing.Deleted {
			return errors.New(viewOnly)
		}
		for _, c := range op.CharOps {
			// Every character operation advances the document's clock to its id.
			if c.ID.Counter > limit {
				return errors.New(viewOnly)
			}
			// A comment mark that sets a comment: an empty value would erase
			// the comments already on the text.
			if c.Kind != crdt.CharMark || c.Mark == nil || c.Mark.Key != "comment" || c.Mark.Value == "" || c.Mark.ID.Counter > limit || len(c.Mark.Value) > 256 {
				return errors.New(viewOnly)
			}
		}
	}
	return nil
}

// prunePresence drops cursors that have not been refreshed within the TTL and
// tells the others they are gone (rm.mu held).
func (h *Hub) prunePresence(rm *room) {
	cutoff := h.now().Add(-h.cfg.PresenceTTL)
	for id, e := range rm.pres {
		if e.seen.Before(cutoff) {
			rm.dropPresence(id)
			rm.broadcast(id, jsonEvent("presence", Presence{Replica: id, Gone: true}))
		}
	}
}

// setPresence records p as its replica's cursor (what newcomers are told at
// once) and tells the others, at most once per PresenceInterval: an update
// inside the interval is held, and one send of the latest value goes out when
// it ends (rm.mu held).
func (h *Hub) setPresence(rm *room, p Presence) {
	now := h.now()
	e := rm.pres[p.Replica]
	e.p, e.seen = p, now
	interval := h.cfg.PresenceInterval
	switch {
	case interval < 0 || now.Sub(e.lastSent) >= interval:
		if e.timer != nil {
			e.timer.Stop()
			e.timer = nil
		}
		e.lastSent = now
		rm.pres[p.Replica] = e
		rm.broadcast(p.Replica, jsonEvent("presence", p))
	case e.timer == nil:
		replica := p.Replica
		e.timer = time.AfterFunc(interval-now.Sub(e.lastSent), func() {
			rm.mu.Lock()
			defer rm.mu.Unlock()
			cur, ok := rm.pres[replica]
			if !ok {
				return
			}
			cur.timer, cur.lastSent = nil, h.now()
			rm.pres[replica] = cur
			rm.broadcast(replica, jsonEvent("presence", cur.p)) // the latest, whatever came meanwhile
		})
		rm.pres[p.Replica] = e
	default:
		rm.pres[p.Replica] = e // the scheduled send will carry this one
	}
}

func (h *Hub) handlePresence(w http.ResponseWriter, r *http.Request) {
	rm, role, ok := h.access(w, r)
	if !ok {
		return
	}
	var p Presence
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch status, msg := h.acceptPresence(rm, role, r, p); status {
	case http.StatusNoContent:
		w.WriteHeader(http.StatusNoContent)
	case http.StatusForbidden:
		denied(w, msg)
	default:
		http.Error(w, msg, status)
	}
}

// acceptPresence validates and records a cursor update from any transport.
func (h *Hub) acceptPresence(rm *room, role Role, r *http.Request, p Presence) (int, string) {
	if p.Replica == "" || len(p.Replica) > 128 || len(p.Name) > 256 || len(p.Color) > 64 {
		return http.StatusBadRequest, "replica is required (at most 128 characters)"
	}
	p.Role, p.Gone = role, false
	p.User = ""
	if h.cfg.User != nil {
		p.User = h.cfg.User(r)
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if !h.bindReplica(rm, p.Replica, r) {
		return http.StatusForbidden, "that replica id belongs to another user"
	}
	h.prunePresence(rm)
	if _, known := rm.pres[p.Replica]; !known && len(rm.pres) >= h.cfg.MaxPeers {
		return http.StatusTooManyRequests, "too many people on this document"
	}
	h.setPresence(rm, p)
	return http.StatusNoContent, ""
}

// join subscribes replica to the document: it returns the subscription, the
// snapshot and the others' cursors to tell it first. The caller must call
// leave. ok is false if the replica id belongs to someone else.
func (h *Hub) join(rm *room, role Role, r *http.Request, replica string) (s *sub, snapshot []crdt.Op, peers []Presence, ok bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if !h.bindReplica(rm, replica, r) {
		return nil, nil, nil, false
	}
	s = &sub{replica: replica, role: role, ch: make(chan event, 256)}
	snapshot = rm.doc.Snapshot()
	h.prunePresence(rm)
	for id, e := range rm.pres {
		if id != replica {
			peers = append(peers, e.p)
		}
	}
	rm.subs[s] = true
	return s, snapshot, peers, true
}

// leave ends a subscription and tells the others the cursor is gone.
func (h *Hub) leave(rm *room, s *sub) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.subs[s] {
		delete(rm.subs, s)
	}
	if s.replica != "" {
		// Always say so: an idle replica's cursor may have been pruned
		// already, and its peers would otherwise keep the ghost.
		rm.dropPresence(s.replica)
		rm.broadcast(s.replica, jsonEvent("presence", Presence{Replica: s.replica, Gone: true}))
	}
}

func (h *Hub) handleEvents(w http.ResponseWriter, r *http.Request) {
	rm, role, ok := h.access(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported here", http.StatusInternalServerError)
		return
	}
	replica := r.URL.Query().Get("replica")
	s, snapshot, peers, ok := h.join(rm, role, r, replica)
	if !ok {
		denied(w, "that replica id belongs to another user")
		return
	}
	defer h.leave(rm, s)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(e event) bool {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "event: %s\ndata: %s\n\n", e.name, e.data)
		_, err := w.Write(buf.Bytes())
		fl.Flush()
		return err == nil
	}
	if !write(jsonEvent("hello", map[string]any{"role": role, "replica": replica})) || !write(jsonEvent("snapshot", snapshot)) {
		return
	}
	for _, p := range peers {
		if !write(jsonEvent("presence", p)) {
			return
		}
	}
	tick := time.NewTicker(h.cfg.KeepAlive)
	defer tick.Stop()
	for {
		select {
		case e, open := <-s.ch:
			if !open || !write(e) {
				return
			}
		case <-tick.C:
			// Access can be taken away or changed while a stream is open:
			// end it, and the client reconnects with whatever it has now.
			if h.role(r, id) != role {
				return
			}
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
