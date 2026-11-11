package directoryclient

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

type witnessEnv struct {
	*env
	wpub  map[string]ed25519.PublicKey
	wpriv map[string]ed25519.PrivateKey
}

func newWitnessEnv(t *testing.T, seed []byte, names ...string) *witnessEnv {
	t.Helper()
	mgr, err := auth.NewManager(context.Background(), auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := encrypt.DirectorySignerFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	w := &witnessEnv{wpub: map[string]ed25519.PublicKey{}, wpriv: map[string]ed25519.PrivateKey{}}
	for _, n := range names {
		pub, priv, _ := ed25519.GenerateKey(rand.Reader)
		w.wpub[n], w.wpriv[n] = pub, priv
	}
	store := docstore.NewMemoryAdapter()
	dir := &encrypt.AdapterDirectory{Store: store, Signer: signer, Witnesses: w.wpub}
	handler := docstore.NewServer(store, "memory").WithAuth(mgr).WithDirectory(encrypt.DirectoryService(dir)).
		WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Op") == "1" }).
		WithDirectoryLimits(func(_ string, perMinute float64, burst int) docstore.Limiter {
			return docstore.NewRateLimiter(perMinute*1000, burst*1000)
		}).Handler()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	w.env = &env{t: t, srv: srv, mgr: mgr, store: store, dir: dir, keys: signer.(encrypt.PublicKeyer).PublicKeys()}
	return w
}

func (w *witnessEnv) witness() *Client {
	c := w.client(bo)
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("X-Op", "1")
		return http.DefaultTransport.RoundTrip(r)
	})}
	return c
}

func (w *witnessEnv) cosign(name string, st *WitnessState) error {
	priv := w.wpriv[name]
	_, err := w.witness().Witness(context.Background(), name, func(m []byte) []byte { return ed25519.Sign(priv, m) }, st)
	return err
}

func (w *witnessEnv) publish(n int) {
	w.t.Helper()
	c := w.client(ana)
	for range n {
		p, _, id := ageKey(w.t)
		if _, err := c.Publish(context.Background(), PublishRequest{Email: "ana@x.io", Key: p, Identity: id}); err != nil {
			w.t.Fatal(err)
		}
	}
}

func (w *witnessEnv) watcher(quorum int) *Client {
	c := w.client(ana)
	c.Witnesses, c.WitnessQuorum = w.wpub, quorum
	return c
}

func TestAClientRequiresAQuorumOfWitnessesToHaveCosignedTheLog(t *testing.T) {
	ctx := context.Background()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	w := newWitnessEnv(t, seed, "w1", "w2", "w3")
	w.publish(3)

	c := w.watcher(2)
	if _, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); !errors.Is(err, ErrNotWitnessed) {
		t.Fatalf("nothing cosigned: %v", err)
	}
	st1, st2 := &WitnessState{}, &WitnessState{}
	if err := w.cosign("w1", st1); err != nil || st1.Seq != 3 {
		t.Fatalf("%v %+v", err, st1)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); !errors.Is(err, ErrNotWitnessed) {
		t.Fatalf("one of two: %v", err)
	}
	if err := w.cosign("w2", st2); err != nil {
		t.Fatal(err)
	}
	rep, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{})
	if err != nil || rep.Witnessed == nil || rep.Witnessed.Seq != 3 || !rep.WitnessChecked || len(rep.Witnessed.Witnesses) != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	w.publish(2)
	rep, err = c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{})
	if err != nil || rep.Head.Seq != 5 || rep.Witnessed.Seq != 3 || !rep.WitnessChecked {
		t.Fatalf("%+v %v", rep, err)
	}
	if err := w.cosign("w1", st1); err != nil || st1.Seq != 5 {
		t.Fatalf("%v %+v", err, st1)
	}
	if err := w.cosign("w2", st2); err != nil {
		t.Fatal(err)
	}
	rep, err = c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{})
	if err != nil || rep.Witnessed.Seq != 5 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := w.watcher(3).WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); !errors.Is(err, ErrNotWitnessed) {
		t.Fatalf("two of three: %v", err)
	}
	if rep, err := w.client(ana).WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err != nil || rep.Witnessed != nil {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestAWitnessedLogShownAsAnotherLogIsASplitView(t *testing.T) {
	ctx := context.Background()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	a := newWitnessEnv(t, seed, "w1", "w2")
	a.publish(3)
	for _, n := range []string{"w1", "w2"} {
		if err := a.cosign(n, &WitnessState{}); err != nil {
			t.Fatal(err)
		}
	}
	b := newWitnessEnv(t, seed)
	b.wpub = a.wpub
	b.dir.Witnesses = a.wpub
	b.publish(3)
	for n := range a.wpub {
		doc, err := a.store.Load(ctx, "directory-log/witness/"+fmt.Sprintf("%x", n))
		if err != nil {
			t.Fatal(err)
		}
		if err := b.store.Save(ctx, doc.ID, doc.Data); err != nil {
			t.Fatal(err)
		}
	}
	c := b.watcher(2)
	if _, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); !errors.Is(err, ErrSplitView) {
		t.Fatalf("a log with another entry 3: %v", err)
	}
	if _, err := a.watcher(2).WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err != nil {
		t.Fatalf("%v", err)
	}
	short := newWitnessEnv(t, seed)
	short.dir.Witnesses = a.wpub
	short.publish(2)
	for n := range a.wpub {
		doc, _ := a.store.Load(ctx, "directory-log/witness/"+fmt.Sprintf("%x", n))
		_ = short.store.Save(ctx, doc.ID, doc.Data)
	}
	sc := short.watcher(2)
	sc.Witnesses = a.wpub
	if _, err := sc.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); !errors.Is(err, ErrSplitView) {
		t.Fatalf("a shorter log: %v", err)
	}
}

