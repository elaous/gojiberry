package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func (h *Hub) handleHealth(w http.ResponseWriter, r *http.Request) {
	hl := h.Health()
	status := http.StatusOK
	if !hl.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, hl)
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

// access finds the document and the caller's role.
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
	rm, err := h.room(r, id, role)
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

// broadcast sends an event to every subscriber but the sender.
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

// bindReplica ties a replica id to the signed-in user.
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

// maxBoundReplicas bounds the replica-to-user table.
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

// acceptOps checks and applies operations from replicas.
func (h *Hub) acceptOps(rm *room, role Role, r *http.Request, replica string, ops []crdt.Op) (int, string) {
	done := observe.Default().Work("hub_ops")
	status, msg, renew := h.applyOps(rm, role, r, replica, ops)
	switch status {
	case http.StatusNoContent:
		done("ok")
	case http.StatusForbidden:
		done("rejected")
	default:
		done("error")
	}
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
		// A commenter may only write comments and replies.
		if err := commentAuthors(ops, h.cfg.User(r)); err != nil {
			return http.StatusForbidden, err.Error(), false
		}
	}
	rm.doc.Apply(ops...)
	rm.broadcast(replica, jsonEvent("ops", ops))
	h.markDirty(rm)
	return http.StatusNoContent, "", role.CanEdit() && len(ops) > 0 && h.renewDue(rm)
}

// commentAuthors checks authors of new comments and replies.
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
	// maxClockJump is how far a clock may run ahead of the document.
	maxClockJump = 1 << 16
	// maxCommentSize is the largest comment or reply allowed.
	maxCommentSize = 16 << 10
)

// authorize decides whether a role may make these changes.
func authorize(doc *crdt.Doc, role Role, ops []crdt.Op) error {
	if role.CanEdit() {
		return nil
	}
	if !role.CanComment() {
		return errors.New("you do not have access to this document")
	}
	const viewOnly = "you can only view and comment on this document"
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
		if !exists || len(op.CharOps) == 0 || blk.Kind != existing.Kind || blk.Order != existing.Order || blk.Deleted != existing.Deleted {
			return errors.New(viewOnly)
		}
		for _, c := range op.CharOps {
			// Each character operation advances the clock.
			if c.ID.Counter > limit {
				return errors.New(viewOnly)
			}
			if c.Kind != crdt.CharMark || c.Mark == nil || c.Mark.Key != "comment" || c.Mark.Value == "" || c.Mark.ID.Counter > limit || len(c.Mark.Value) > 256 {
				return errors.New(viewOnly)
			}
		}
	}
	return nil
}

// prunePresence drops cursors not refreshed recently.
func (h *Hub) prunePresence(rm *room) {
	cutoff := h.now().Add(-h.cfg.PresenceTTL)
	for id, e := range rm.pres {
		if e.seen.Before(cutoff) {
			rm.dropPresence(id)
			rm.broadcast(id, jsonEvent("presence", Presence{Replica: id, Gone: true}))
		}
	}
}

// setPresence records p as its replica's cursor.
func (h *Hub) setPresence(rm *room, p Presence) {
	if len(p.Holding) > MaxHolding { // a name of an object, not a message
		p.Holding = ""
	}
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

// acceptPresence validates and records a cursor update.
func (h *Hub) acceptPresence(rm *room, role Role, r *http.Request, p Presence) (status int, msg string) {
	done := observe.Default().Work("hub_presence")
	defer func() {
		switch {
		case status == http.StatusNoContent:
			done("ok")
		case status >= 500:
			done("error")
		default:
			done("rejected")
		}
	}()
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

// join subscribes a replica to the document.
func (h *Hub) join(rm *room, role Role, r *http.Request, replica string) (s *sub, snapshot []crdt.Op, peers []Presence, ok bool) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if !h.bindReplica(rm, replica, r) {
		return nil, nil, nil, false
	}
	observe.Default().Work("hub_join")("ok")
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

// leave ends a subscription.
func (h *Hub) leave(rm *room, s *sub) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	if rm.subs[s] {
		delete(rm.subs, s)
	}
	if s.replica != "" {
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

// MaxHolding is the longest Presence.Holding the hub accepts.
const MaxHolding = 80
