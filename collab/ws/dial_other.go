//go:build !js

package ws

import (
	"net/http"
	"net/url"

	"github.com/coder/websocket"

	"github.com/elaous/gojiberry/collab/client"
)

// dialOptions sends the options' headers with the upgrade request.
func dialOptions(opts client.Options) *websocket.DialOptions {
	return &websocket.DialOptions{HTTPHeader: opts.Header.Clone(), HTTPClient: opts.Client}
}

// addCredentials adds nothing outside a browser.
func addCredentials(url.Values, http.Header) {}
