package docstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// stepAdapter records every mutation step.
type stepAdapter struct {
	Adapter
	mu     sync.Mutex
	log    []string
	failAt int
}

func (s *stepAdapter) step(op string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, op)
	if s.failAt > 0 && len(s.log) == s.failAt {
		return errors.New("injected failure at " + op)
	}
	return nil
}

func (s *stepAdapter) Save(ctx context.Context, id string, data []byte) error {
	if err := s.step("save:" + id); err != nil {
		return err
	}
	return s.Adapter.Save(ctx, id, data)
}

func (s *stepAdapter) Delete(ctx context.Context, id string) error {
	if err := s.step("delete:" + id); err != nil {
		return err
	}
	return s.Adapter.Delete(ctx, id)
}

func (s *stepAdapter) reset(failAt int) {
	s.mu.Lock()
	s.log, s.failAt = nil, failAt
	s.mu.Unlock()
}

func (s *stepAdapter) ops() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.log...)
}

func has(t *testing.T, a Adapter, id string) bool {
	t.Helper()
	_, err := a.Load(context.Background(), id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func put(t *testing.T, a Adapter, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := a.Save(context.Background(), id, []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFixedTTLIsAnAllowlistOfDocumentKeys(t *testing.T) {
	p := FixedTTL(time.Hour)
	for id, want := range map[string]bool{
		"doc": true, "a.docx": true, "0b6f2c1e-1111-4222-8333-444455556666~1f2e3d4c5b6a.open": true, "my/nested/doc": true,
		"keys:abc": false, "KEYS:abc": false, "fonts/Inter/400-normal-1234abcd.ttf": false, "acl:x": false, "collab:x": false,
		"catalog:index": false, "catalog:u/ab/index": false, "versions/a/1.open": false, "directory/h/k": false,
		"passkey/account/x": false, "expiry:x": false, "unknown:thing": false, "unknown:thing/x": false, "": false,
	} {
		if _, ok := p.TTL(id, time.Now()); ok != want {
			t.Errorf("TTL(%q) expires = %v, want %v", id, ok, want)
		}
	}
}

func TestExpiryNeverSweepsKeyRecordsOrFonts(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	system := []string{"keys:doc1", "fonts/Inter/400-normal-1234abcd.ttf", "acl:doc1", "collab:doc1", "catalog:index", "directory/h/k", "passkey/account/x", "versions/doc1/1.open"}
	put(t, inner, append([]string{"doc1.open"}, system...)...)
	ea, clk := newExpiring(inner)
	ea.AdoptUpdatedAt = true
	clk.Add(30 * day)
	if _, err := ea.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range system {
		if id == "acl:doc1" || id == "collab:doc1" || id == "keys:doc1" || id == "versions/doc1/1.open" {
			continue
		}
		if !has(t, inner, id) {
			t.Errorf("%s was swept", id)
		}
		if _, ok, _ := ea.Expiry(ctx, id); ok {
			t.Errorf("%s got a deadline", id)
		}
	}
	put(t, ea, "doc2.open")
	put(t, inner, "keys:doc2.open", "keys:doc3")
	put(t, ea, "doc3~0123456789ab.open")
	clk.Add(2 * day)
	if n, err := ea.Sweep(ctx); err != nil || n < 2 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	if has(t, inner, "keys:doc2.open") || has(t, inner, "keys:doc3") {
		t.Error("the key record of an expired document was left behind")
	}
}

func TestExpiryKeyRecordOfLiveDocumentSurvivesSiblingExpiry(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	put(t, ea, "u1~aaaaaaaaaaaa.open") // the previous copy
	clk.Add(20 * time.Hour)
	put(t, ea, "u1~bbbbbbbbbbbb.open") // the current one
	put(t, inner, "keys:u1", "acl:u1", "versions/u1/1.open")
	clk.Add(5 * time.Hour)
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	for _, id := range []string{"u1~bbbbbbbbbbbb.open", "keys:u1", "acl:u1", "versions/u1/1.open"} {
		if !has(t, inner, id) {
			t.Errorf("%s deleted with the expired previous copy", id)
		}
	}
	clk.Add(day)
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	for _, id := range []string{"keys:u1", "acl:u1", "versions/u1/1.open"} {
		if has(t, inner, id) {
			t.Errorf("%s left after the last copy expired", id)
		}
	}
}

func TestExpiryAdoptionDefaultsToAFullGracePeriod(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	put(t, inner, "old1", "old2", "acl:old1")
	ea := NewExpiringAdapter(inner, FixedTTL(day))
	clk := &fakeClock{t: time.Now().Add(5 * day)} // far past UpdatedAt + TTL
	ea.Now = clk.Now
	if n, err := ea.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("first sweep deleted %d (%v): enabling expiry must not delete at once", n, err)
	}
	for _, id := range []string{"old1", "old2"} {
		at, ok, _ := ea.Expiry(ctx, id)
		if !ok || !at.Equal(clk.Now().Add(day)) {
			t.Errorf("%s deadline %v %v, want now+TTL", id, at, ok)
		}
	}
	if _, ok, _ := ea.Expiry(ctx, "acl:old1"); ok {
		t.Error("acl got a deadline")
	}
	clk.Add(day - time.Minute)
	if n, _ := ea.Sweep(ctx); n != 0 {
		t.Errorf("deleted %d inside the grace period", n)
	}
	clk.Add(2 * time.Minute)
	if n, _ := ea.Sweep(ctx); n != 2 {
		t.Errorf("deleted %d after the grace period, want 2", n)
	}
}

func TestParseExpiryAdopt(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "grace": false, " Updated ": true, "updated": true} {
		if got, err := ParseExpiryAdopt(raw); err != nil || got != want {
			t.Errorf("ParseExpiryAdopt(%q) = %v, %v", raw, got, err)
		}
	}
	if _, err := ParseExpiryAdopt("yes"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestExpiryRelatedCollisions(t *testing.T) {
	ctx := context.Background()
	t.Run("a.docx vs a.pdf share the stem", func(t *testing.T) {
		inner := NewMemoryAdapter()
		ea, clk := newExpiring(inner)
		put(t, ea, "a.docx")
		clk.Add(20 * time.Hour)
		put(t, ea, "a.pdf")
		put(t, inner, "versions/a/1.docx", "acl:a.docx", "acl:a.pdf")
		clk.Add(5 * time.Hour)
		if n, err := ea.Sweep(ctx); err != nil || n != 1 {
			t.Fatalf("sweep = %d %v", n, err)
		}
		if !has(t, inner, "versions/a/1.docx") || !has(t, inner, "a.pdf") || !has(t, inner, "acl:a.pdf") {
			t.Error("the live a.pdf lost its versions or access list")
		}
		if has(t, inner, "acl:a.docx") {
			t.Error("acl of the expired document kept")
		}
		clk.Add(day)
		if n, _ := ea.Sweep(ctx); n != 1 {
			t.Fatalf("second sweep = %d", n)
		}
		if has(t, inner, "versions/a/1.docx") {
			t.Error("versions kept after the last sibling expired")
		}
	})
	t.Run("a vs a/b", func(t *testing.T) {
		inner := NewMemoryAdapter()
		ea, clk := newExpiring(inner)
		put(t, ea, "a")
		clk.Add(20 * time.Hour)
		put(t, ea, "a/b")
		put(t, inner, "versions/a/1", "versions/a/b/1", "versions/a/b/2")
		clk.Add(5 * time.Hour) // "a" expired, "a/b" live
		if n, err := ea.Sweep(ctx); err != nil || n != 1 {
			t.Fatalf("sweep = %d %v", n, err)
		}
		if has(t, inner, "versions/a/1") {
			t.Error("versions of a kept")
		}
		if !has(t, inner, "versions/a/b/1") || !has(t, inner, "versions/a/b/2") || !has(t, inner, "a/b") {
			t.Error("the history of the live a/b was deleted with a's")
		}
		clk.Add(day)
		_, _ = ea.Sweep(ctx)
		if has(t, inner, "versions/a/b/1") {
			t.Error("history of a/b kept after a/b expired")
		}
	})
	t.Run("an id with a slash never reaches beyond its own versions", func(t *testing.T) {
		exact, prefixes := DefaultRelated("x/y.docx")
		for _, p := range prefixes {
			if p != "versions/x/y.docx/" {
				t.Errorf("prefix %q", p)
			}
		}
		for _, e := range exact {
			if !strings.HasSuffix(e, ":x/y.docx") {
				t.Errorf("exact %q reaches beyond the id", e)
			}
		}
	})
}

func TestExpirySaveWritesRecordBeforeDocument(t *testing.T) {
	ctx := context.Background()
	st := &stepAdapter{Adapter: NewMemoryAdapter()}
	ea, _ := newExpiring(st)
	if err := ea.Save(ctx, "d", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(st.ops(), ","), "save:expiry-created:d,save:expiry:d,save:d"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	st2 := &stepAdapter{Adapter: NewMemoryAdapter(), failAt: 3}
	ea2, _ := newExpiring(st2)
	if err := ea2.Save(ctx, "d", []byte("x")); err == nil {
		t.Fatal("injected failure not reported")
	}
	if has(t, st2.Adapter, "d") {
		t.Error("document written despite failure")
	}
	inner := NewMemoryAdapter()
	ea3, clk := newExpiring(inner)
	_ = inner.Save(ctx, "expiry:d", []byte(clk.Now().Add(day).UTC().Format(time.RFC3339Nano)))
	clk.Add(2 * day)
	if _, err := ea3.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if has(t, inner, "expiry:d") {
		t.Error("orphan record not dropped")
	}
}

func TestExpiryPurgeDeletesRecordLastAndRetriesAfterFailure(t *testing.T) {
	ctx := context.Background()
	build := func() (*stepAdapter, *ExpiringAdapter, *fakeClock) {
		st := &stepAdapter{Adapter: NewMemoryAdapter()}
		ea, clk := newExpiring(st)
		put(t, ea, "d.open")
		put(t, st.Adapter, "acl:d.open", "collab:d.open", "keys:d.open", "versions/d/1.open", "versions/d/2.open")
		clk.Add(2 * day)
		st.reset(0)
		return st, ea, clk
	}
	st, ea, _ := build()
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	ops := st.ops()
	if len(ops) < 3 || ops[0] != "delete:d.open" || ops[len(ops)-1] != "delete:expiry:d.open" {
		t.Fatalf("order = %v: want the document first and the record last", ops)
	}
	for _, o := range ops[:len(ops)-1] {
		if o == "delete:expiry:d.open" {
			t.Fatalf("record deleted before the end: %v", ops)
		}
	}
	steps := len(ops)
	for n := 1; n <= steps; n++ {
		st, ea, _ := build()
		st.reset(n)
		_, err := ea.Sweep(ctx)
		if err == nil {
			t.Fatalf("failure at step %d not reported", n)
		}
		_, recErr := st.Load(ctx, "expiry:d.open")
		if recErr != nil {
			t.Fatalf("step %d failed but the record is gone: the sweeper can no longer find the leftovers", n)
		}
		st.reset(0)
		if cnt, err := ea.Sweep(ctx); err != nil {
			t.Fatalf("retry after step %d: %d %v", n, cnt, err)
		}
		left, _ := st.List(ctx)
		var ids []string
		for _, m := range left {
			ids = append(ids, m.ID)
		}
		if len(ids) != 0 {
			t.Fatalf("after failure at step %d and a retry, left %v", n, ids)
		}
	}
}

func TestExpiryPolicyFlipOnWriteDeletesRecordAndNeverSweeps(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	st := &stepAdapter{Adapter: inner}
	noTTL := false
	ea := NewExpiringAdapter(st, ExpiryPolicyFunc(func(id string, _ time.Time) (time.Duration, bool) { return time.Hour, !noTTL }))
	clk := newClock()
	ea.Now = clk.Now
	put(t, ea, "d")
	noTTL = true
	st.reset(0)
	put(t, ea, "d")
	if got, want := strings.Join(st.ops(), ","), "delete:expiry-created:d,delete:expiry:d,save:d"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if has(t, inner, "expiry:d") {
		t.Error("record kept")
	}
	clk.Add(10 * time.Hour)
	if n, err := ea.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep = %d %v", n, err)
	}
	if !has(t, ea, "d") {
		t.Error("exempt document swept")
	}
}

func TestExpiryDeleteOfLiveDocumentRemovesRelatedRecords(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, _ := newExpiring(inner)
	put(t, ea, "d.open", "other.open")
	put(t, inner, "acl:d.open", "collab:d.open", "keys:d.open", "versions/d/1.open", "acl:other.open")
	if err := ea.Delete(ctx, "d.open"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"d.open", "acl:d.open", "collab:d.open", "keys:d.open", "versions/d/1.open", "expiry:d.open"} {
		if has(t, inner, id) {
			t.Errorf("%s survived Delete", id)
		}
	}
	if !has(t, inner, "other.open") || !has(t, inner, "acl:other.open") {
		t.Error("Delete touched another document")
	}
	if err := ea.Delete(ctx, "d.open"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	put(t, ea, "d.open")
	if has(t, inner, "acl:d.open") {
		t.Error("new document inherited an access list")
	}
}

// countLoads counts Loads of documents.
type countLoads struct {
	Adapter
	mu    sync.Mutex
	loads int
}

func (c *countLoads) Load(ctx context.Context, id string) (Document, error) {
	if strings.HasPrefix(id, ExpiryPrefix) {
		c.mu.Lock()
		c.loads++
		c.mu.Unlock()
	}
	return c.Adapter.Load(ctx, id)
}

func (c *countLoads) n() int { c.mu.Lock(); defer c.mu.Unlock(); return c.loads }

func TestExpiryListingsCacheDeadlines(t *testing.T) {
	ctx := context.Background()
	cl := &countLoads{Adapter: NewMemoryAdapter()}
	ea, clk := newExpiring(cl)
	for i := 0; i < 20; i++ {
		put(t, ea, fmt.Sprintf("d%02d", i))
	}
	base := cl.n()
	for i := 0; i < 5; i++ {
		if got := ids(t, ea); len(got) != 20 {
			t.Fatalf("list = %d", len(got))
		}
	}
	if d := cl.n() - base; d > 20 {
		t.Errorf("5 listings of 20 records made %d record Loads, want at most one pass (20)", d)
	}
	put(t, ea, "late")
	if got := ids(t, ea); len(got) != 21 {
		t.Errorf("list after Save = %d", len(got))
	}
	clk.Add(day + time.Minute)
	if got := ids(t, ea); len(got) != 0 {
		t.Errorf("expired documents listed after the cache window: %v", got)
	}
	_ = ctx
}

func TestExpiryCacheStalenessAcrossProcessesIsBounded(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	clk := newClock()
	mk := func() *ExpiringAdapter {
		e := NewExpiringAdapter(inner, FixedTTL(day))
		e.Now = clk.Now
		e.DeadlineCacheTTL = 10 * time.Second
		return e
	}
	a, b := mk(), mk()
	put(t, a, "d")
	if got := ids(t, b); len(got) != 1 { // b caches d's deadline
		t.Fatalf("list = %v", got)
	}
	clk.Add(day - 5*time.Second)
	put(t, a, "d") // a renews; b's cache still holds the old deadline
	clk.Add(6 * time.Second)
	if !has(t, b, "d") {
		t.Error("Load hid a renewed document")
	}
	clk.Add(10 * time.Second)
	got := ids(t, b)
	sort.Strings(got)
	if len(got) != 1 {
		t.Errorf("listing still stale after the cache bound: %v", got)
	}
	_ = ctx
}
