//go:build !(js && wasm)

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/collab/hub"
	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestLiveEndToEnd opens the showcase in several browsers on one shared
// document served by a collab hub: shared typing, cursors, per-user undo, and
// a viewer who can comment but not edit. The page takes its role from the
// bearer token it sends, which this test's hub reads as the role name. It
// runs over both transports (?transport=http|ws).
func TestLiveEndToEnd(t *testing.T) {
	// The WebSocket run shares everything (?flow=*), the HTTP one the text only.
	for _, c := range []struct{ transport, flow string }{{"http", ""}, {"ws", "*"}} {
		transport, flow := c.transport, c.flow
		t.Run(transport, func(t *testing.T) {
			h := hub.New(hub.Config{KeepAlive: 500 * time.Millisecond, Roles: func(r *http.Request, _ string) hub.Role {
				return hub.Role(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			}})
			srv := httptest.NewServer(h.Handler())
			defer srv.Close()
			defer h.Close()
			e2etest.RunEnv(t, "e2e/live.js", "HUB_URL="+srv.URL, "TRANSPORT="+transport, "FLOW="+flow)
		})
	}
}
