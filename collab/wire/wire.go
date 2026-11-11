// Package wire holds the types shared by collab servers and clients.
package wire

import (
	"encoding/json"

	"github.com/elaous/gojiberry/crdt"
)

// Role is what a person may do with a document.
type Role string

const (
	Owner  Role = "owner"
	Editor Role = "editor"
	Viewer Role = "viewer"
)

// CanRead reports whether the role may see the document.
func (r Role) CanRead() bool { return r == Owner || r == Editor || r == Viewer }

// CanEdit reports whether the role may change the text.
func (r Role) CanEdit() bool { return r == Owner || r == Editor }

// CanComment reports whether the role may comment.
func (r Role) CanComment() bool { return r.CanRead() }

// Presence is one replica's cursor and its owner.
type Presence struct {
	Replica string         `json:"replica"`
	Name    string         `json:"name,omitempty"`
	Color   string         `json:"color,omitempty"`
	User    string         `json:"user,omitempty"`
	Role    Role           `json:"role,omitempty"`
	Has     bool           `json:"has,omitempty"`
	Anchor  crdt.Timestamp `json:"anchor"`
	Caret   crdt.Timestamp `json:"caret"`
	// Holding names the object this replica is changing.
	Holding string `json:"holding,omitempty"`
	// Gone is set on the event sent when a replica leaves.
	Gone bool `json:"gone,omitempty"`
}

// Frame is one WebSocket message of the collab protocol.
type Frame struct {
	Event string          `json:"event"`
	ID    uint64          `json:"id,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}
