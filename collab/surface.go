package collab

import (
	"slices"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/crdt"
)

// A Surface is a part of the canvas other than the text -- the pages'
// headers and footers, floating text, tables and images -- shared as small
// whole-value records, one per element, last writer wins.
//
// Surfaces are polled, not subscribed to: the session asks Read for the
// current records now and then (see Bridge.SyncSurfaces), and sends the ones
// that changed once they have held still for a moment, so dragging an object
// shares where it was dropped, not every step on the way.
type Surface interface {
	// Owns reports whether the record id belongs to this surface.
	Owns(id string) bool
	// Read returns the records for the surface's current state.
	Read() map[string]string
	// Write applies records received from others (only this surface's).
	// Records for elements that do not exist here are ignored, and elements
	// without a record keep their state.
	Write(records map[string]string)
}

// Sharing says what a session shares beyond the default (the flow's text,
// its comments, notes and styles).
type Sharing struct {
	// NoText leaves the flow's text, comments, notes and styles alone: only
	// Surfaces are shared.
	NoText bool
	// Surfaces are shared as well.
	Surfaces []Surface
}

// Configure sets what the bridge shares. Call it before Seed or the first
// Apply.
func (b *Bridge) Configure(sh Sharing) {
	b.surfaces = slices.Clone(sh.Surfaces)
	b.noText = sh.NoText
	if b.noText {
		b.Flow.UndoHook = nil
	}
}

func (b *Bridge) surfaceOwns(id string) bool {
	for _, sf := range b.surfaces {
		if sf.Owns(id) {
			return true
		}
	}
	return false
}

// surfaceRecords is every record the surfaces hold now.
func (b *Bridge) surfaceRecords() map[string]string {
	out := map[string]string{}
	for _, sf := range b.surfaces {
		for id, v := range sf.Read() {
			out[id] = v
		}
	}
	return out
}

// pullSurfaces hands each surface the replica's records for it, then takes
// what it reads back as agreed: an element that normalises what it was
// given must not send the normalised form back and start an echo.
func (b *Bridge) pullSurfaces() {
	if len(b.surfaces) == 0 {
		return
	}
	if b.side.synced == nil {
		b.side.synced = map[string]string{}
	}
	for _, sf := range b.surfaces {
		recs := map[string]string{}
		for _, blk := range b.Doc.Blocks() {
			if crdt.LWWKind(blk.Kind) && sf.Owns(blk.ID) {
				recs[blk.ID] = blk.Content
			}
		}
		if len(recs) > 0 {
			sf.Write(recs)
		}
		for id, v := range sf.Read() {
			b.side.synced[id] = v
		}
		for id, v := range recs { // records for elements missing here stay as they are
			if _, ok := b.side.synced[id]; !ok {
				b.side.synced[id] = v
			}
		}
	}
	b.seen = ""
}

// SyncSurfaces shares what changed in the surfaces, once it has held still
// for two calls; call it at the polling interval. It reports nothing and
// does nothing when there are no surfaces or the bridge is not ready.
func (b *Bridge) SyncSurfaces() {
	if len(b.surfaces) == 0 || !b.ready || b.detached || b.applying {
		return
	}
	want := b.surfaceRecords()
	sig := signature(want)
	changed := false
	for id, v := range want {
		if b.side.synced[id] != v {
			changed = true
			break
		}
	}
	if !changed {
		b.seen = ""
		return
	}
	if sig != b.seen {
		b.seen = sig
		return
	}
	b.seen = ""
	b.pushSurfaces(want)
}

// PushSurfaces shares the surfaces' records the document does not have, or
// has differently, at once: for a replica that has just received the
// document and may edit.
func (b *Bridge) PushSurfaces() {
	if len(b.surfaces) == 0 || !b.ready || b.detached {
		return
	}
	b.pushSurfaces(b.surfaceRecords())
}

func (b *Bridge) pushSurfaces(want map[string]string) {
	if b.side.synced == nil {
		b.side.synced = map[string]string{}
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if b.side.synced[id] != want[id] {
			b.Doc.Put(crdt.Block{ID: id, Kind: sideKind, Content: want[id]})
			b.side.synced[id] = want[id]
		}
	}
	b.flush()
}

func signature(m map[string]string) string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var sb strings.Builder
	for _, id := range ids {
		sb.WriteString(id)
		sb.WriteByte(0)
		sb.WriteString(m[id])
		sb.WriteByte(1)
	}
	return sb.String()
}
