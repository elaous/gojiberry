package docstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingAdapter logs the writes it receives.
type recordingAdapter struct {
	Adapter
	mu     sync.Mutex
	saves  []string
	failOn string // fail a Save of this key
}

func (r *recordingAdapter) Save(ctx context.Context, id string, data []byte) error {
	r.mu.Lock()
	r.saves = append(r.saves, id)
	fail := r.failOn != "" && id == r.failOn
	r.mu.Unlock()
	if fail {
		return errors.New("disk full")
	}
	return r.Adapter.Save(ctx, id, data)
}

func (r *recordingAdapter) reset() { r.mu.Lock(); r.saves = nil; r.mu.Unlock() }

func (r *recordingAdapter) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.saves...)
}

func mustExpiry(t *testing.T, ea *ExpiringAdapter, id string) time.Time {
	t.Helper()
	x, ok, err := ea.Expiry(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("Expiry(%q) = %v, %v, %v; want a deadline", id, x, ok, err)
	}
	return x
}

func TestTouchMovesTheDeadlineAndLeavesTheDocumentAlone(t *testing.T) {
	ctx := context.Background()
	inner := &recordingAdapter{Adapter: NewMemoryAdapter()}
	ea, clk := newExpiring(inner)
	if err := ea.Save(ctx, "doc", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	before, _ := inner.Load(ctx, "doc")
	clk.Add(23 * time.Hour)
	inner.reset()
	if err := ea.Touch(ctx, "doc"); err != nil {
		t.Fatal(err)
	}
	if got, want := mustExpiry(t, ea, "doc"), clk.Now().Add(day); !got.Equal(want) {
		t.Fatalf("deadline = %v, want now+TTL = %v", got, want)
	}
	if got := inner.log(); len(got) != 1 || got[0] != "expiry:doc" {
		t.Fatalf("Touch wrote %v, want only the record", got)
	}
	after, _ := inner.Load(ctx, "doc")
	if string(after.Data) != "v1" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("the document was modified: %q %v -> %q %v", before.Data, before.UpdatedAt, after.Data, after.UpdatedAt)
	}
	clk.Add(2 * time.Hour) // t = 25h: past the original deadline
	if _, err := ea.Load(ctx, "doc"); err != nil {
		t.Fatalf("a touched document must live on: %v", err)
	}
	clk.Add(23 * time.Hour) // t = 48h: past the renewed one
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after the renewed deadline = %v, want ErrNotFound", err)
	}
}

func TestTouchRefusesMissingAndExpiredDocuments(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	if err := ea.Touch(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch(missing) = %v, want ErrNotFound", err)
	}
	if _, ok, _ := ea.Expiry(ctx, "nope"); ok {
		t.Fatal("Touch of a missing document created a deadline record")
	}
	if err := ea.Save(ctx, "doc", []byte("x")); err != nil {
		t.Fatal(err)
	}
	clk.Add(day) // exactly at the deadline: expired
	rec, _, _ := ea.Expiry(ctx, "doc")
	if err := ea.Touch(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch(expired, not yet swept) = %v, want ErrNotFound", err)
	}
	if got, _, _ := ea.Expiry(ctx, "doc"); !got.Equal(rec) {
		t.Fatalf("Touch moved the deadline of a dead document: %v -> %v", rec, got)
	}
	if _, err := ea.Load(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired document came back to life: %v", err)
	}
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v; want the dead document deleted", n, err)
	}
	if err := ea.Touch(ctx, "doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch(swept) = %v, want ErrNotFound", err)
	}
}

func TestTouchIgnoresAnOrphanRecord(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	// The crash leftover: a record whose document is missing.
	if err := inner.Save(ctx, "expiry:ghost", []byte(clk.Now().Add(day).Format(time.RFC3339Nano))); err != nil {
		t.Fatal(err)
	}
	if err := ea.Touch(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch(record without document) = %v, want ErrNotFound", err)
	}
}

