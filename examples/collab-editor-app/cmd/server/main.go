package main

import (
	"compress/gzip"
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/elaous/gojiberry/wasmserve"
)

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
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := c.Write(wctx, websocket.MessageText, msg); err != nil {
			_ = c.CloseNow()
		}
		cancel()
	}
}

func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: allowedOrigins()})
	if err != nil {
		log.Println("websocket accept:", err)
		return
	}
	defer func() { _ = conn.CloseNow() }()

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
	mux.Handle("/", wasmserve.Handler(os.DirFS("static"), wasmserve.Options{GzipLevel: gzip.BestSpeed}))

	addr := "127.0.0.1:8096"
	if v := os.Getenv("ADDR"); v != "" {
		addr = v
	}
	log.Println("collab-editor-app listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
