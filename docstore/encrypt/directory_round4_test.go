package encrypt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

func crashAfterEnroll(t *testing.T, e *ownerEnv, withKey bool) (Recipient, []byte) {
	t.Helper()
	ctx := context.Background()
	var k Recipient
	if withKey {
		k = newRecovery(t)
		if err := e.d.Publish(ctx, ana, k); err != nil {
			t.Fatal(err)
		}
	}
	_, priv := ownerKey(t)
	e.mustEnroll(priv)
	for _, id := range e.d.enrollNames("ana@x.io") {
		_ = e.store.Delete(ctx, id)
	}
	if v, _ := e.d.Enrollment(ctx, "ana@x.io"); v.Enrolled {
		t.Fatal("the record is gone")
	}
	return k, nil
}

func TestReconcileFindsAFirstEnrollmentThroughThePublishedKeys(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	crashAfterEnroll(t, e, true)
	rep, err := e.d.reconcileAll(ctx)
	if err != nil || rep.Completed != 0 || rep.Deferred != 1 {
		t.Fatalf("too young to judge: %+v %v", rep, err)
	}
	e.clk.Add(reconcileGrace + time.Second)
	if n, err := e.d.ReconcileEnrollments(ctx); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if v, err := e.d.Enrollment(ctx, "ana@x.io"); err != nil || !v.Enrolled {
		t.Fatalf("%+v %v", v, err)
	}
	if a := e.audit(AuditOptions{}); len(a.Violations) != 0 || !a.Enrolled {
		t.Errorf("%+v", a)
	}
}

func TestReconcileCountsAnAddressItCannotName(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	crashAfterEnroll(t, e, false)
	e.clk.Add(reconcileGrace + time.Second)
	rep, err := e.d.reconcileAll(ctx)
	if err != nil || rep.Completed != 0 || rep.Unmapped != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestPublishAndRevokeReconcileTheEnrollment(t *testing.T) {
	e := newOwnerEnv(t)
	ctx := context.Background()
	k, _ := crashAfterEnroll(t, e, true)
	if _, err := e.d.PublishWith(ctx, ana, newRecovery(t), PublishOptions{}); !errors.Is(err, docstore.ErrDirectoryBusy) {
		t.Fatalf("a young dangling entry: %v", err)
	}
	if _, err := e.d.Revoke(ctx, ana, k.Fingerprint, nil); !errors.Is(err, docstore.ErrDirectoryBusy) {
		t.Fatalf("a young dangling entry: %v", err)
	}
	e.clk.Add(reconcileGrace + time.Second)
	if _, err := e.d.PublishWith(ctx, ana, newRecovery(t), PublishOptions{}); !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("the record is completed and the owner's signature asked: %v", err)
	}
	for _, id := range e.d.enrollNames("ana@x.io") {
		_ = e.store.Delete(ctx, id)
	}
	if _, err := e.d.Revoke(ctx, ana, k.Fingerprint, nil); !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("%v", err)
	}
}

