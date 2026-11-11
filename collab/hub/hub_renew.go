package hub

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/observe"
	"log"
	"net/http"
	"time"
)

// a stale tab reconnecting must not start a new room
const goneMemory = time.Hour

const maxGone = 4096

func goneKey(id string) string { return "collab-gone:" + id }

func (h *Hub) storeTombstone(ctx context.Context, id string) bool {
	if h.cfg.Store == nil {
		return false
	}
	doc, err := h.cfg.Store.Load(ctx, goneKey(id))
	if err != nil {
		return false
	}
	var m struct {
		Until int64 `json:"until"`
	}
	if json.Unmarshal(doc.Data, &m) == nil && h.now().Unix() < m.Until {
		return true
	}
	_ = h.cfg.Store.Delete(context.WithoutCancel(ctx), goneKey(id))
	return false
}

func (h *Hub) clearStoreTombstone(id string) {
	if h.cfg.Store == nil {
		return
	}
	if err := h.cfg.Store.Delete(context.Background(), goneKey(id)); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		log.Printf("collab hub: clearing the mark of %q: %v", id, err)
	}
}

// rm.mu held
func (h *Hub) renewDue(rm *room) bool {
	if h.cfg.Renewer == nil {
		return false
	}
	now := h.now()
	if rm.renewed && h.cfg.RenewInterval > 0 && now.Sub(rm.lastRenew) < h.cfg.RenewInterval && !now.Before(rm.lastRenew) {
		return false
	}
	rm.lastRenew, rm.renewed = now, true
	return true
}

func (h *Hub) renew(rm *room, r *http.Request) {
	h.renewMu.Lock()
	h.renewing++
	h.renewMu.Unlock()
	go func() {
		defer func() {
			h.renewMu.Lock()
			h.renewing--
			if h.renewing == 0 {
				h.renewIdle.Broadcast()
			}
			h.renewMu.Unlock()
		}()
		defer func() {
			if p := recover(); p != nil {
				h.renewFailures.Add(1)
				log.Printf("collab hub: renewing the expiry of %q panicked: %v", rm.id, p)
			}
		}()
		ctx, cancel := h.renewCtx(r)
		defer cancel()
		err := h.cfg.Renewer.Renew(ctx, rm.id)
		if errors.Is(err, docstore.ErrNotFound) && h.hadSuccess(rm) {
			// one "gone" does not kill a live room: confirm after a delay
			if !h.confirmWait() {
				return
			}
			ctx2, cancel2 := h.renewCtx(r)
			defer cancel2()
			if err = h.cfg.Renewer.Renew(ctx2, rm.id); err == nil {
				log.Printf("collab hub: %q was reported gone once by the store and is live again: the room stays open", rm.id)
			}
		}
		h.renewResult(rm, err)
	}()
}

func (h *Hub) renewCtx(r *http.Request) (context.Context, context.CancelFunc) {
	auth := r.Header.Get("Authorization")
	if h.cfg.AuthFunc != nil {
		auth = h.cfg.AuthFunc(r)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if auth != "" {
		ctx = docstore.WithAuthorization(ctx, auth)
	}
	return ctx, cancel
}

func (h *Hub) hadSuccess(rm *room) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.renewedOnce
}

func (h *Hub) confirmWait() bool {
	d := h.cfg.GoneConfirmDelay
	if d == 0 {
		d = DefaultGoneConfirmDelay
	}
	if d < 0 {
		return !h.closing.Load()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-h.stop:
		return false
	}
}

func (h *Hub) revived(r *http.Request, id string) bool {
	if h.cfg.Renewer == nil || h.closing.Load() {
		return false
	}
	ctx, cancel := h.renewCtx(r)
	defer cancel()
	ok := func() (ok bool) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("collab hub: checking whether %q is live again panicked: %v", id, p)
			}
		}()
		return h.cfg.Renewer.Renew(ctx, id) == nil
	}()
	if ok {
		h.revivedRooms.Add(1)
		log.Printf("collab hub: %q is live in the store again: its room may be reopened", id)
	}
	return ok
}

