//go:build !js

package ws

import (
	"net/http"
	"net/url"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/client"
)

func dialOptions(opts client.Options) *websocket.DialOptions {
	return &websocket.DialOptions{HTTPHeader: opts.Header.Clone(), HTTPClient: opts.Client}
}

func addCredentials(url.Values, http.Header) {}
