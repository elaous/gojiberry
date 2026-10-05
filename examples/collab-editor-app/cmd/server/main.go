// Command server runs the live collab-editor-app demo: it serves the
// static client (index.html, main.wasm, wasm_exec.js, the components
// CSS the client needs) and a /ws WebSocket endpoint that relays
// every edit to every other connected client, so two or more browser
// tabs editing the same document actually see each other's changes in
// real time -- not a simulation of collaboration, the real thing,
// over a real WebSocket connection each client opens via
// domrt/browser.Connect.
//
// The sync strategy is deliberately the simplest one that's still
// genuinely real-time and correct for a single shared document: each
// edit is the document's *entire* current text (not a diff/operational
// transform), the server keeps the latest one in memory, and a newly
// connecting client is sent that snapshot immediately so it starts in
// sync. Last edit wins if two people type at the exact same instant --
// a real product would want operational transforms or a CRDT to merge
// concurrent edits properly, which is a much bigger project than this
// demo's scope.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// hub tracks every connected client and the document's last known
// content, so a client that joins after others have already been
// editing starts from the current state instead of a blank page.
type hub struct {
	mu      sync.Mutex
	clients map[*websocket.Conn]bool
	doc     string
}

func newHub() *hub {
	return &hub{clients: make(map[*websocket.Conn]bool)}
}

func (h *hub) broadcast(ctx context.Context, from *websocket.Conn, msg []byte) {
	h.mu.Lock()
	peers := make([]*websocket.Conn, 0, len(h.clients))
	for c := range h.clients {
		if c != from {
			peers = append(peers, c)
		}
	}
	h.mu.Unlock()
	for _, c := range peers {
		// A peer that is not reading must not hold up everyone else: give each write a
		// deadline, and drop the peer that misses it.
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := c.Write(wctx, websocket.MessageText, msg); err != nil {
			_ = c.CloseNow()
		}
		cancel()
	}
}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	// Only pages served by this server may open the socket (the library's default
	// check: the Origin's host must be the request's Host), so another site open in the
	// same browser cannot read or overwrite the document through localhost. Set
	// ALLOWED_ORIGINS (comma-separated host patterns) to let others in.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: allowedOrigins()})
	if err != nil {
		log.Println("websocket accept:", err)
		return
	}
	defer conn.CloseNow()

	ctx := r.Context()

	h.mu.Lock()
	h.clients[conn] = true
	initial := h.doc
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, conn)
		h.mu.Unlock()
	}()

	if err := conn.Write(ctx, websocket.MessageText, []byte(initial)); err != nil {
		return
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return // client disconnected (tab closed, network drop, ...)
		}
		h.mu.Lock()
		h.doc = string(data)
		h.mu.Unlock()
		h.broadcast(ctx, conn, data)
	}
}

// allowedOrigins reads ALLOWED_ORIGINS ("example.com,*.example.com"); none means only
// the server's own origin.
func allowedOrigins() []string {
	var out []string
	for _, p := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	h := newHub()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	mux.Handle("/", http.FileServer(http.Dir("static")))

	// Loopback by default: the demo has no sign-in, so it is not offered to the network.
	// Set ADDR (":8096") to listen on every interface.
	addr := "127.0.0.1:8096"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	log.Println("collab-editor-app listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