func (h *Hub) renewResult(rm *room, err error) {
	switch {
	case err == nil:
		rm.mu.Lock()
		rm.renewedOnce = true
		rm.mu.Unlock()
	case errors.Is(err, docstore.ErrNotFound):
		rm.mu.Lock()
		was := rm.renewedOnce
		rm.mu.Unlock()
		if was {
			log.Printf("collab hub: %q expired in the store while it was open (confirmed twice): closing it", rm.id)
			h.closeDead(rm)
			return
		}
		rm.mu.Lock()
		first := !rm.notFoundLog
		rm.notFoundLog = true
		rm.mu.Unlock()
		if first {
			h.hubOnlyRooms.Add(1)
			log.Printf("collab hub: %q is not in the store (it lives only in the hub): nothing to renew; said once for this room (Health.hubOnlyRooms)", rm.id)
		}
	default:
		h.renewFailures.Add(1)
		var se *docstore.StatusError
		if errors.As(err, &se) && (se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden) {
			h.renewDenied.Add(1)
			rm.mu.Lock()
			warned := rm.authWarned
			rm.authWarned = true
			rm.mu.Unlock()
			if !warned {
				log.Printf("collab hub: WARNING the store refused to renew %q (%d): the editor's credentials were rejected, or the store requires an access list on this document while the hub's roles allow editing without one (GOJIBERRY_COLLAB_NO_ACL_ROLE=owner/editor against GOJIBERRY_DOCSTORE_REQUIRE_ACL=true: the two settings must agree). Edits will not keep the document alive and it will expire. Further refusals are counted in /healthz renewFailures only.", rm.id, se.Status)
			}
			return
		}
		rm.mu.Lock()
		first := !rm.failLogged
		rm.failLogged = true
		rm.mu.Unlock()
		if first || !errors.Is(err, docstore.ErrRenewUnsupported) {
			log.Printf("collab hub: renewing the expiry of %q: %v", rm.id, err)
		}
	}
}

func (h *Hub) closeDead(rm *room) {
	// wait for a save in flight: the delete below is the last write
	rm.saveMu.Lock()
	defer rm.saveMu.Unlock()
	rm.mu.Lock()
	if rm.dead {
		rm.mu.Unlock()
		return
	}
	rm.dead, rm.dirty = true, false
	if rm.timer != nil {
		rm.timer.Stop()
	}
	for s := range rm.subs {
		close(s.ch)
		delete(rm.subs, s)
	}
	for id := range rm.pres {
		rm.dropPresence(id)
	}
	rm.mu.Unlock()
	h.deadRooms.Add(1)

	h.mu.Lock()
	if h.rooms[rm.id] == rm {
		delete(h.rooms, rm.id)
		observe.Default().SetOpen("hub_rooms", len(h.rooms))
	}
	if len(h.gone) >= maxGone {
		now := h.now()
		for id, until := range h.gone {
			if !now.Before(until) {
				delete(h.gone, id)
			}
		}
		for id := range h.gone { // still full: drop an arbitrary one
			if len(h.gone) < maxGone {
				break
			}
			delete(h.gone, id)
		}
	}
	h.gone[rm.id] = h.now().Add(goneMemory)
	h.mu.Unlock()
	if h.cfg.Store != nil {
		mark, _ := json.Marshal(map[string]int64{"until": h.now().Add(goneMemory).Unix()})
		if err := h.cfg.Store.Save(context.Background(), goneKey(rm.id), mark); err != nil {
			log.Printf("collab hub: saving the mark of the expired %q: %v", rm.id, err)
		}
	}

	if h.cfg.Store != nil {
		if err := h.cfg.Store.Delete(context.Background(), StoreKey(rm.id)); err != nil && !errors.Is(err, docstore.ErrNotFound) {
			log.Printf("collab hub: deleting the state of the expired %q: %v", rm.id, err)
		}
	}
}

func (h *Hub) isDead(rm *room) bool {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.dead
}

func (h *Hub) waitRenewals() {
	h.renewMu.Lock()
	for h.renewing > 0 {
		h.renewIdle.Wait()
	}
	h.renewMu.Unlock()
}
