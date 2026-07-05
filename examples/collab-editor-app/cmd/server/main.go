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
	"sync"

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
		_ = c.Write(ctx, websocket.MessageText, msg)
	}
}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// This demo's own static client is the only intended origin,
		// but there's no real deployment/origin to lock this down to
		// here, so accept any -- a real product would restrict this.
		OriginPatterns: []string{"*"},
	})
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

func main() {
	h := newHub()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.handleWS)
	mux.Handle("/", http.FileServer(http.Dir("static")))

	addr := ":8096"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	log.Println("collab-editor-app listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
