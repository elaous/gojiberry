//go:build !js

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// Only a page served by this server may open the socket: another site in the same
// browser must not be able to read or overwrite the document through localhost.
func TestOnlyThisServersPagesMayOpenTheSocket(t *testing.T) {
	h := newHub()
	srv := httptest.NewServer(http.HandlerFunc(h.handleWS))
	defer srv.Close()
	dial := func(origin string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hdr := http.Header{}
		if origin != "" {
			hdr.Set("Origin", origin)
		}
		c, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], &websocket.DialOptions{HTTPHeader: hdr})
		if err == nil {
			_ = c.CloseNow()
		}
		return err
	}
	if err := dial(srv.URL); err != nil {
		t.Errorf("this server's own page: %v", err)
	}
	if err := dial("https://evil.example"); err == nil {
		t.Error("another site's page opened the socket")
	}
	t.Setenv("ALLOWED_ORIGINS", "friend.example")
	if err := dial("https://friend.example"); err != nil {
		t.Errorf("an origin that is allowed on purpose: %v", err)
	}
}
