package encrypt

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func TestUnmappedAddressesListAndMend(t *testing.T) {
	ctx := context.Background()
	e := newOwnerEnv(t)
	crashAfterEnroll(t, e, false)
	e.clk.Add(reconcileGrace + time.Second)
	u, err := e.d.UnmappedAddresses(ctx)
	if err != nil || u.Count != 1 || len(u.Addresses) != 1 || u.Truncated || u.ComputedAt != e.clk.Now().Unix() {
		t.Fatalf("%+v %v", u, err)
	}
	a := u.Addresses[0]
	if a.Address != e.d.LogAddressFor("ana@x.io") || a.FirstSeq != 1 || a.LastSeq != 1 || a.AgeSeconds < int64(reconcileGrace/time.Second) {
		t.Errorf("%+v", a)
	}
	if b, _ := json.Marshal(u); strings.Contains(string(b), "ana") {
		t.Errorf("no e-mail in the list: %s", b)
	}
	if _, err := e.d.RequestReset(ctx, ana); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.d.UnmappedAddresses(ctx); u.Count != 0 {
		t.Errorf("mended by the owner: %+v", u)
	}

	e2 := newOwnerEnv(t)
	crashAfterEnroll(t, e2, false)
	e2.clk.Add(reconcileGrace + time.Second)
	if u, _ := e2.d.UnmappedAddresses(ctx); u.Count != 1 {
		t.Fatalf("%+v", u)
	}
	if _, err := e2.d.OperatorResetEnrollment(ctx, e2.d.LogAddressFor("ana@x.io")); err != nil {
		t.Fatal(err)
	}
	if u, _ := e2.d.UnmappedAddresses(ctx); u.Count != 0 {
		t.Errorf("reset by the operator: %+v", u)
	}
}

func TestUnmappedAddressesAreCapped(t *testing.T) {
	d, _, clk := newTestDirectory(t)
	d.UnmappedCap = 3
	d.idxMu.Lock()
	d.idx.byAddr = map[string][]LogEntry{}
	d.idx.scanned = 0
	for i := int64(1); i <= 7; i++ {
		a := strings.Repeat(string(rune('a'+i)), 64)
		d.idx.byAddr[a] = []LogEntry{{Seq: i, At: clk.Now().Unix(), Op: OpEnroll, Address: a}}
	}
	d.idxMu.Unlock()
	u, err := d.UnmappedAddresses(context.Background())
	if err != nil || u.Count != 7 || len(u.Addresses) != 3 || !u.Truncated || u.Addresses[0].FirstSeq != 1 || u.Addresses[2].FirstSeq != 3 {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestUnmappedWarningOncePerChange(t *testing.T) {
	ctx := context.Background()
	e := newOwnerEnv(t)
	buf := captureLog(t)
	warnings := func() int { return strings.Count(buf.String(), "WARNING") }
	if _, err := e.d.reconcileAll(ctx); err != nil || warnings() != 0 {
		t.Fatalf("nothing unmapped, nothing logged: %q", buf)
	}
	crashAfterEnroll(t, e, false)
	e.clk.Add(reconcileGrace + time.Second)
	for range 3 {
		if _, err := e.d.reconcileAll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if warnings() != 1 || !strings.Contains(buf.String(), "1 address pseudonyms") || !strings.Contains(buf.String(), "/admin/directory/unmapped") {
		t.Fatalf("once for the same count: %q", buf)
	}
	e.d.idxMu.Lock()
	e.d.idx.byAddr[strings.Repeat("c", 64)] = []LogEntry{{Seq: 99, Op: OpEnroll}}
	e.d.idxMu.Unlock()
	for range 2 {
		_, _ = e.d.reconcileAll(ctx)
	}
	if warnings() != 2 || !strings.Contains(buf.String(), "2 address pseudonyms") {
		t.Fatalf("again when the count changes: %q", buf)
	}
	if strings.Contains(buf.String(), "ana") {
		t.Errorf("no e-mail in the log: %q", buf)
	}
}

func TestUnmappedRouteIsOperatorOnly(t *testing.T) {
	var d *AdapterDirectory
	e := newDirEnvWith(t, func(x *AdapterDirectory) { d = x }, func(s *docstore.Server) {
		s.WithDirectoryAdmin(func(r *http.Request) bool { return r.Header.Get("X-Admin") == "yes" })
	})
	d.idxMu.Lock()
	d.idx.byAddr = map[string][]LogEntry{strings.Repeat("d", 64): {{Seq: 4, At: 1, Op: OpEnroll}}}
	d.idxMu.Unlock()
	get := func(admin bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/admin/directory/unmapped", nil)
		if admin {
			req.Header.Set("X-Admin", "yes")
		}
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	var got docstore.DirectoryUnmapped
	rec := get(true)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Count != 1 || got.Addresses[0].FirstSeq != 4 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("POST", "/admin/directory/unmapped", "", ""); rec.Code != 405 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("POST: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	for range 10 {
		if r := get(false); r.Code != 403 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("a failure inside the budget: %d %q", r.Code, r.Header().Get("Cache-Control"))
		}
	}
	if r := get(false); r.Code != 429 || r.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the failure budget applies: %d %q", r.Code, r.Header().Get("Cache-Control"))
	}
}