func TestTouchOrderAndFailure(t *testing.T) {
	ctx := context.Background()
	inner := &recordingAdapter{Adapter: NewMemoryAdapter()}
	ea, clk := newExpiring(inner)
	if err := ea.Save(ctx, "doc", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	old := mustExpiry(t, ea, "doc")
	clk.Add(time.Hour)

	// A failure writing the record: the old deadline stands, the document is intact, the
	// error is reported (so the caller retries).
	inner.failOn = "expiry:doc"
	inner.reset()
	if err := ea.Touch(ctx, "doc"); err == nil {
		t.Fatal("Touch hid a failed write")
	}
	if got := mustExpiry(t, ea, "doc"); !got.Equal(old) {
		t.Fatalf("a failed Touch changed the deadline: %v -> %v", old, got)
	}
	if d, err := ea.Load(ctx, "doc"); err != nil || string(d.Data) != "v1" {
		t.Fatalf("document after a failed Touch: %q, %v", d.Data, err)
	}
	// Crash after the record and before "the document" cannot hurt: Touch has no document
	// write at all, so the only state it can leave is old or new deadline, both with a live
	// document; and a document is never present without a record.
	inner.failOn = ""
	inner.reset()
	if err := ea.Touch(ctx, "doc"); err != nil {
		t.Fatal(err)
	}
	for _, k := range inner.log() {
		if !strings.HasPrefix(k, ExpiryPrefix) {
			t.Fatalf("Touch wrote %q, which is not a deadline record", k)
		}
	}
	if got := mustExpiry(t, ea, "doc"); !got.After(old) {
		t.Fatalf("deadline did not move: %v -> %v", old, got)
	}
}

func TestTouchPolicyAndKeyspaces(t *testing.T) {
	ctx := context.Background()
	ea, _ := newExpiring(NewMemoryAdapter())
	if err := ea.Touch(ctx, "expiry:doc"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch in the reserved keyspace = %v, want a refusal", err)
	}
	if err := ea.Touch(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Touch(\"\") = %v", err)
	}
	// A system key lives until deleted: Touch is a no-op that gives it no deadline.
	if err := ea.Save(ctx, "collab:x", []byte("[]")); err != nil {
		t.Fatal(err)
	}
	if err := ea.Touch(ctx, "collab:x"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ea.Expiry(ctx, "collab:x"); ok {
		t.Fatal("a system key got a deadline")
	}
	// A document without a record (written before the store was wrapped) is live and is
	// given a full lifetime.
	inner := NewMemoryAdapter()
	_ = inner.Save(ctx, "old", []byte("x"))
	ea2, clk := newExpiring(inner)
	if err := ea2.Touch(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	if got := mustExpiry(t, ea2, "old"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("deadline = %v", got)
	}
}

func TestTouchRenewsTheSaversGenerationKeys(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	if err := ea.Save(ctx, "d~1f2e3d4c5b6a.docx", []byte("g1")); err != nil {
		t.Fatal(err)
	}
	if err := ea.Save(ctx, "dd.docx", []byte("other")); err != nil {
		t.Fatal(err)
	}
	clk.Add(10 * time.Hour)
	if err := ea.Touch(ctx, "d"); err != nil { // the hub knows the logical id
		t.Fatal(err)
	}
	if got := mustExpiry(t, ea, "d~1f2e3d4c5b6a.docx"); !got.Equal(clk.Now().Add(day)) {
		t.Fatalf("generation key deadline = %v", got)
	}
	if got := mustExpiry(t, ea, "dd.docx"); !got.Equal(clk.Now().Add(-10 * time.Hour).Add(day)) {
		t.Fatalf("an unrelated document with the same prefix was renewed: %v", got)
	}
}

func TestSweepRemovesHubStateWithItsDocument(t *testing.T) {
	ctx := context.Background()
	inner := NewMemoryAdapter()
	ea, clk := newExpiring(inner)
	for _, id := range []string{"doc", "other"} {
		if err := ea.Save(ctx, id, []byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := ea.Save(ctx, "collab:"+id, []byte("[]")); err != nil { // what the hub writes
			t.Fatal(err)
		}
	}
	clk.Add(23 * time.Hour)
	if err := ea.Touch(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	clk.Add(2 * time.Hour)
	if n, err := ea.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if _, err := inner.Load(ctx, "collab:doc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collab:doc survived its document: %v", err)
	}
	if _, err := inner.Load(ctx, "collab:other"); err != nil {
		t.Fatalf("collab:other of a live document was removed: %v", err)
	}
	// Deleting a document directly takes the hub state with it too.
	if err := ea.Delete(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.Load(ctx, "collab:other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("collab:other survived Delete: %v", err)
	}
}

func TestTouchRaceWithSaveAndSweep(t *testing.T) {
	ctx := context.Background()
	ea, clk := newExpiring(NewMemoryAdapter())
	for _, id := range []string{"a", "b", "c"} {
		_ = ea.Save(ctx, id, []byte("x"))
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = ea.Touch(ctx, "a")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = ea.Save(ctx, "b", []byte("y"))
				_ = ea.Touch(ctx, "c")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				clk.Add(time.Minute)
				_, _ = ea.Sweep(ctx)
			}
		}()
	}
	wg.Wait()
}

// ---- POST /docs/{id}/renew

func renewReq(t *testing.T, h http.Handler, id, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/docs/"+id+"/renew", nil)
	if token != "" {
		withBearer(req, token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRenewRouteNeedsWriteRole(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	ea, clk := newExpiring(NewMemoryAdapter())
	srv.WithExpiry(ea)
	h := srv.Handler()
	owner := sessionTokenFor(t, h, provider, "owner@example.com", "o")
	editor := sessionTokenFor(t, h, provider, "editor@example.com", "e")
	viewer := sessionTokenFor(t, h, provider, "viewer@example.com", "v")
	stranger := sessionTokenFor(t, h, provider, "stranger@example.com", "s")

	for _, seed := range []*http.Request{
		withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1", strings.NewReader("v1")), owner),
		withBearer(httptest.NewRequest(http.MethodPut, "/docs/doc1/acl", strings.NewReader(`{"grants":{"editor@example.com":"editor","viewer@example.com":"viewer"}}`)), owner),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, seed)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("seed %s = %d", seed.URL.Path, rec.Code)
		}
	}
	seeded := mustExpiry(t, ea, "doc1")

	clk.Add(time.Hour)
	for _, tc := range []struct {
		name, token string
		want        int
		moves       bool
	}{
		{"anonymous", "", http.StatusUnauthorized, false},
		{"stranger", stranger, http.StatusForbidden, false},
		{"viewer", viewer, http.StatusForbidden, false},
		{"editor", editor, http.StatusNoContent, true},
		{"owner", owner, http.StatusNoContent, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := mustExpiry(t, ea, "doc1")
			rec := renewReq(t, h, "doc1", tc.token)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
			after := mustExpiry(t, ea, "doc1")
			if tc.moves && !after.Equal(clk.Now().Add(day)) {
				t.Fatalf("deadline %v -> %v, want now+TTL", before, after)
			}
			if !tc.moves && !after.Equal(before) {
				t.Fatalf("a refused renewal moved the deadline: %v -> %v", before, after)
			}
			if tc.moves && rec.Header().Get("X-Gojiberry-Expires") == "" {
				t.Fatal("no X-Gojiberry-Expires")
			}
		})
		clk.Add(time.Minute)
	}
	if !mustExpiry(t, ea, "doc1").After(seeded) {
		t.Fatal("authorized renewals did not move the deadline")
	}
	// Not found / expired, reserved ids, wrong method.
	if rec := renewReq(t, h, "nothere", owner); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("renew of an unknown document = %d", rec.Code)
	}
	for _, id := range []string{"collab:doc1", "acl:doc1", "expiry:doc1"} {
		if rec := renewReq(t, h, id, owner); rec.Code != http.StatusBadRequest {
			t.Fatalf("renew of reserved %q = %d, want 400", id, rec.Code)
		}
	}
	get := withBearer(httptest.NewRequest(http.MethodGet, "/docs/doc1/renew", nil), owner)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET renew = %d, want 405", rec.Code)
	}
	clk.Add(2 * day)
	if rec := renewReq(t, h, "doc1", owner); rec.Code != http.StatusNotFound {
		t.Fatalf("renew of an expired document = %d, want 404", rec.Code)
	}
}

func TestRenewRouteOnAnOpenDocumentAndNoExpiry(t *testing.T) {
	// No auth at all (as for a write): open. A store that does not expire: 501.
	ea, _ := newExpiring(NewMemoryAdapter())
	_ = ea.Save(context.Background(), "d", []byte("x"))
	h := NewServer(ea, "memory").Handler()
	if rec := renewReq(t, h, "d", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("open renew = %d", rec.Code)
	}
	if rec := renewReq(t, h, "missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("renew of a missing document = %d", rec.Code)
	}
	plain := NewServer(NewMemoryAdapter(), "memory").Handler()
	if rec := renewReq(t, plain, "d", ""); rec.Code != http.StatusNotImplemented {
		t.Fatalf("renew without expiry = %d, want 501", rec.Code)
	}
}

func TestRenewRouteWithRequireACLNeverBootstrapsAnACL(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL).WithRequireACL(true)
	ea, _ := newExpiring(NewMemoryAdapter())
	srv.WithExpiry(ea)
	h := srv.Handler()
	tok := sessionTokenFor(t, h, provider, "a@example.com", "a")
	_ = ea.Save(context.Background(), "d", []byte("x")) // a document nobody owns
	if rec := renewReq(t, h, "d", tok); rec.Code != http.StatusForbidden {
		t.Fatalf("renew of an unowned document with RequireACL = %d, want 403", rec.Code)
	}
	if _, err := ea.Load(context.Background(), "acl:d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renew created an ACL: %v", err)
	}
}

