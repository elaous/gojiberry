// Package wire holds the types a collab server and its clients share, so a
// WASM client need not import the server's dependencies.
package wire

import (
	"encoding/json"

	"github.com/elaous/gojiberry/crdt"
)

// Role is what a person may do with a document. The names match
// canvas.Role and docstore's access levels.
type Role string

const (
	Owner  Role = "owner"
	Editor Role = "editor"
	Viewer Role = "viewer"
)

// CanRead reports whether the role may see the document at all (the empty
// role may not).
func (r Role) CanRead() bool { return r == Owner || r == Editor || r == Viewer }

// CanEdit reports whether the role may change the text and everything else.
func (r Role) CanEdit() bool { return r == Owner || r == Editor }

// CanComment reports whether the role may add comments and replies: every
// role that can read.
func (r Role) CanComment() bool { return r.CanRead() }

// Presence is one replica's cursor and who it belongs to. Positions are
// character ids (crdt.Timestamp), the character just before the position, so they
// stay right as the text changes; the zero id means the very start of the text.
// Has says whether Anchor and Caret mean anything. The server sets User and
// Role; the rest comes from the client.
type Presence struct {
	Replica string         `json:"replica"`
	Name    string         `json:"name,omitempty"`
	Color   string         `json:"color,omitempty"`
	User    string         `json:"user,omitempty"`
	Role    Role           `json:"role,omitempty"`
	Has     bool           `json:"has,omitempty"`
	Anchor  crdt.Timestamp `json:"anchor"`
	Caret   crdt.Timestamp `json:"caret"`
	// Gone is set on the event sent when a replica leaves.
	Gone bool `json:"gone,omitempty"`
}

// Frame is one WebSocket message of the collab protocol (the transport in
// collab/ws and the hub's /docs/{id}/ws endpoint). It mirrors the HTTP
// transport's server-sent events and posts, in one stream.
//
// From the server: Event is "hello", "snapshot", "ops" or "presence" with the
// same Data the SSE events carry, and "ack" or "reject" answering a client
// "ops" frame by its ID (Error says why a rejection happened). From the
// client: "ops" (with an ID, Data the operations) and "presence" (Data the
// cursor).
type Frame struct {
	Event string          `json:"event"`
	ID    uint64          `json:"id,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}
