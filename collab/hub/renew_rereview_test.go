//go:build !(js && wasm)

package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// editOK posts one accepted edit and waits for the renewal it starts.
func editOK(t *testing.T, h *Hub, e edit, id, op string) {
	t.Helper()
	if s := e.post(t, id, "editor", "", "r", textOps(t, op, "x")); s != http.StatusNoContent {
		t.Fatalf("edit %s: %d", op, s)
	}
	h.waitRenewals()
}

// --- finding 1: a transient or foreign 404 never kills a live room

// A reverse proxy that answers a plain 404 (no marker) in front of the docstore after one good
// renewal: the room stays open, its state is kept, nothing answers 410.
func TestProxy404WithoutMarkerKeepsTheRoomAlive(t *testing.T) {
	var proxyDown atomic.Bool
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proxyDown.Load() {
			http.NotFound(w, r) // the proxy's own 404: no marker
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ds.Close()
	store := docstore.NewMemoryAdapter()
	h, e := newRenewHub(t, Config{Store: store, Renewer: docstore.NewHTTPAdapter(ds.URL, nil), RenewInterval: -1})
	editOK(t, h, e, "doc", "c1") // one success
	proxyDown.Store(true)
	for i := 0; i < 4; i++ {
		editOK(t, h, e, "doc", "p"+string(rune('a'+i)))
	}
	_ = h.Flush()
	if !exists(t, store, StoreKey("doc")) {
		t.Fatal("the collaborative state was deleted by a proxy 404")
	}
	if got := h.Health(); got.ExpiredRooms != 0 || got.RenewFailures != 4 {
		t.Fatalf("health = %+v, want no closed room and 4 failed renewals", got)
	}
	if s := e.post(t, "doc", "editor", "", "r", textOps(t, "after", "x")); s != http.StatusNoContent {
		t.Fatalf("edit after the proxy 404s: %d (the room was closed)", s)
	}
}

// A docstore older than the renew route (every /renew is a plain 404) never closes a room, even
// though an earlier call (a success from a newer instance during a rolling deploy) came first.
func TestOlderDocstoreWithoutRenewRouteNeverClosesARoom(t *testing.T) {
	var calls atomic.Int32
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusNoContent) // a new instance answered once
			return
		}
		http.Error(w, "404 page not found", http.StatusNotFound) // an old one: no such route
	}))
	defer ds.Close()
	store := docstore.NewMemoryAdapter()
	h, e := newRenewHub(t, Config{Store: store, Renewer: docstore.NewHTTPAdapter(ds.URL, nil), RenewInterval: -1})
	for i := 0; i < 5; i++ {
		editOK(t, h, e, "doc", "o"+string(rune('a'+i)))
	}
	_ = h.Flush()
	if got := h.Health(); got.ExpiredRooms != 0 {
		t.Fatalf("an older docstore closed a room: %+v", got)
	}
	if !exists(t, store, StoreKey("doc")) {
		t.Fatal("state deleted")
	}
}

// A genuine "gone" (the docstore's marked 404) closes the room, but only after a second "gone".
func TestGenuineGoneClosesTheRoomOnlyAfterConfirmation(t *testing.T) {
	var calls atomic.Int32
	h, e := newRenewHub(t, Config{RenewInterval: -1, GoneConfirmDelay: 20 * time.Millisecond, Renewer: rf(func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return nil
		}
		return docstore.ErrNotFound
	})})
	editOK(t, h, e, "d", "c1") // success
	start := time.Now()
	editOK(t, h, e, "d", "c2") // gone, confirmed after the delay, gone again
	if n := calls.Load(); n != 3 {
		t.Fatalf("%d renewals, want 3 (success, gone, confirming gone)", n)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("the confirming check did not wait")
	}
	if s := e.post(t, "d", "editor", "", "r", textOps(t, "c3", "x")); s != http.StatusGone {
		t.Fatalf("edit of a confirmed-gone room: %d, want 410", s)
	}
}

// One "gone" followed by a success (a failover, a recreated document) leaves the room open.
func TestSingleGoneAnswerDoesNotCloseTheRoom(t *testing.T) {
	var calls atomic.Int32
	store := docstore.NewMemoryAdapter()
	h, e := newRenewHub(t, Config{Store: store, RenewInterval: -1, Renewer: rf(func(context.Context, string) error {
		if calls.Add(1) == 2 {
			return docstore.ErrNotFound // exactly one transient "gone"
		}
		return nil
	})})
	editOK(t, h, e, "d", "c1")
	editOK(t, h, e, "d", "c2") // gone, then the confirming check succeeds
	_ = h.Flush()
	if got := h.Health().ExpiredRooms; got != 0 {
		t.Fatalf("expiredRooms = %d", got)
	}
	if s := e.post(t, "d", "editor", "", "r", textOps(t, "c3", "x")); s != http.StatusNoContent {
		t.Fatalf("edit: %d", s)
	}
	if !exists(t, store, StoreKey("d")) {
		t.Fatal("state deleted")
	}
}

// --- finding 2: a tombstone does not outlive the document's return

