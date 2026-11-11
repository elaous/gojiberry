package link

import (
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
)

// Conn is what a session needs from a hub connection.
type Conn interface {
	// Events delivers what the server says, in order.
	Events() <-chan client.Event
	// Send queues operations for delivery.
	Send(ops []crdt.Op)
	// SetPresence tells the others where this replica's cursor is.
	SetPresence(p wire.Presence)
	// Pending is how many operations the server has not accepted.
	Pending() int
	// Close ends the connection.
	Close()
}

// Transport opens connections to a hub.
type Transport interface {
	Dial(opts client.Options) Conn
}

// TransportFunc adapts a function to a Transport.
type TransportFunc func(client.Options) Conn

// Dial implements Transport.
func (f TransportFunc) Dial(opts client.Options) Conn { return f(opts) }
