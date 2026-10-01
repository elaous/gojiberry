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
	"io"
	"net/http"
	"strings"
	"sync"
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
	// MaxBody limits a request body in bytes; 0 means 8 MiB.
	MaxBody int64
}

// Hub serves documents. Create one with New and mount Handler.
type Hub struct {
	cfg   Config
	mu    sync.Mutex
	rooms map[string]*room
	now   func() time.Time
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
	id    string
	mu    sync.Mutex
	doc   *crdt.Doc
	subs  map[*sub]bool
	pres  map[string]presenceEntry
	dirty bool
	timer *time.Timer
}

type presenceEntry struct {
	p    Presence
	seen time.Time
}

func (h *Hub) room(ctx context.Context, id string) (*room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rm, ok := h.rooms[id]; ok {
		return rm, nil
	}
	rm := &room{id: id, doc: crdt.NewDocWithStrategy("hub", crdt.MergeCharCRDT), subs: map[*sub]bool{}, pres: map[string]presenceEntry{}}
	if h.cfg.Store != nil {
		stored, err := h.cfg.Store.Load(ctx, StoreKey(id))
		switch {
		case err == nil:
			var ops []crdt.Op
			if err := json.Unmarshal(stored.Data, &ops); err != nil {
				return nil, fmt.Errorf("hub: the saved document %q is unreadable: %w", id, err)
			}
			rm.doc.Apply(ops...)
		case errors.Is(err, docstore.ErrNotFound):
		default:
			return nil, err
		}
	}
	h.rooms[id] = rm
	return rm, nil
}

func (h *Hub) role(r *http.Request, id string) Role {
	if h.cfg.Roles == nil {
		return Owner
	}
	return h.cfg.Roles(r, id)
}

// save writes the document to the store.
func (h *Hub) save(rm *room) error {
	if h.cfg.Store == nil {
		return nil
	}
	rm.mu.Lock()
	rm.dirty = false
	data, err := json.Marshal(rm.doc.Snapshot())
	rm.mu.Unlock()
	if err != nil {
		return err
	}
	return h.cfg.Store.Save(context.Background(), StoreKey(rm.id), data)
}

// markDirty schedules a save (rm.mu held).
func (h *Hub) markDirty(rm *room) {
	if h.cfg.Store == nil || rm.dirty {
		return
	}
	rm.dirty = true
	rm.timer = time.AfterFunc(h.cfg.SaveDelay, func() { _ = h.save(rm) })
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
	return cors(mux)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
	role := h.role(r, id)
	if !role.CanRead() {
		denied(w, "you do not have access to this document")
		return nil, "", false
	}
	rm, err := h.room(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	for _, op := range ops {
		blk := op.Block
		existing, exists := doc.Get(blk.ID)
		if crdt.LWWKind(blk.Kind) || (exists && crdt.LWWKind(existing.Kind)) {
			isNew := !exists && !blk.Deleted
			isComment := strings.HasPrefix(blk.ID, "comment:") || strings.HasPrefix(blk.ID, "reply:")
			if !isNew || !isComment || !crdt.LWWKind(blk.Kind) {
				return errors.New(viewOnly)
			}
			continue
		}
		// A change to the text: only comment marks, leaving the block as it is.
		if !exists || len(op.CharOps) == 0 || blk.Kind != existing.Kind || blk.Order != existing.Order || blk.Deleted != existing.Deleted {
			return errors.New(viewOnly)
		}
		for _, c := range op.CharOps {
			if c.Kind != crdt.CharMark || c.Mark == nil || c.Mark.Key != "comment" {
				return errors.New(viewOnly)
			}
		}
	}
	return nil
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
	if p.Replica == "" {
		http.Error(w, "replica is required", http.StatusBadRequest)
		return
	}
	p.Role, p.Gone = role, false
	p.User = ""
	if h.cfg.User != nil {
		p.User = h.cfg.User(r)
	}
	rm.mu.Lock()
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
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported here", http.StatusInternalServerError)
		return
	}
	replica := r.URL.Query().Get("replica")
	s := &sub{replica: replica, role: role, ch: make(chan event, 256)}

	rm.mu.Lock()
	snapshot := rm.doc.Snapshot()
	var peers []Presence
	cutoff := h.now().Add(-h.cfg.PresenceTTL)
	for id, e := range rm.pres {
		switch {
		case e.seen.Before(cutoff):
			delete(rm.pres, id)
		case id != replica:
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
			if _, had := rm.pres[replica]; had {
				delete(rm.pres, replica)
				rm.broadcast(replica, jsonEvent("presence", Presence{Replica: replica, Gone: true}))
			}
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
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
