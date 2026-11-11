package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/collab/wire"
	"github.com/elaous/gojiberry/crdt"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/observe"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Role, Presence and the role names are shared with clients.
type (
	Role     = wire.Role
	Presence = wire.Presence
)

const (
	Owner  = wire.Owner
	Editor = wire.Editor
	Viewer = wire.Viewer
)

// RoleFunc says what the caller of a request may do.
type RoleFunc func(r *http.Request, id string) Role

// Config configures a Hub.
type Config struct {
	// Store keeps documents between runs; nil keeps them in memory.
	Store docstore.Adapter
	// Roles decides each request's role.
	Roles RoleFunc
	// User names the caller for presence.
	User      func(r *http.Request) string
	SaveDelay time.Duration
	// KeepAlive is how often an idle event stream is pinged.
	KeepAlive time.Duration
	// PresenceInterval is the least time between presence updates.
	PresenceInterval time.Duration
	PresenceTTL      time.Duration
	// Guard decides who may call the hub.
	Guard   guard.Guard
	MaxBody int64
	// CookieOrigins lists the origins allowed to send cookies.
	CookieOrigins []string
	// set only if the proxy sets X-Forwarded-Host itself
	TrustForwardedHost bool
	// MaxRooms limits how many documents the hub keeps in memory.
	MaxRooms int
	// MaxPeers limits the cursors kept per document.
	MaxPeers int
	Renewer  Renewer
	// RenewInterval is the least time between two renewals.
	RenewInterval time.Duration
	// ExpiryTTL is the lifetime the store gives a document.
	ExpiryTTL time.Duration
	// AuthFunc returns the Authorization header value for renewals.
	AuthFunc         func(r *http.Request) string
	GoneConfirmDelay time.Duration
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// DefaultGoneConfirmDelay is the default GoneConfirmDelay.
const DefaultGoneConfirmDelay = 2 * time.Second

// Renewer is docstore.Renewer: what the hub calls to renew expiry.
type Renewer = docstore.Renewer

// Hub serves documents; create one with New and mount Handler.
type Hub struct {
	cfg   Config
	mu    sync.Mutex
	rooms map[string]*room
	now   func() time.Time

	closing atomic.Bool // set by Stop: no more writes or new streams

	renewMu   sync.Mutex // guards renewing; renewIdle is signalled when it reaches zero
	renewing  int        // renewals in flight
	renewIdle *sync.Cond

	renewFailures atomic.Int64
	renewDenied   atomic.Int64 // of those, answered 401 or 403
	deadRooms     atomic.Int64 // rooms closed because their document expired
	hubOnlyRooms  atomic.Int64
	revivedRooms  atomic.Int64 // closed rooms reopened because the document is live again

	stop     chan struct{} // closed by Stop
	stopOnce sync.Once

	gone map[string]time.Time
}

// New creates a hub.
func New(cfg Config) *Hub {
	if cfg.SaveDelay == 0 {
		cfg.SaveDelay = 2 * time.Second
	}
	if cfg.KeepAlive == 0 {
		cfg.KeepAlive = 15 * time.Second
	}
	if cfg.PresenceInterval == 0 {
		cfg.PresenceInterval = 100 * time.Millisecond
	}
	if cfg.PresenceTTL == 0 {
		cfg.PresenceTTL = 10 * time.Second
	}
	if cfg.MaxBody == 0 {
		cfg.MaxBody = 8 << 20
	}
	if cfg.MaxRooms == 0 {
		cfg.MaxRooms = 1000
	}
	if cfg.MaxPeers == 0 {
		cfg.MaxPeers = 256
	}
	if cfg.RenewInterval == 0 {
		cfg.RenewInterval = time.Minute
	}
	if cfg.ExpiryTTL > 0 && cfg.RenewInterval > 0 {
		if cfg.RenewInterval > cfg.ExpiryTTL {
			log.Printf("collab hub: WARNING the renew interval %s is longer than the document lifetime %s: a document would expire while it is being edited", cfg.RenewInterval, cfg.ExpiryTTL)
		}
		if max := cfg.ExpiryTTL / 4; cfg.RenewInterval > max && max > 0 {
			log.Printf("collab hub: renew interval lowered from %s to %s (a quarter of the document lifetime %s)", cfg.RenewInterval, max, cfg.ExpiryTTL)
			cfg.RenewInterval = max
		}
	}
	h := &Hub{cfg: cfg, rooms: map[string]*room{}, now: time.Now, gone: map[string]time.Time{}, stop: make(chan struct{})}
	if cfg.Now != nil {
		h.now = cfg.Now
	}
	h.renewIdle = sync.NewCond(&h.renewMu)
	return h
}

// StoreKey is the docstore key a document is saved under.
func StoreKey(id string) string { return "collab:" + id }

type event struct {
	name string
	data []byte
}

type sub struct {
	replica string
	role    Role
	ch      chan event
}

type room struct {
	id     string
	mu     sync.Mutex
	doc    *crdt.Doc
	subs   map[*sub]bool
	pres   map[string]presenceEntry
	owners map[string]string // replica id -> the signed-in user it first spoke for
	dirty  bool
	timer  *time.Timer

	lastRenew   time.Time
	renewed     bool // lastRenew is set
	renewedOnce bool
	dead        bool
	authWarned  bool
	notFoundLog bool
	failLogged  bool

	saveMu  sync.Mutex    // one save at a time, in order
	loaded  chan struct{} // closed when the stored document has been read
	loadErr error
}

type presenceEntry struct {
	p    Presence
	seen time.Time

	// a timer sends the latest cursor when the throttle interval ends
	lastSent time.Time
	timer    *time.Timer
}

// rm.mu held
func (rm *room) dropPresence(replica string) {
	if e, ok := rm.pres[replica]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(rm.pres, replica)
	}
}

