package docstore

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/audit"
)

func auditLines(t *testing.T, buf *bytes.Buffer) []audit.Event {
	t.Helper()
	var out []audit.Event
	for _, ln := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if ln == "" {
			continue
		}
		var e audit.Event
		if err := json.Unmarshal([]byte(ln), &e); err != nil {
			t.Fatalf("an audit line is not an event: %q", ln)
		}
		out = append(out, e)
	}
	return out
}

func kindsOf(es []audit.Event) (ks []audit.Kind) {
	for _, e := range es {
		ks = append(ks, e.Kind)
	}
	return
}

// A document's life is in the log: created (a new document owned by whoever saved it first), shared, unshared
// and deleted, each with the class of the one who did it and a Ref, and never a name, an address or the id.
func TestAuditLogFollowsADocumentWithoutNamingIt(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL).WithRequireACL(true)
	var buf bytes.Buffer
	srv.WithAudit(audit.NewLogger(&buf), []byte("an operator's audit key"))
	handler := srv.Handler()
	owner := sessionTokenFor(t, handler, provider, "owner@example.com", "owner-sub")
	do := func(method, path, body string) int {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, withBearer(httptest.NewRequest(method, path, strings.NewReader(body)), owner))
		return rec.Code
	}
	if do("PUT", "/docs/secret-plans", "v1") != http.StatusNoContent {
		t.Fatal("first save")
	}
	if do("PUT", "/docs/secret-plans/acl", `{"grants":{"friend@example.com":"editor","other@example.com":"viewer"}}`) != http.StatusNoContent {
		t.Fatal("share")
	}
	if do("PUT", "/docs/secret-plans/acl", `{"grants":{"friend@example.com":"editor"}}`) != http.StatusNoContent {
		t.Fatal("unshare one")
	}
	if do("DELETE", "/docs/secret-plans", "") != http.StatusNoContent {
		t.Fatal("delete")
	}
	events := auditLines(t, &buf)
	want := []audit.Kind{audit.DocumentCreated, audit.AccessGranted, audit.AccessRevoked, audit.DocumentDeleted}
	if got := kindsOf(events); len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i, k := range want {
		if events[i].Kind != k || events[i].Outcome != audit.OK || events[i].Ref == "" {
			t.Errorf("event %d = %+v, want %s ok with a Ref", i, events[i], k)
		}
	}
	if events[1].Count != 2 || events[2].Count != 1 {
		t.Errorf("counts: %d granted, %d revoked", events[1].Count, events[2].Count)
	}
	if events[1].Actor != audit.Owner || events[3].Actor != audit.Owner {
		t.Errorf("actors: %s, %s", events[1].Actor, events[3].Actor)
	}
	if events[0].Ref != events[3].Ref || events[0].Ref != audit.Pseudonym([]byte("an operator's audit key"), "secret-plans") {
		t.Error("one document, one Ref, from the key")
	}
	for _, leak := range []string{"secret-plans", "example.com", "owner-sub", "friend", "v1"} {
		if strings.Contains(buf.String(), leak) {
			t.Errorf("the log holds %q:\n%s", leak, buf.String())
		}
	}
	if srv.auditLog.Rejected() != 0 {
		t.Error("an event was off the allowlist")
	}
}

func TestAuditLogIsOffUnlessAsked(t *testing.T) {
	srv := NewServer(NewMemoryAdapter(), "memory")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/docs/a", strings.NewReader("x"))
	req.Header.Set("If-None-Match", "*")
	srv.Handler().ServeHTTP(rec, req) // no logger: nothing to write, and no panic
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d", rec.Code)
	}
	var buf bytes.Buffer
	srv.WithAudit(audit.NewLogger(&buf), nil) // no key: lines carry no Ref
	req = httptest.NewRequest(http.MethodPut, "/docs/b", strings.NewReader("x"))
	req.Header.Set("If-None-Match", "*")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if es := auditLines(t, &buf); len(es) != 1 || es[0].Kind != audit.DocumentCreated || es[0].Ref != "" || es[0].Actor != audit.Anonymous {
		t.Fatalf("events = %+v", es)
	}
}