func TestADirectoryTakesOnlyGenuineCosignaturesOfItsOwnEntries(t *testing.T) {
	ctx := context.Background()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	w := newWitnessEnv(t, seed, "w1")
	w.publish(2)
	e1 := entry(t, w, 2)
	sign := func(name string) func([]byte) []byte {
		priv := w.wpriv[name]
		return func(m []byte) []byte { return ed25519.Sign(priv, m) }
	}
	good := encrypt.CosignEntry(sign("w1"), "w1", e1.Kid, e1.Seq, e1.Hash)
	if err := w.dir.AddCosignature(ctx, good); err != nil {
		t.Fatal(err)
	}
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	bad := []docstore.DirectoryCosignature{
		encrypt.CosignEntry(sign("w1"), "stranger", e1.Kid, e1.Seq, e1.Hash),
		encrypt.CosignEntry(func(m []byte) []byte { return ed25519.Sign(otherPriv, m) }, "w1", e1.Kid, e1.Seq, e1.Hash),
		encrypt.CosignEntry(sign("w1"), "w1", e1.Kid, e1.Seq, "00"+e1.Hash[2:]),
		encrypt.CosignEntry(sign("w1"), "w1", e1.Kid, 99, e1.Hash),
		encrypt.CosignEntry(sign("w1"), "w1", "other-kid", e1.Seq, e1.Hash),
	}
	for i, c := range bad {
		if err := w.dir.AddCosignature(ctx, c); !errors.Is(err, docstore.ErrDirectoryBadCosignature) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	e0 := entry(t, w, 1)
	if err := w.dir.AddCosignature(ctx, encrypt.CosignEntry(sign("w1"), "w1", e0.Kid, e0.Seq, e0.Hash)); err != nil {
		t.Fatal(err)
	}
	got, err := w.dir.Cosigned(ctx)
	if err != nil || len(got) != 1 || got[0].Seq != 2 || len(got[0].Witnesses) != 1 {
		t.Errorf("%+v %v", got, err)
	}
}

func entry(t *testing.T, w *witnessEnv, seq int64) docstore.DirectoryLogEntry {
	t.Helper()
	l, err := w.dir.ReadLog(context.Background(), seq-1, 1)
	if err != nil || len(l.Entries) != 1 {
		t.Fatalf("%v %+v", err, l)
	}
	return l.Entries[0]
}

func TestAWitnessDoesNotCosignALogThatWasCutBack(t *testing.T) {
	ctx := context.Background()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	w := newWitnessEnv(t, seed, "w1")
	w.publish(4)
	st := &WitnessState{}
	if err := w.cosign("w1", st); err != nil || st.Seq != 4 {
		t.Fatalf("%v %+v", err, st)
	}
	for _, seq := range []int64{4, 3} {
		_ = w.store.Delete(ctx, fmt.Sprintf("directory-log/e/%012d", seq))
	}
	_ = w.store.Delete(ctx, "directory-log/head")
	before := *st
	if err := w.cosign("w1", st); !errors.Is(err, ErrLogCut) {
		t.Fatalf("a cut log: %v", err)
	}
	if *st != before {
		t.Errorf("the state moved on a refusal: %+v", st)
	}
}
