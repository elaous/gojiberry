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

// handleWS serves GET /docs/{id}/ws: the same conversation as the event
// stream plus the ops and presence posts, over one WebSocket (see wire.Frame).
// Access, roles, replica binding and throttling are the HTTP transport's: both
// go through the same checks.
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
	// Who may open the socket was decided above (the Guard, the roles, the
	// origin allow-list if one is set); the library's own same-host origin
	// rule would only get in the way of a page on another origin.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return // Accept has answered
	}
	defer func() { _ = c.CloseNow() }()
	// A frame over MaxBody is answered with a 413 reject, as the POST would be,
	// so the client learns that it will never be accepted; the library's hard
	// limit (which closes the socket) is higher: four times, but no more than 1 MiB
	// over MaxBody, so a client cannot make the hub buffer much more than a POST
	// could carry.
	c.SetReadLimit(min(4*h.cfg.MaxBody, h.cfg.MaxBody+1<<20))
	s, snapshot, peers, ok := h.join(rm, role, r, replica)
	if !ok {
		_ = c.Close(websocket.StatusPolicyViolation, "that replica id belongs to another user")
		return
	}
	defer h.leave(rm, s)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	out := make(chan event, 64) // answers to the client's frames, written by the loop below
	incoming := make(chan struct{})
	// send queues an answer, or gives up when the connection is done (a client
	// that floods frames and never reads must not park this goroutine).
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
				// Access is checked on every frame, not only on the keep-alive tick:
				// a downgraded user must not write until the next tick.
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
			if !open || !write(e) {
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

// ackData is what an ack or reject event carries inside the hub.
type ackData struct {
	ID     uint64 `json:"id"`
	Error  string `json:"error,omitempty"`
	Status int    `json:"status,omitempty"`
}

func ackEvent(name string, id uint64, msg string, status int) event {
	return jsonEvent(name, ackData{ID: id, Error: msg, Status: status})
}

// tokenFromQuery lets a browser, which cannot set headers on a WebSocket,
// pass the sign-in token as ?access_token=: for the socket's path it becomes
// the Authorization header the Roles and User functions read. (A token in a
// URL can end up in logs; use it over TLS and keep tokens short-lived.)
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

// cookieSocketAllowed refuses a WebSocket from another origin that carries
// cookies unless the origin is listed in Config.CookieOrigins (see there).
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
