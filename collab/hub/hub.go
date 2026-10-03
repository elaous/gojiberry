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
	// MaxRooms limits how many documents the hub keeps in memory; 0 means
	// 1000. Further documents are refused until the hub restarts.
	MaxRooms int
	// MaxPeers limits the cursors kept per document; 0 means 256.
	MaxPeers int
}

// Hub serves documents. Create one with New and mount Handler.
type Hub struct {
	cfg   Config
	mu    sync.Mutex
	rooms map[string]*room
	now   func() time.Time

	closing atomic.Bool // set by Stop: no more writes or new streams
}

// New creates a hub.
func New(cfg Config) *Hub {
	if cfg.SaveDelay == 0 {
		cfg.SaveDelay = 2 * time.Second
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = 15 * time.Second
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
	return &Hub{cfg: cfg, rooms: map[string]*room{}, now: time.Now}
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

	saveMu  sync.Mutex    // one save at a time, in order
	loaded  chan struct{} // closed when the stored document has been read
	loadErr error
}

type presenceEntry struct {
	p    Presence
	seen time.Time
}

var errTooManyRooms = errors.New("hub: too many documents")

// room finds or loads a document. The store is read outside the hub's lock,
// so a slow load of one document does not hold up the others.
func (h *Hub) room(ctx context.Context, id string) (*room, error) {
	h.mu.Lock()
	rm, ok := h.rooms[id]
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
	if h.cfg.Store == nil || rm.dirty {
		return
	}
	rm.dirty = true
	rm.timer = time.AfterFunc(h.cfg.SaveDelay, func() {
		if err := h.save(rm); err != nil {
			log.Printf("collab hub: saving %q: %v (will retry)", rm.id, err)
		}
	})
}

// Flush saves every document with unsaved changes now.
func (h *Hub) Flush() error {
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
	mux.HandleFunc("POST /docs/{id}/ops", h.handleOps)
	mux.HandleFunc("POST /docs/{id}/presence", h.handlePresence)
	mux.HandleFunc("GET /docs/{id}/role", h.handleRole)
	return guard.Or(h.cfg.Guard).Wrap(mux)
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
	rm.mu.Lock()
	if !h.bindReplica(rm, r.URL.Query().Get("replica"), r) {
		rm.mu.Unlock()
		denied(w, "that replica id belongs to another user")
		return
	}
	if err := authorize(rm.doc, role, ops); err != nil {
		rm.mu.Unlock()
		denied(w, err.Error())
		return
	}
	rm.doc.Apply(ops...)
	rm.broadcast(r.URL.Query().Get("replica"), jsonEvent("ops", ops))
	h.markDirty(rm)
	rm.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
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
			delete(rm.pres, id)
			rm.broadcast(id, jsonEvent("presence", Presence{Replica: id, Gone: true}))
		}
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
	if p.Replica == "" || len(p.Replica) > 128 || len(p.Name) > 256 || len(p.Color) > 64 {
		http.Error(w, "replica is required (at most 128 characters)", http.StatusBadRequest)
		return
	}
	p.Role, p.Gone = role, false
	p.User = ""
	if h.cfg.User != nil {
		p.User = h.cfg.User(r)
	}
	rm.mu.Lock()
	if !h.bindReplica(rm, p.Replica, r) {
		rm.mu.Unlock()
		denied(w, "that replica id belongs to another user")
		return
	}
	h.prunePresence(rm)
	if _, known := rm.pres[p.Replica]; !known && len(rm.pres) >= h.cfg.MaxPeers {
		rm.mu.Unlock()
		http.Error(w, "too many people on this document", http.StatusTooManyRequests)
		return
	}
	rm.pres[p.Replica] = presenceEntry{p: p, seen: h.now()}
	rm.broadcast(p.Replica, jsonEvent("presence", p))
	rm.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
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
	s := &sub{replica: replica, role: role, ch: make(chan event, 256)}

	rm.mu.Lock()
	if !h.bindReplica(rm, replica, r) {
		rm.mu.Unlock()
		denied(w, "that replica id belongs to another user")
		return
	}
	snapshot := rm.doc.Snapshot()
	var peers []Presence
	h.prunePresence(rm)
	for id, e := range rm.pres {
		if id != replica {
			peers = append(peers, e.p)
		}
	}
	rm.subs[s] = true
	rm.mu.Unlock()
	defer func() {
		rm.mu.Lock()
		if rm.subs[s] {
			delete(rm.subs, s)
		}
		if replica != "" {
			// Always say so: an idle replica's cursor may have been pruned
			// already, and its peers would otherwise keep the ghost.
			delete(rm.pres, replica)
			rm.broadcast(replica, jsonEvent("presence", Presence{Replica: replica, Gone: true}))
		}
		rm.mu.Unlock()
	}()

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
