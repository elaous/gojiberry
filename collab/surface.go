package collab

import (
	"slices"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/crdt"
)

type Surface interface {
	// Owns reports whether record id belongs to this surface.
	Owns(id string) bool
	// Read returns the surface's current records.
	Read() map[string]string
	// Write applies records received from others.
	Write(records map[string]string)
}

// Sharing says what a session shares beyond the default.
type Sharing struct {
	// NoText leaves the flow's text unshared.
	NoText bool
	// Surfaces are shared as well.
	Surfaces []Surface
}

// Configure sets what the bridge shares; call before connecting.
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

func (b *Bridge) surfaceRecords() map[string]string {
	out := map[string]string{}
	for _, sf := range b.surfaces {
		for id, v := range sf.Read() {
			out[id] = v
		}
	}
	return out
}

// an element that normalises its input must not echo it back
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
		for id, v := range recs {
			if _, ok := b.side.synced[id]; !ok {
				b.side.synced[id] = v
			}
		}
	}
	b.seen = ""
}

// SyncSurfaces shares what changed in the surfaces.
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

// PushSurfaces shares the surfaces' records the document lacks.
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