func TestRunSweepReconcilesAgainAfterTheGrace(t *testing.T) {
	e := newOwnerEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.d.ReconcileGrace = 40 * time.Millisecond
	crashAfterEnroll(t, e, true)
	if rep, err := e.d.reconcileAll(ctx); err != nil || rep.Deferred != 1 {
		t.Fatalf("the start-up reconcile finds it too young: %+v %v", rep, err)
	}
	go e.d.RunSweep(ctx, time.Hour)
	time.Sleep(300 * time.Millisecond)
	if v, _ := e.d.Enrollment(ctx, "ana@x.io"); v.Enrolled {
		t.Fatal("completed before the grace was over")
	}
	e.clk.Add(time.Minute)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v, _ := e.d.Enrollment(ctx, "ana@x.io"); v.Enrolled {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the record was not completed after the grace")
}

func TestPruningATombstoneRemovesTheRetiredNameCopies(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	clk := newTestClock()
	old := &AdapterDirectory{Store: store, Signer: testSigner(t, 1), Now: clk.Now}
	k1, k2 := newRecovery(t), newRecovery(t)
	oldCopies := map[string][]byte{}
	for _, k := range []Recipient{k1, k2} {
		if err := old.Publish(ctx, ana, k); err != nil {
			t.Fatal(err)
		}
		doc, err := store.Load(ctx, old.directoryID("ana@x.io", k.Fingerprint))
		if err != nil {
			t.Fatal(err)
		}
		oldCopies[k.Fingerprint] = doc.Data
	}
	rotated, err := DirectorySignerFromSeed([]byte(testSeed(2)), []byte(testSeed(1)))
	if err != nil {
		t.Fatal(err)
	}
	d := &AdapterDirectory{Store: store, Signer: rotated, Now: clk.Now}
	clk.Add(time.Minute)
	restore := func() { // the retired-name copies that a best-effort delete failed to remove
		for fp, data := range oldCopies {
			if err := store.Save(ctx, old.directoryID("ana@x.io", fp), data); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, k := range []Recipient{k1, k2} {
		if _, err := d.Revoke(ctx, ana, k.Fingerprint, nil); err != nil {
			t.Fatal(err)
		}
	}
	restore()
	if rows, _ := d.Lookup(ctx, "ana@x.io"); len(rows) != 0 {
		t.Fatalf("while the tombstones stand the keys stay revoked: %+v", rows)
	}
	clk.Add(DefaultRevokeWindow + time.Hour)
	if n, err := d.Sweep(ctx); err != nil || n < 2 {
		t.Fatalf("%d %v", n, err)
	}
	if rows, _ := d.Lookup(ctx, "ana@x.io"); len(rows) != 0 {
		t.Fatalf("a revoked key came back from a retired-name copy: %+v", rows)
	}

	clk.Add(time.Minute)
	for _, k := range []Recipient{k1, k2} {
		if err := d.Publish(ctx, ana, k); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Revoke(ctx, ana, k.Fingerprint, nil); err != nil {
			t.Fatal(err)
		}
	}
	restore()
	clk.Add(DefaultRevokeWindow + time.Hour)
	if err := d.Publish(ctx, ana, newRecovery(t)); err != nil {
		t.Fatal(err)
	}
	if rows, _ := d.Lookup(ctx, "ana@x.io"); len(rows) != 1 {
		t.Fatalf("only the new key: %+v", rows)
	}
	k3 := newRecovery(t)
	if err := d.Publish(ctx, ana, k3); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Revoke(ctx, ana, k3.Fingerprint, nil); err != nil {
		t.Fatal(err)
	}
	clk.Add(time.Minute)
	newer := &AdapterDirectory{Store: store, Signer: testSigner(t, 1), Now: clk.Now}
	if err := newer.Publish(ctx, ana, k3); err != nil {
		t.Fatal(err)
	}
	clk.Add(DefaultRevokeWindow + time.Hour)
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	found := false
	rows, _ := d.Lookup(ctx, "ana@x.io")
	for _, r := range rows {
		found = found || r.Fingerprint == k3.Fingerprint
	}
	if !found {
		t.Errorf("a key published again after its revocation was removed with the tombstone: %+v", rows)
	}
}

func testSeed(fill byte) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = fill
	}
	return string(b)
}

func TestAuditHoldsAVoidToTheVoidWindow(t *testing.T) {
	d, _, _ := newTestDirectory(t)
	addr := d.LogAddressFor("ana@x.io")
	const at0 = int64(1_000_000)
	pub := LogEntry{Seq: 1, Prev: zeroHash, At: at0, Op: OpPublish, Address: addr, Fingerprint: "SHA256:x"}
	for name, tc := range map[string]struct {
		after int64
		ok    bool
	}{
		"on time":         {1, true},
		"at the window":   {int64(VoidWindow / time.Second), true},
		"past the window": {int64(VoidWindow/time.Second) + 1, false},
		"long after":      {int64(time.Hour / time.Second), false},
	} {
		v := LogEntry{Seq: 2, Prev: "x", At: at0 + tc.after, Op: OpVoid, Address: addr, Ref: 1}
		a := AuditAddress("ana@x.io", []string{addr}, []LogEntry{pub, v}, AuditOptions{})
		_, voided := a.Voided[1]
		if voided != tc.ok || (len(a.Violations) == 0) != tc.ok {
			t.Errorf("%s: voided %v, violations %+v", name, voided, a.Violations)
		}
	}
}

func TestAdminFailureBudgetFollowsTheConfiguredClientAddress(t *testing.T) {
	path := "/admin/directory/reset-enrollment/" + strings.Repeat("ab", 32)
	fail := func(e *dirEnv, client string) int {
		req := httptest.NewRequest("POST", path, nil)
		req.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	deny := func(*http.Request) bool { return false }
	shared := newDirEnvWith(t, nil, func(s *docstore.Server) { s.WithDirectoryAdmin(deny) })
	split := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryAdmin(deny).WithDirectoryClientIP(func(r *http.Request) string { return r.Header.Get("X-Forwarded-For") })
	})
	for _, e := range []*dirEnv{shared, split} {
		for range 10 {
			if code := fail(e, "198.51.100.1"); code != 403 {
				t.Fatalf("a failure inside the budget: %d", code)
			}
		}
		if code := fail(e, "198.51.100.1"); code != 429 {
			t.Fatalf("the client's budget is spent: %d", code)
		}
	}
	if code := fail(shared, "198.51.100.2"); code != 429 {
		t.Errorf("without a client address function the budget is the connection's: %d", code)
	}
	if code := fail(split, "198.51.100.2"); code != 403 {
		t.Errorf("another client has its own budget: %d", code)
	}
}

func TestLookupIPBudgetFollowsTheConfiguredClientAddress(t *testing.T) {
	limits := func(name string, perMinute float64, burst int) docstore.Limiter {
		if name == "dir-lookup-ip" {
			return docstore.NewRateLimiter(0.001, 1) // one lookup per client, then wait
		}
		return docstore.NewRateLimiter(perMinute*100, burst*100)
	}
	lookup := func(e *dirEnv, tok, client string) int {
		req := httptest.NewRequest("GET", "/directory/lookup?email=bo@x.io", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Forwarded-For", client)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	shared := newDirEnvWith(t, nil, func(s *docstore.Server) { s.WithDirectoryLimits(limits) })
	split := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryLimits(limits).WithDirectoryClientIP(func(r *http.Request) string { return r.Header.Get("X-Forwarded-For") })
	})
	for name, tc := range map[string]struct {
		e    *dirEnv
		want int
	}{"shared": {shared, 429}, "split": {split, 200}} {
		tok := tc.e.token(ana)
		if code := lookup(tc.e, tok, "198.51.100.1"); code != 200 {
			t.Fatalf("%s: the first lookup: %d", name, code)
		}
		if code := lookup(tc.e, tc.e.token(bo), "198.51.100.2"); code != tc.want {
			t.Errorf("%s: a lookup from another client: %d, want %d", name, code, tc.want)
		}
		if code := lookup(tc.e, tc.e.token(ana), "198.51.100.1"); code != 429 {
			t.Errorf("%s: the same client again: %d", name, code)
		}
	}
}
