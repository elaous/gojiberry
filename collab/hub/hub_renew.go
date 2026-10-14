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

// goneMemory is how long the hub refuses a document whose room it closed as expired, so a stale
// tab that reconnects does not start a new room and write the content back.
const goneMemory = time.Hour

// maxGone bounds the table of closed documents.
const maxGone = 4096

// goneKey is the docstore key of the mark that outlives the in-process table: a document whose
// room was closed as expired stays refused for goneMemory after a restart, after the table evicted
// it at maxGone, and on the other replicas of a shared store. The value is {"until": unix seconds}.
// A mark nobody asks about again is left behind (about 40 bytes); it is deleted when it is asked
// about after it lapsed.
func goneKey(id string) string { return "collab-gone:" + id }

// storeTombstone says whether the store holds a mark that has not lapsed. A lapsed one is deleted.
// A store that cannot be read answers false: the mark is a second line of defence.
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

// renewDue says whether an accepted edit should now renew the document's expiry, and if so
// starts the interval (rm.mu held).
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

// renew calls the Renewer for the room's document in the background; r is the request whose
// edit caused it (its Authorization, or AuthFunc's answer for it, authorizes the renewal).
// A panicking Renewer or AuthFunc is logged and recovered: it must not take the process down.
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
			// One "gone" is not enough to kill a live room: a deploy, a failover or a
			// recreated document can answer it once. Ask again after a short delay; only a
			// second "gone" closes the room.
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

// renewCtx is the context of a renewal for the edit r: a deadline and the editor's
// Authorization (AuthFunc's answer for it, if set).
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

// confirmWait waits GoneConfirmDelay; it is false if the hub is stopping meanwhile.
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

// revived reports whether the document of a closed room is live in the store again, which
// clears its tombstone: the Renewer, called with the editor's own credentials, succeeds only for
// a live document it may write (a deleted and re-created document, a late renewal).
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

// renewResult acts on the outcome of one renewal.
//
// ErrNotFound after an earlier success means the document expired and was swept while this room
// stayed open in a stale tab: the room is closed (see closeDead). Before any success it means
// nothing (a document that lives only in the hub was never saved to the store, so it is always
// not found), and is only logged. A 401 or 403 is the store refusing this editor's credentials,
// most often because the hub's role rule and the docstore's access rule disagree (see
// Config.Renewer): it is counted and logged as a warning once per document.
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
		// A document that lives only in the hub: always "not found". Said once per room and
		// counted once, then silence for as long as it is edited.
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

// closeDead retires a room whose document expired in the store: it rejects further operations
// with 410 Gone, stops saving, deletes the hub state "collab:<id>" (a save may have written it
// after the sweeper removed it), ends its streams and removes it from the hub; the id is then
// refused with 410 for a while (goneMemory) so a stale tab that reconnects cannot start a new
// room that writes the content back.
func (h *Hub) closeDead(rm *room) {
	// Wait for a save in flight, so that the delete below is the last write.
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

// waitRenewals blocks until the renewals in flight have finished.
func (h *Hub) waitRenewals() {
	h.renewMu.Lock()
	for h.renewing > 0 {
		h.renewIdle.Wait()
	}
	h.renewMu.Unlock()
}
