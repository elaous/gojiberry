package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// handleWS serves the document's WebSocket endpoint.
func (h *Hub) handleWS(w http.ResponseWriter, r *http.Request) {
	rm, role, ok := h.access(w, r)
	if !ok {
		return
	}
	if !h.cookieSocketAllowed(r) {
		http.Error(w, "cross-origin WebSocket with cookies", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	replica := r.URL.Query().Get("replica")
	// Access was checked before the upgrade.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return // Accept has answered
	}
	defer func() { _ = c.CloseNow() }()
	// A frame over MaxBody is answered with a 413 reject.
	c.SetReadLimit(min(4*h.cfg.MaxBody, h.cfg.MaxBody+1<<20))
	s, snapshot, peers, ok := h.join(rm, role, r, replica)
	if !ok {
		_ = c.Close(websocket.StatusPolicyViolation, "that replica id belongs to another user")
		return
	}
	defer h.leave(rm, s)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	out := make(chan event, 64) // Answers to client frames, written by the loop.
	incoming := make(chan struct{})
	// send queues an answer, or gives up when the connection closes.
	send := func(e event) bool {
		select {
		case out <- e:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() { // reads what the client sends
		defer close(incoming)
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var f wire.Frame
			if json.Unmarshal(data, &f) != nil {
				continue
			}
			switch f.Event {
			case "ops", "presence":
				// Access is checked on every frame, not only on open.
				if h.role(r, id) != role {
					_ = c.Close(websocket.StatusPolicyViolation, "access changed")
					return
				}
			}
			switch f.Event {
			case "ops":
				if int64(len(f.Data)) > h.cfg.MaxBody {
					if !send(ackEvent("reject", f.ID, "operations too large", http.StatusRequestEntityTooLarge)) {
						return
					}
					continue
				}
				var ops []crdt.Op
				if json.Unmarshal(f.Data, &ops) != nil {
					if !send(ackEvent("reject", f.ID, "bad operations", http.StatusBadRequest)) {
						return
					}
					continue
				}
				if status, msg := h.acceptOps(rm, role, r, replica, ops); status == http.StatusNoContent {
					if !send(ackEvent("ack", f.ID, "", 0)) {
						return
					}
				} else if !send(ackEvent("reject", f.ID, msg, status)) {
					return
				}
			case "presence":
				var p Presence
				if json.Unmarshal(f.Data, &p) == nil {
					p.Replica = replica // a socket speaks for its own replica only
					_, _ = h.acceptPresence(rm, role, r, p)
				}
			}
		}
	}()

	write := func(e event) bool {
		f := wire.Frame{Event: e.name, Data: e.data}
		if e.name == "ack" || e.name == "reject" {
			var a ackData
			_ = json.Unmarshal(e.data, &a)
			f = wire.Frame{Event: e.name, ID: a.ID, Error: a.Error, Data: e.data}
		}
		b, err := json.Marshal(f)
		if err != nil {
			return false
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return c.Write(wctx, websocket.MessageText, b) == nil
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
			if !open {
				if h.isDead(rm) { // the document expired: say so (4410 is 410 Gone) instead of just hanging up
					_ = c.Close(websocket.StatusCode(4410), goneMsg)
				}
				return
			}
			if !write(e) {
				return
			}
		case e := <-out:
			if !write(e) {
				return
			}
		case <-tick.C:
			if h.role(r, id) != role { // access changed: end the socket, the client reconnects
				_ = c.Close(websocket.StatusPolicyViolation, "access changed")
				return
			}
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-incoming:
			return
		}
	}
}

// ackData is the payload of an ack or reject event.
type ackData struct {
	ID     uint64 `json:"id"`
	Error  string `json:"error,omitempty"`
	Status int    `json:"status,omitempty"`
}

func ackEvent(name string, id uint64, msg string, status int) event {
	return jsonEvent(name, ackData{ID: id, Error: msg, Status: status})
}

// tokenFromQuery lets browsers pass a token in the URL.
func tokenFromQuery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") && r.Header.Get("Authorization") == "" {
			if t := r.URL.Query().Get("access_token"); t != "" {
				r.Header.Set("Authorization", "Bearer "+t)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// cookieSocketAllowed refuses a WebSocket from another origin.
func (h *Hub) cookieSocketAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || r.Header.Get("Cookie") == "" {
		return true
	}
	if u, err := url.Parse(origin); err == nil {
		if strings.EqualFold(u.Host, r.Host) {
			return true
		}
		if fwd := r.Header.Get("X-Forwarded-Host"); h.cfg.TrustForwardedHost && fwd != "" && strings.EqualFold(u.Host, fwd) {
			return true
		}
	}
	for _, o := range h.cfg.CookieOrigins {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}