func TestRecreatedDocumentClearsTheTombstone(t *testing.T) {
	ctx := context.Background()
	clk := newClock()
	ea, _ := newExpiringStore(clk)
	h, e := newRenewHub(t, Config{Store: ea, Renewer: ea, RenewInterval: -1, Now: clk.Now})
	_ = ea.Save(ctx, "doc", []byte("v1"))
	editOK(t, h, e, "doc", "c1")
	// The document is deleted (not merely expired) while the room is open.
	if err := ea.Delete(ctx, "doc"); err != nil {
		t.Fatal(err)
	}
	editOK(t, h, e, "doc", "c2") // gone (twice): the room is closed
	if s := e.post(t, "doc", "editor", "", "r", textOps(t, "c3", "x")); s != http.StatusGone {
		t.Fatalf("closed room: %d, want 410", s)
	}
	// Still deleted: still 410, for a reader too.
	if s := e.get(t, "/docs/doc/snapshot", "viewer"); s != http.StatusGone {
		t.Fatalf("snapshot of the dead document: %d", s)
	}
	// Recreated under the same id: an editor can use it at once.
	_ = ea.Save(ctx, "doc", []byte("v2"))
	if s := e.get(t, "/docs/doc/snapshot", "viewer"); s != http.StatusGone {
		t.Fatalf("a viewer cannot clear the tombstone (it cannot renew): %d", s)
	}
	if s := e.post(t, "doc", "editor", "", "r", textOps(t, "c4", "back")); s != http.StatusNoContent {
		t.Fatalf("edit of the recreated document: %d, want 204", s)
	}
	h.waitRenewals()
	if s := e.get(t, "/docs/doc/snapshot", "viewer"); s != http.StatusOK {
		t.Fatalf("snapshot after the revival: %d", s)
	}
	if got := h.Health(); got.RevivedRooms != 1 || got.ExpiredRooms != 1 {
		t.Fatalf("health = %+v", got)
	}
}

// --- finding 4: a hub-only document is logged once per room

func TestHubOnlyDocumentIsLoggedOncePerRoom(t *testing.T) {
	lb := captureLog(t)
	h, e := newRenewHub(t, Config{RenewInterval: -1, Renewer: rf(func(context.Context, string) error { return docstore.ErrNotFound })})
	for i := 0; i < 6; i++ {
		editOK(t, h, e, "solo", "s"+string(rune('a'+i)))
	}
	if n := countLines(lb.String(), `"solo" is not in the store`); n != 1 {
		t.Fatalf("%d log lines for one hub-only room, want 1:\n%s", n, lb.String())
	}
	editOK(t, h, e, "solo2", "x1")
	editOK(t, h, e, "solo2", "x2")
	if n := countLines(lb.String(), "is not in the store"); n != 2 {
		t.Fatalf("%d lines for two rooms, want 2:\n%s", n, lb.String())
	}
	got := h.Health()
	if got.HubOnlyRooms != 2 || got.RenewFailures != 0 || got.ExpiredRooms != 0 {
		t.Fatalf("health = %+v", got)
	}
	if strings.Contains(lb.String(), "closing it") {
		t.Fatal("a hub-only room was closed")
	}
}

// --- public sessions never reach a public document through the hub

// The hub refuses a document id with a slash, so a room can never be named "pubdoc/<id>" and a
// hub edit can never renew a public document; and a public session token is no sign-in, so on a
// document with an access list it is nobody.
func TestHubCannotAddressPublicDocuments(t *testing.T) {
	var renewed atomic.Int32
	h, e := newRenewHub(t, Config{RenewInterval: -1, Renewer: rf(func(context.Context, string) error { renewed.Add(1); return nil })})
	for _, id := range []string{"pubdoc%2Fabc", "pubdoc/abc", "public%2Fdoc%2Fabc"} {
		req, _ := http.NewRequest("POST", e.srv.URL+"/docs/"+id+"/ops?replica=r", strings.NewReader(textOps(t, "c", "x")))
		req.Header.Set("X-Role", "owner")
		req.Header.Set("Authorization", "Bearer public-session-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			t.Fatalf("the hub accepted an edit of %q", id)
		}
	}
	h.waitRenewals()
	if renewed.Load() != 0 {
		t.Fatal("a renewal was made for a slash id")
	}
}

// The mark of a closed document is in the store: a new process (a restart, another replica) refuses
// the id too, and an editor of a document that is live again clears it.
func TestClosedDocumentStaysRefusedAcrossARestart(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	var calls atomic.Int32
	h1, e1 := newRenewHub(t, Config{Store: store, RenewInterval: -1, Renewer: rf(func(context.Context, string) error {
		if calls.Add(1) == 1 {
			return nil
		}
		return docstore.ErrNotFound
	})})
	editOK(t, h1, e1, "d", "c1")
	editOK(t, h1, e1, "d", "c2") // gone, confirmed: the room is closed
	if !exists(t, store, goneKey("d")) {
		t.Fatal("no mark in the store")
	}

	live := false
	_, e2 := newRenewHub(t, Config{Store: store, RenewInterval: -1, Renewer: rf(func(context.Context, string) error {
		if live {
			return nil
		}
		return docstore.ErrNotFound
	})})
	if s := e2.post(t, "d", "editor", "", "r", textOps(t, "c3", "x")); s != http.StatusGone {
		t.Fatalf("a new process took the closed document: %d, want 410", s)
	}
	if s := e2.get(t, "/docs/d/snapshot", "viewer"); s != http.StatusGone {
		t.Fatalf("a viewer of the closed document: %d, want 410", s)
	}
	live = true
	if s := e2.post(t, "d", "editor", "", "r", textOps(t, "c4", "y")); s >= 300 {
		t.Fatalf("an editor of a document that is live again: %d", s)
	}
	if exists(t, store, goneKey("d")) {
		t.Error("the mark was not cleared")
	}
}