func TestAuditLogCountsExpiredDocumentsAndRefusedRequests(t *testing.T) {
	var buf bytes.Buffer
	l := audit.NewLogger(&buf)
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	exp := NewExpiringAdapter(NewMemoryAdapter(), FixedTTL(time.Hour))
	exp.Now = func() time.Time { return now }
	NewServer(exp, "memory").WithAudit(l, []byte("k"))
	if err := exp.Save(context.Background(), "old", []byte("x")); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if n, err := exp.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	es := auditLines(t, &buf)
	if len(es) != 1 || es[0].Kind != audit.DocumentExpired || es[0].Count != 1 || es[0].Actor != audit.System {
		t.Fatalf("events = %+v", es)
	}

	// A limiter that refuses logs the refusal, with no key or client.
	buf.Reset()
	f := AuditLimiters(func(string, float64, int) Limiter { return refuseAll{} }, l)
	if ok, _ := f("dir-publish", 1, 1).Allow(context.Background(), "client-a"); ok {
		t.Fatal("refused")
	}
	es = auditLines(t, &buf)
	if len(es) != 1 || es[0].Kind != audit.RateLimited || es[0].Reason != audit.OverRate || strings.Contains(buf.String(), "client-a") {
		t.Fatalf("events = %+v\n%s", es, buf.String())
	}
}

type refuseAll struct{}

func (refuseAll) Allow(context.Context, string) (bool, time.Duration) { return false, time.Second }

// Enrolling an owner key and asking for a reset are logged, by the account's Ref and never its address.
func TestAuditLogRecordsOwnerKeyEvents(t *testing.T) {
	var buf bytes.Buffer
	d := newDirRoutes(t, func(s *Server) { s.WithAudit(audit.NewLogger(&buf), []byte("an operator's audit key")) })
	if rec := d.do("POST", "/directory/enroll", &ana, `{"first":"x"}`); rec.Code != 200 {
		t.Fatalf("enroll: %d", rec.Code)
	}
	if rec := d.do("POST", "/directory/enroll/reset", &ana, ""); rec.Code != 200 {
		t.Fatalf("reset: %d", rec.Code)
	}
	es := auditLines(t, &buf)
	if got := kindsOf(es); len(got) != 2 || got[0] != audit.KeyEnrolled || got[1] != audit.OwnerReset {
		t.Fatalf("events = %v", got)
	}
	if es[0].Ref == "" || es[0].Ref != es[1].Ref || strings.Contains(buf.String(), "example") {
		t.Errorf("the same account, the same Ref, no address: %s", buf.String())
	}
}

// A sign-in that fails is logged as a refusal with no provider, account or address; one that works is not.
func TestAuditLogRecordsFailedSignIns(t *testing.T) {
	provider := newFakeOIDCProvider(t)
	srv := newTestServerWithAuth(t, provider.srv.URL)
	var buf bytes.Buffer
	srv.WithAudit(audit.NewLogger(&buf), []byte("an operator's audit key"))
	handler := srv.Handler()
	_ = sessionTokenFor(t, handler, provider, "ana@example.com", "ana-sub") // works: nothing logged
	if got := strings.TrimSpace(buf.String()); got != "" {
		t.Fatalf("a good sign-in was logged: %s", got)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=x&state=forged-by-ana@example.com", nil))
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("a forged state should be a client error, got %d", rec.Code)
	}
	es := auditLines(t, &buf)
	if len(es) != 1 || es[0].Kind != audit.SignInFailed || es[0].Outcome != audit.Denied || es[0].Actor != audit.Anonymous || es[0].Ref != "" {
		t.Fatalf("events = %+v", es)
	}
	if strings.Contains(buf.String(), "ana") || strings.Contains(buf.String(), "forged") || strings.Contains(buf.String(), "test") {
		t.Errorf("the line carries something of the request: %s", buf.String())
	}
	// Other routes' errors are not sign-in failures.
	buf.Reset()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/docs/missing", nil))
	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("logged: %s", buf.String())
	}
}
