//go:build js

package ws

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/client"
)

// dialOptions: a browser WebSocket takes no headers.
func dialOptions(client.Options) *websocket.DialOptions { return nil }

// addCredentials puts the tokens in the query string.
func addCredentials(q url.Values, h http.Header) {
	if tok, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer "); ok && tok != "" {
		q.Set("access_token", tok)
	}
	if tok := h.Get("X-Gojiberry-Token"); tok != "" {
		q.Set("guard_token", tok)
	}
}
