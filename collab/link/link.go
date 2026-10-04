// Package link holds the two small interfaces between a collab session and
// the connection it talks over, so that transports (the HTTP one in
// collab/client, the WebSocket one in collab/ws, your own) and the session
// need not import each other. collab re-exports them as collab.Conn,
// collab.Transport and collab.TransportFunc.
package link

import (
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// Conn is what a session needs from a connection to a hub: the events from
// the server in order, a way to send operations (kept until the server
// accepts them) and the cursor, and a count of what is still waiting.
type Conn interface {
	// Events delivers what the server says, in order.
	Events() <-chan client.Event
	// Send queues operations for delivery; they are kept until accepted.
	Send(ops []crdt.Op)
	// SetPresence tells the others where this replica's cursor is.
	SetPresence(p wire.Presence)
	// Pending is how many operations the server has not yet accepted.
	Pending() int
	// Close ends the connection.
	Close()
}

// Transport opens connections: the adapter that decides how a session talks
// to the hub. The options say where (BaseURL, Doc), as whom (Replica, Header)
// and how to retry; a Transport uses what it needs.
type Transport interface {
	Dial(opts client.Options) Conn
}

// TransportFunc adapts a function to a Transport.
type TransportFunc func(client.Options) Conn

// Dial implements Transport.
func (f TransportFunc) Dial(opts client.Options) Conn { return f(opts) }
