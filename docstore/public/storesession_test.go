package public

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

var t0 = time.Date(2030, 1, 1, 10, 0, 0, 0, time.UTC)

func newStoreSessions(t *testing.T, store docstore.Adapter, max int) *StoreSessions {
	t.Helper()
	s, err := NewStoreSessions(store, max)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreSessionsLifecycle(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	a, b := newStoreSessions(t, store, 10), newStoreSessions(t, store, 10)
	key := TokenKey("secret-token")
	s := Session{ID: "abc", Client: "c1", Net: "n1", Wide: "w1", Created: t0, Expires: t0.Add(time.Hour)}
	if err := a.Create(ctx, key, s, t0, SessionCaps{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Create(ctx, key, s, t0, SessionCaps{}); err != ErrSessionExists {
		t.Errorf("the same key again: %v", err)
	}
	// the other replica sees it
	got, ok, err := b.Get(ctx, key, t0)
	if err != nil || !ok || got != s {
		t.Fatalf("Get on the other replica: %+v %v %v", got, ok, err)
	}
	// the store holds the hash, not the token
	metas, _ := docstore.ListPrefix(ctx, store, SessionKeyPrefix)
	if len(metas) != 1 || strings.Contains(metas[0].ID, "secret-token") || metas[0].ID != SessionKeyPrefix+key {
		t.Errorf("records: %+v", metas)
	}
	// one document per session, whichever replica sets it
	if ok, err := a.SetDoc(ctx, key, "", "doc1", t0); !ok || err != nil {
		t.Fatalf("first SetDoc: %v %v", ok, err)
	}
	if ok, err := b.SetDoc(ctx, key, "", "doc2", t0); ok || err != nil {
		t.Fatalf("second SetDoc must lose: %v %v", ok, err)
	}
	if got, _, _ := b.Get(ctx, key, t0); got.DocID != "doc1" {
		t.Errorf("DocID = %q", got.DocID)
	}
	// extending moves the expiry later, never earlier
	if err := b.Extend(ctx, key, t0.Add(3*time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	if err := a.Extend(ctx, key, t0.Add(time.Minute), t0); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := a.Get(ctx, key, t0.Add(2*time.Hour)); !ok || !got.Expires.Equal(t0.Add(3*time.Hour)) {
		t.Errorf("after Extend: %+v %v", got, ok)
	}
	// expired: absent, and the methods that need a session say so
	late := t0.Add(4 * time.Hour)
	if _, ok, _ := a.Get(ctx, key, late); ok {
		t.Error("an expired session was found")
	}
	if _, err := a.SetDoc(ctx, key, "doc1", "x", late); err != ErrNoSession {
		t.Errorf("SetDoc on an expired session: %v", err)
	}
	if err := a.Extend(ctx, key, late.Add(time.Hour), late); err != ErrNoSession {
		t.Errorf("Extend on an expired session: %v", err)
	}
	// Delete ends it on every replica, and deleting a missing one is fine
	if err := a.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get(ctx, key, t0); ok {
		t.Error("deleted on one replica, still there on the other")
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Errorf("deleting a missing session: %v", err)
	}
}

func TestStoreSessionsSetDocHasOneWinnerUnderRaces(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	key := TokenKey("t")
	if err := newStoreSessions(t, store, 10).Create(ctx, key, Session{ID: "x", Created: t0, Expires: t0.Add(time.Hour)}, t0, SessionCaps{}); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := newStoreSessions(t, store, 10) // a replica each
			if ok, err := r.SetDoc(ctx, key, "", fmt.Sprint("doc", i), t0); ok && err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d replicas set the document, want exactly 1", wins.Load())
	}
}

func TestStoreSessionsCaps(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	st := newStoreSessions(t, store, 7)
	mk := func(client, net, wide string, d time.Duration) Session {
		return Session{ID: "x", Client: client, Net: net, Wide: wide, Created: t0, Expires: t0.Add(d)}
	}
	create := func(key string, s Session, now time.Time, caps SessionCaps) error {
		return st.Create(ctx, TokenKey(key), s, now, caps)
	}
	caps := SessionCaps{PerClient: 2, PerNet: 3, PerWide: 4}
	for i, d := range []time.Duration{time.Hour, time.Minute} {
		if err := create(fmt.Sprint("a", i), mk("A", "N1", "W1", d), t0, caps); err != nil {
			t.Fatal(err)
		}
	}
	if err := create("a2", mk("A", "N1", "W1", time.Hour), t0, caps); err != ErrClientSessionsFull {
		t.Fatalf("per client: %v", err)
	}
	if err := create("b0", mk("B", "N1", "W1", time.Hour), t0, caps); err != nil {
		t.Fatal(err)
	}
	if err := create("c0", mk("C", "N1", "W1", time.Hour), t0, caps); err != ErrNetSessionsFull {
		t.Fatalf("per net: %v", err)
	}
	if err := create("d0", mk("D", "N2", "W1", time.Hour), t0, caps); err != nil {
		t.Fatal(err)
	}
	if err := create("e0", mk("E", "N3", "W1", time.Hour), t0, caps); err != ErrWideSessionsFull {
		t.Fatalf("per wide: %v", err)
	}
	if err := create("f0", mk("F", "N4", "W2", time.Hour), t0, caps); err != nil {
		t.Fatal(err)
	}
	if err := create("f1", mk("G", "N5", "W3", time.Hour), t0, caps); err != nil { // the 6th
		t.Fatal(err)
	}
	if err := create("f2", mk("H", "N6", "W4", time.Hour), t0, caps); err != nil { // the 7th: now at the cap
		t.Fatal(err)
	}
	if err := create("f3", mk("I", "N7", "W5", time.Hour), t0, caps); err != ErrSessionsFull {
		t.Fatalf("global: %v", err)
	}
	// a session that expired no longer counts once the counts are rebuilt (a minute on, a1 is gone),
	// and Reap deletes it
	later := t0.Add(2 * time.Minute)
	if n, err := st.Reap(ctx, later); err != nil || n != 1 {
		t.Fatalf("Reap = %d %v, want 1", n, err)
	}
	if err := create("a3", mk("A", "N1", "W1", time.Hour), later, caps); err != nil {
		t.Fatalf("after the expired one was reaped: %v", err)
	}
}

func TestStoreSessionsReserveKeepsRoomForNewNetworks(t *testing.T) {
	ctx := context.Background()
	st := newStoreSessions(t, docstore.NewMemoryAdapter(), 10)
	caps := SessionCaps{ReservePercent: 20} // the last 2 of 10 are for networks with no session yet
	for i := range 8 {
		s := Session{ID: "x", Wide: "W1", Created: t0, Expires: t0.Add(time.Hour)}
		if err := st.Create(ctx, TokenKey(fmt.Sprint(i)), s, t0, caps); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	old := Session{ID: "x", Wide: "W1", Created: t0, Expires: t0.Add(time.Hour)}
	if err := st.Create(ctx, TokenKey("known"), old, t0, caps); err != ErrSessionsFull {
		t.Fatalf("a known network in the reserve: %v", err)
	}
	if err := st.Create(ctx, TokenKey("new"), Session{ID: "x", Wide: "W2", Created: t0, Expires: t0.Add(time.Hour)}, t0, caps); err != nil {
		t.Fatalf("a newcomer: %v", err)
	}
}

// Two replicas that have not recounted may each let a cap's worth in; the next recount sees both.
func TestStoreSessionsReplicasOvershootByWhatTheyWriteBetweenRecounts(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	a, b := newStoreSessions(t, store, 3), newStoreSessions(t, store, 3)
	a.Refresh, b.Refresh = time.Hour, time.Hour
	mk := func() Session { return Session{ID: "x", Created: t0, Expires: t0.Add(time.Hour)} }
	for i := range 3 {
		if err := a.Create(ctx, TokenKey(fmt.Sprint("a", i)), mk(), t0, SessionCaps{}); err != nil {
			t.Fatal(err)
		}
	}
	// b counted before a wrote? No: b's first Create counts the store, so it already sees 3.
	if err := b.Create(ctx, TokenKey("b0"), mk(), t0, SessionCaps{}); err != ErrSessionsFull {
		t.Fatalf("b recounts on its first Create and sees the store full: %v", err)
	}
	// but once both have counted, what each writes is unseen by the other until the next recount
	c := newStoreSessions(t, store, 5)
	c.Refresh = time.Hour
	if err := c.Create(ctx, TokenKey("c0"), mk(), t0, SessionCaps{}); err != nil { // counts 3, writes the 4th
		t.Fatal(err)
	}
	if err := a.Create(ctx, TokenKey("a3"), mk(), t0, SessionCaps{}); err != ErrSessionsFull {
		t.Fatalf("a's own count: %v", err) // a still thinks 3 of 3
	}
	if n, _ := docstore.ListPrefix(ctx, store, SessionKeyPrefix); len(n) != 4 {
		t.Fatalf("records = %d", len(n))
	}
}

func TestStoreSessionsNeedConditionalWrites(t *testing.T) {
	if _, err := NewStoreSessions(plainAdapter{docstore.NewMemoryAdapter()}, 0); err == nil {
		t.Error("a store without conditional writes was accepted")
	}
}

// plainAdapter hides the conditional methods of the adapter it wraps.
type plainAdapter struct{ docstore.Adapter }

// Two services over one store share the anonymous sessions: a session begun on one writes its
// document through the other.
func TestTwoServicesShareAnonymousSessionsThroughTheStore(t *testing.T) {
	var sessionsA *StoreSessions
	e := newEnv(t, func(o *Options) {
		sessionsA, _ = NewStoreSessions(o.Store, 0)
		o.Sessions = sessionsA
	})
	token, _ := e.session()

	// a second service over the same store, with its own StoreSessions
	o := Options{Store: e.store, Now: e.clk.Now, Config: e.svc.cfg, Sessions: newStoreSessions(t, e.store, 0)}
	svc2, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	srv2 := docstore.NewServer(e.store, "memory")
	svc2.Mount(srv2)
	ts2 := httptest.NewServer(srv2.Handler())
	defer ts2.Close()
	e2 := &env{t: t, clk: e.clk, inner: e.inner, store: e.store, svc: svc2, srv: ts2}

	d := e2.create(token, "written through the other replica")
	if d.ID == "" {
		t.Fatalf("no document: %+v", d)
	}
	// and the first replica sees that the session now has its document
	if r := e.do("POST", "/public/doc", token, []byte("a second one")); r.StatusCode == 201 {
		t.Errorf("a second document on the same session was accepted: %d %s", r.StatusCode, r.body)
	}
}

// With a PriorityQuota two services over one store share each reporter's allowance of priority reports; the
// per-process count lets each use it.
func TestPriorityAllowanceIsSharedByReplicasThroughTheStore(t *testing.T) {
	ctx := context.Background()
	var a *Service
	e := newEnv(t, func(o *Options) {
		o.Config.MaxPriorityReportsPerReporter = 2
		q, err := NewStoreQuota(o.Store, 2, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		q.Now = o.Now
		o.PriorityQuota = q
	})
	a = e.svc
	q2, _ := NewStoreQuota(e.store, 2, 24*time.Hour)
	q2.Now = e.clk.Now
	b, err := New(Options{Store: e.store, Now: e.clk.Now, Config: e.svc.cfg, PriorityQuota: q2})
	if err != nil {
		t.Fatal(err)
	}
	if !a.allowPriority(ctx, "rep1") || !b.allowPriority(ctx, "rep1") {
		t.Fatal("the first two are allowed, one on each replica")
	}
	if a.allowPriority(ctx, "rep1") || b.allowPriority(ctx, "rep1") {
		t.Error("a third was allowed: the replicas do not share the count")
	}
	if !a.allowPriority(ctx, "rep2") {
		t.Error("another reporter has an allowance of its own")
	}
	// without it each process counts for itself
	c, d := &Service{cfg: Config{MaxPriorityReportsPerReporter: 1}, now: e.clk.Now}, &Service{cfg: Config{MaxPriorityReportsPerReporter: 1}, now: e.clk.Now}
	if !c.allowPriority(ctx, "rep1") || !d.allowPriority(ctx, "rep1") {
		t.Error("per-process counts are independent")
	}
	c.prio.record("rep1", e.clk.Now())
	if c.allowPriority(ctx, "rep1") {
		t.Error("a recorded report counts in its own process")
	}
}
