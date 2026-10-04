package collab

import (
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/link"
)

// Conn is what a Session needs from a connection to a hub (see package link).
type Conn = link.Conn

// Transport opens connections: the adapter that decides how a session talks
// to the hub -- HTTPEndpoints, collab/ws's WebSockets, or your own.
type Transport = link.Transport

// TransportFunc adapts a function to a Transport.
type TransportFunc = link.TransportFunc

var _ Conn = (*client.Conn)(nil)

// HTTPEndpoints is the default transport: a Server-Sent Events stream for
// what the server says, and POSTs for what the client sends. It works through
// any HTTP proxy and carries headers (Authorization, a guard token) like any
// request.
func HTTPEndpoints() Transport {
	return TransportFunc(func(opts client.Options) Conn { return client.Dial(opts) })
}
