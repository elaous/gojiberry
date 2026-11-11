package collab

import (
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/collab/link"
)

// Conn is what a Session needs from a hub connection.
type Conn = link.Conn

// Transport opens connections to a hub.
type Transport = link.Transport

// TransportFunc adapts a function to a Transport.
type TransportFunc = link.TransportFunc

var _ Conn = (*client.Conn)(nil)

// HTTPEndpoints is the default transport, using Server-Sent Events.
func HTTPEndpoints() Transport {
	return TransportFunc(func(opts client.Options) Conn { return client.Dial(opts) })
}