func TestHTTPAdapterRenewForwardsTheCallersAuthorization(t *testing.T) {
	var gotAuth, gotTok, gotPath, gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotTok, gotPath, gotMethod = r.Header.Get("Authorization"), r.Header.Get("X-Gojiberry-Token"), r.URL.Path, r.Method
		if strings.Contains(r.URL.Path, "gone") {
			w.Header().Set(GoneHeader, "1")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"gone"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	a := NewHTTPAdapter(ts.URL, nil)
	a.Header.Set("X-Gojiberry-Token", "svc")
	if err := a.Renew(WithAuthorization(context.Background(), "Bearer abc"), "d"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer abc" || gotTok != "svc" || gotPath != "/docs/d/renew" || gotMethod != http.MethodPost {
		t.Fatalf("request: %s %s auth=%q token=%q", gotMethod, gotPath, gotAuth, gotTok)
	}
	if err := a.Renew(context.Background(), "gone"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 = %v, want ErrNotFound", err)
	}
}

func TestWithConfiguredExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := NewMemoryAdapter()
	if got, ea := WithConfiguredExpiry(ctx, inner, Config{}); ea != nil || got != Adapter(inner) {
		t.Fatalf("expiry off: %v %v", got, ea)
	}
	got, ea := WithConfiguredExpiry(ctx, inner, Config{ExpiryTTL: time.Hour, ExpirySweep: time.Minute, ExpiryAdoptUpdated: true})
	if ea == nil || got != Adapter(ea) || !ea.AdoptUpdatedAt {
		t.Fatalf("expiry on: %v %v", got, ea)
	}
	var _ Renewer = ea // what collabhub passes to the hub
}

func TestOnSaveSeesSavesAndConditionalSavesNotTouches(t *testing.T) {
	ctx := context.Background()
	ea := NewExpiringAdapter(NewMemoryAdapter(), FixedTTL(time.Hour))
	type saved struct {
		id   string
		size int
	}
	var got []saved
	ea.OnSave = func(id string, size int) { got = append(got, saved{id, size}) }
	if err := ea.Save(ctx, "a", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if err := ea.SaveIf(ctx, "b", []byte("123"), ""); err != nil {
		t.Fatal(err)
	}
	if err := ea.SaveIf(ctx, "b", []byte("1"), "wrong digest"); err == nil {
		t.Fatal("conflict expected")
	}
	if err := ea.Touch(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (saved{"a", 5}) || got[1] != (saved{"b", 3}) {
		t.Fatalf("OnSave saw %v, want the two successful writes", got)
	}
}