var errTooManyRooms = errors.New("hub: too many documents")

var errGone = errors.New("hub: the document has expired")

const goneMsg = "this document has expired and been deleted; copy your content elsewhere"

func (h *Hub) room(r *http.Request, id string, role Role) (*room, error) {
	rm, err := h.openRoom(r, id, role)
	// a closed document is remembered: a live editor clears the mark once
	if errors.Is(err, errGone) && role.CanEdit() && h.storeTombstone(r.Context(), id) && h.revived(r, id) {
		h.clearStoreTombstone(id)
		return h.openRoom(r, id, role)
	}
	return rm, err
}

func (h *Hub) openRoom(r *http.Request, id string, role Role) (*room, error) {
	ctx := r.Context()
	h.mu.Lock()
	rm, ok := h.rooms[id]
	if !ok {
		if until, was := h.gone[id]; was {
			if h.now().Before(until) {
				h.mu.Unlock()
				if !role.CanEdit() || !h.revived(r, id) {
					return nil, errGone
				}
				h.clearStoreTombstone(id)
				h.mu.Lock()
				rm, ok = h.rooms[id]
			}
			delete(h.gone, id)
		}
	}
	if !ok {
		if len(h.rooms) >= h.cfg.MaxRooms {
			h.mu.Unlock()
			return nil, errTooManyRooms
		}
		rm = &room{id: id, doc: crdt.NewDocWithStrategy("hub", crdt.MergeCharCRDT), subs: map[*sub]bool{}, pres: map[string]presenceEntry{}, loaded: make(chan struct{})}
		h.rooms[id] = rm
		observe.Default().SetOpen("hub_rooms", len(h.rooms))
	}
	h.mu.Unlock()
	if !ok {
		// detached from the first requester: its leaving must not fail waiters
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		rm.loadErr = h.load(lctx, rm)
		cancel()
		if rm.loadErr != nil {
			h.mu.Lock()
			delete(h.rooms, id) // the next request tries again
			observe.Default().SetOpen("hub_rooms", len(h.rooms))
			h.mu.Unlock()
		}
		close(rm.loaded)
	}
	select {
	case <-rm.loaded:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if rm.loadErr != nil {
		return nil, rm.loadErr
	}
	return rm, nil
}

func (h *Hub) load(ctx context.Context, rm *room) error {
	if h.cfg.Store == nil {
		return nil
	}
	if h.storeTombstone(ctx, rm.id) {
		return errGone
	}
	stored, err := h.cfg.Store.Load(ctx, StoreKey(rm.id))
	switch {
	case err == nil:
		var ops []crdt.Op
		if err := json.Unmarshal(stored.Data, &ops); err != nil {
			return fmt.Errorf("hub: the saved document %q is unreadable: %w", rm.id, err)
		}
		rm.doc.Apply(ops...)
		return nil
	case errors.Is(err, docstore.ErrNotFound):
		return nil
	}
	return err
}

func (h *Hub) role(r *http.Request, id string) Role {
	if h.cfg.Roles == nil {
		return Owner
	}
	return h.cfg.Roles(r, id)
}

// serialised per document: snapshot taken on its turn
func (h *Hub) save(rm *room) error {
	if h.cfg.Store == nil {
		return nil
	}
	rm.saveMu.Lock()
	defer rm.saveMu.Unlock()
	rm.mu.Lock()
	if rm.dead { // An expired document is not written back.
		rm.dirty = false
		rm.mu.Unlock()
		return nil
	}
	rm.dirty = false
	data, err := json.Marshal(rm.doc.Snapshot())
	rm.mu.Unlock()
	if err == nil {
		err = h.cfg.Store.Save(context.Background(), StoreKey(rm.id), data)
	}
	if err != nil {
		rm.mu.Lock()
		h.markDirty(rm) // try again after the delay
		rm.mu.Unlock()
	}
	return err
}

// rm.mu held
func (h *Hub) markDirty(rm *room) {
	if h.cfg.Store == nil || rm.dirty || rm.dead {
		return
	}
	rm.dirty = true
	rm.timer = time.AfterFunc(h.cfg.SaveDelay, func() {
		if err := h.save(rm); err != nil {
			log.Printf("collab hub: saving %q: %v (will retry)", rm.id, err)
		}
	})
}

// Health is what GET /healthz reports.
type Health struct {
	OK            bool  `json:"ok"`
	Rooms         int   `json:"rooms"`
	RenewFailures int64 `json:"renewFailures"`
	RenewDenied   int64 `json:"renewDenied"`  // of those, refused with 401 or 403: see Config.Renewer
	ExpiredRooms  int64 `json:"expiredRooms"` // rooms closed because their document expired
	HubOnlyRooms  int64 `json:"hubOnlyRooms"`
	RevivedRooms  int64 `json:"revivedRooms"` // closed rooms reopened because the document is live again
}

// Health returns the hub's counters.
func (h *Hub) Health() Health {
	h.mu.Lock()
	n := len(h.rooms)
	h.mu.Unlock()
	return Health{OK: !h.closing.Load(), Rooms: n, RenewFailures: h.renewFailures.Load(), RenewDenied: h.renewDenied.Load(), ExpiredRooms: h.deadRooms.Load(), HubOnlyRooms: h.hubOnlyRooms.Load(), RevivedRooms: h.revivedRooms.Load()}
}

// Flush saves every document with unsaved changes now.
func (h *Hub) Flush() error {
	defer h.waitRenewals()
	h.mu.Lock()
	rooms := make([]*room, 0, len(h.rooms))
	for _, rm := range h.rooms {
		rooms = append(rooms, rm)
	}
	h.mu.Unlock()
	var errs []error
	for _, rm := range rooms {
		rm.saveMu.Lock()
		rm.saveMu.Unlock() //nolint:staticcheck // waiting for the holder is the point
		rm.mu.Lock()
		dirty := rm.dirty
		if rm.timer != nil {
			rm.timer.Stop()
		}
		rm.mu.Unlock()
		if dirty {
			errs = append(errs, h.save(rm))
		}
	}
	return errors.Join(errs...)
}

// Stop refuses further writes and new event streams.
func (h *Hub) Stop() {
	h.closing.Store(true)
	h.stopOnce.Do(func() { close(h.stop) })
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rm := range h.rooms {
		rm.mu.Lock()
		for s := range rm.subs {
			close(s.ch)
			delete(rm.subs, s)
		}
		rm.mu.Unlock()
	}
}

// Close saves everything and drops the event streams.
func (h *Hub) Close() error {
	err := h.Flush()
	h.mu.Lock()
	for _, rm := range h.rooms {
		rm.mu.Lock()
		for s := range rm.subs {
			close(s.ch)
			delete(rm.subs, s)
		}
		rm.mu.Unlock()
	}
	h.mu.Unlock()
	return err
}
