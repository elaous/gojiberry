package docstorecmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/public"
)

type failingList struct {
	docstore.Adapter
	broken bool
}

var errListDown = errors.New("list is down")

func (f *failingList) List(ctx context.Context) ([]docstore.Meta, error) {
	if f.broken {
		return nil, errListDown
	}
	return f.Adapter.List(ctx)
}

func TestBackupAndRestoreReportAnAdapterThatCannotList(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := &failingList{Adapter: docstore.NewMemoryAdapter(), broken: true}

	file := filepath.Join(dir, "store.backup")
	_, code, out, errOut := run(t, a, "backup", file)
	if code != 1 || out != "" || !strings.Contains(errOut, "backup failed") || !strings.Contains(errOut, errListDown.Error()) ||
		!strings.Contains(errOut, `"kind":"backup.made"`) || !strings.Contains(errOut, `"outcome":"error"`) {
		t.Fatalf("backup: code %d out %q err %q", code, out, errOut)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("a failed backup left a file under the final name: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".backup-*")); len(left) != 0 {
		t.Errorf("temp file left after a failed backup: %v", left)
	}

	good := docstore.NewMemoryAdapter()
	_ = good.Save(ctx, "one", []byte("data"))
	if _, err := backupToFile(ctx, good, file); err != nil {
		t.Fatal(err)
	}
	if n, err := restoreFromFile(ctx, a, file, false); !errors.Is(err, errListDown) || n != 0 {
		t.Fatalf("restore: %d %v", n, err)
	}
	_, code, _, errOut = run(t, a, "restore", file)
	if code != 1 || !strings.Contains(errOut, "restore failed") || !strings.Contains(errOut, `"reason":"unavailable"`) {
		t.Fatalf("restore command: %d %q", code, errOut)
	}
}

func TestBackupToFileCannotReplaceADirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(filepath.Join(target, "inside"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := docstore.NewMemoryAdapter()
	_ = a.Save(ctx, "x", []byte("y"))
	if _, err := backupToFile(ctx, a, target); err == nil {
		t.Fatal("renaming a file over a non-empty directory succeeded")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".backup-*")); len(left) != 0 {
		t.Errorf("temp file left: %v", left)
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		t.Errorf("the directory was disturbed: %v %v", st, err)
	}
}

func TestWithDirectoryRejectsBadSettings(t *testing.T) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	build := func() (*docstore.Server, docstore.Adapter) {
		a := docstore.NewMemoryAdapter()
		return docstore.NewServer(a, "memory").WithAuth(mgr), a
	}
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")

	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", "not hex")
	if srv, a := build(); withDirectory(ctx, srv, a, docstore.MemoryLimiters()) == nil {
		t.Error("a key that is not hex was accepted")
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))

	for _, w := range []string{"soon", "0", "-1h"} {
		t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW", w)
		srv, a := build()
		if err := withDirectory(ctx, srv, a, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "REVOKE_WINDOW") {
			t.Errorf("revoke window %q: %v", w, err)
		}
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW", "48h")
	if srv, a := build(); withDirectory(ctx, srv, a, docstore.MemoryLimiters()) != nil {
		t.Error("a valid revoke window was refused")
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_REVOKE_WINDOW", "")

	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP", "everyone")
	if srv, a := build(); withDirectory(ctx, srv, a, docstore.MemoryLimiters()) == nil {
		t.Error("an unknown lookup mode was accepted")
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP", "domain")
	if srv, a := build(); withDirectory(ctx, srv, a, docstore.MemoryLimiters()) != nil {
		t.Error("lookup=domain was refused")
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_LOOKUP", "")
}

func TestWithDirectoryAdminTokenAndProxies(t *testing.T) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	a := docstore.NewMemoryAdapter()

	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN", "too short")
	if err := withDirectory(ctx, docstore.NewServer(a, "memory").WithAuth(mgr), a, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "16 characters") {
		t.Fatalf("short admin token: %v", err)
	}

	const tok = "operator-token-0123456789"
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_ADMIN_TOKEN", " "+tok+" ") // surrounding blanks are trimmed
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES", "10.0.0.0/8")
	srv := docstore.NewServer(a, "memory").WithAuth(mgr)
	if err := withDirectory(ctx, srv, a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	usage := func(token string) int {
		req := httptest.NewRequest("GET", "/admin/usage", nil)
		if token != "" {
			req.Header.Set("X-Gojiberry-Admin-Token", token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := usage(""); c != http.StatusForbidden {
		t.Errorf("admin route without the token = %d, want 403", c)
	}
	if c := usage("wrong-token-0123456789"); c != http.StatusForbidden {
		t.Errorf("admin route with a wrong token = %d, want 403", c)
	}
	if c := usage(tok); c != http.StatusOK {
		t.Errorf("admin route with the right token = %d, want 200", c)
	}
}

func TestWithDirectoryReportsAStoreThatCannotBeListed(t *testing.T) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")
	a := &failingList{Adapter: docstore.NewMemoryAdapter(), broken: true}
	logs := captureLog(t)
	err = withDirectory(ctx, docstore.NewServer(a, "plain").WithAuth(mgr), a, docstore.MemoryLimiters())
	if err == nil || !strings.Contains(err.Error(), "start-up check") || !errors.Is(err, errListDown) {
		t.Fatalf("withDirectory = %v", err)
	}
	if !strings.Contains(logs.String(), "could not look for other server processes") {
		t.Errorf("the failed look for other writers is not logged: %q", logs)
	}
}

func TestCheckWritersSaysWhatTheDeclarationLeavesOpen(t *testing.T) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "")
	plain := plainAdapter{docstore.NewMemoryAdapter()}
	logs := captureLog(t)
	if err := withDirectory(ctx, docstore.NewServer(plain, "plain").WithAuth(mgr), plain, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"REPLICAS is not set", "no conditional writes", "fork the directory's log"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("lacks %q: %q", want, logs)
		}
	}
}

func TestWithPublicRejectsBadModes(t *testing.T) {
	for _, env := range []string{
		"GOJIBERRY_DOCSTORE_PUBLIC_QUOTA",
		"GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS",
		"GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS",
	} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("GOJIBERRY_DOCSTORE_PUBLIC", "true")
			t.Setenv(env, "everywhere")
			a := expiring()
			srv := docstore.NewServer(a, "memory")
			svc, err := withPublic(srv, a, 24*time.Hour, docstore.MemoryLimiters())
			if err == nil || !strings.Contains(err.Error(), env) || svc != nil {
				t.Fatalf("withPublic = %v, %v", svc, err)
			}
			if c := status(srv.Handler(), "POST", "/public/session"); c != http.StatusNotFound {
				t.Errorf("/public/session = %d after a refused start", c)
			}
		})
	}
}

func TestPublicSharedModesFallBackOrRefuseWithoutConditionalWrites(t *testing.T) {
	ctx := context.Background()
	plain := plainAdapter{docstore.NewMemoryAdapter()}
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")
	calls := []struct {
		name string
		env  string
		call func(mode string) (any, error)
	}{
		{"quota", "GOJIBERRY_DOCSTORE_PUBLIC_QUOTA", func(m string) (any, error) { q, err := publicQuota(ctx, plain, 100, m); return q, err }},
		{"sessions", "GOJIBERRY_DOCSTORE_PUBLIC_SESSIONS", func(m string) (any, error) { s, err := publicSessions(ctx, plain, 100, m); return s, err }},
		{"priority", "GOJIBERRY_DOCSTORE_PUBLIC_REPORT_COUNTERS", func(m string) (any, error) { q, err := publicPriorityQuota(ctx, plain, 5, m); return q, err }},
	}
	for _, c := range calls {
		t.Run(c.name, func(t *testing.T) {
			logs := captureLog(t)
			got, err := c.call(" Store ")
			if err != nil || !isNil(got) {
				t.Fatalf("store fell back to %v, %v", got, err)
			}
			if !strings.Contains(logs.String(), "NOT SHARED BY REPLICAS") || !strings.Contains(logs.String(), c.env+"=store-required") {
				t.Errorf("warning: %q", logs)
			}
			if _, err := c.call("store-required"); err == nil || !strings.Contains(err.Error(), c.env) {
				t.Errorf("store-required = %v", err)
			}
			logs.Reset()
			if got, err := c.call("memory"); err != nil || !isNil(got) || strings.Contains(logs.String(), "WARNING") {
				t.Errorf("memory: %v %v %q", got, err, logs)
			}
			t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "3")
			logs.Reset()
			if got, err := c.call(""); err != nil || !isNil(got) || !strings.Contains(logs.String(), "GOJIBERRY_DOCSTORE_REPLICAS=3") || !strings.Contains(logs.String(), c.env+"=store") {
				t.Errorf("memory with replicas: %v %v %q", got, err, logs)
			}
		})
	}
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}

func TestPublicPriorityQuotaDefaultsANonPositiveLimit(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	for limit, want := range map[int]int64{0: int64(public.DefaultPriorityPerDay), -1: int64(public.DefaultPriorityPerDay), 7: 7} {
		q, err := publicPriorityQuota(ctx, store, limit, "store")
		sq, ok := q.(*public.StoreQuota)
		if err != nil || !ok || sq.Limit != want {
			t.Fatalf("limit %d: %#v %v, want a StoreQuota with limit %d", limit, q, err, want)
		}
	}
}

func TestPasskeysRejectsBadSettings(t *testing.T) {
	ctx := context.Background()
	a := docstore.NewMemoryAdapter()
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_RPID", "opendocs.io")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS", " https://opendocs.io , ,")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", "everywhere")
	if _, err := withPasskeys(ctx, docstore.NewServer(a, "memory"), a, nil, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "PASSKEY_CHALLENGES") {
		t.Fatalf("bad challenge mode: %v", err)
	}

	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", "")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS", "https://opendocs.io")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.0.0/16")
	srv := docstore.NewServer(a, "memory")
	m, err := withPasskeys(ctx, srv, a, nil, docstore.MemoryLimiters())
	if err != nil || m == nil {
		t.Fatalf("withPasskeys = %v, %v", m, err)
	}
	if c := status(srv.Handler(), "POST", "/auth/passkey/login/begin"); c == http.StatusNotFound {
		t.Errorf("the passkey routes are not mounted: %d", c)
	}
}

func TestWithSessionsRejectsBadSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://opendocs.io"})
	if err != nil {
		t.Fatal(err)
	}
	a := docstore.NewMemoryAdapter()
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "store")
	for _, bad := range []string{"soon", "-1s", "2m"} {
		t.Setenv("GOJIBERRY_DOCSTORE_SESSION_CACHE", bad)
		if err := withSessions(ctx, a, m, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "SESSION_CACHE") {
			t.Errorf("cache %q: %v", bad, err)
		}
	}
	for _, good := range []string{"0", "5s", "1m"} {
		t.Setenv("GOJIBERRY_DOCSTORE_SESSION_CACHE", good)
		logs := captureLog(t)
		if err := withSessions(ctx, a, m, docstore.MemoryLimiters()); err != nil {
			t.Errorf("cache %q: %v", good, err)
		}
		if said := strings.Contains(logs.String(), "cached in each process"); said != (good != "0") {
			t.Errorf("cache %q: said it is cached = %v: %q", good, said, logs)
		}
		if !strings.Contains(logs.String(), "shared through the store") {
			t.Errorf("cache %q: %q", good, logs)
		}
	}
}

func TestWithSessionsMissLimitIsOptIn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES", "10.0.0.0/8")
	current := func(m *auth.Manager, remote, token string) bool {
		req := httptest.NewRequest("GET", "/auth/me", nil)
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer "+token)
		_, ok := m.CurrentUser(req)
		return ok
	}
	flood := func(t *testing.T, on bool) (valid, other bool) {
		t.Helper()
		if on {
			t.Setenv("GOJIBERRY_DOCSTORE_AUTH_MISS_LIMIT", "true")
		} else {
			t.Setenv("GOJIBERRY_DOCSTORE_AUTH_MISS_LIMIT", "")
		}
		m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://opendocs.io"})
		if err != nil {
			t.Fatal(err)
		}
		logs := captureLog(t)
		if err := withSessions(ctx, docstore.NewMemoryAdapter(), m, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		if said := strings.Contains(logs.String(), "AUTH_MISS_LIMIT=true"); said != on {
			t.Errorf("on=%v but the log says %v: %q", on, said, logs)
		}
		tok, _ := m.StartSession(auth.User{ID: "passkey:1"})
		for i := 0; i < 3*passkey.DefaultBurst; i++ {
			if current(m, "198.51.100.9:1", "not-a-session") {
				t.Fatal("a made-up token was accepted")
			}
		}
		return current(m, "198.51.100.9:2", tok), current(m, "198.51.100.10:2", tok)
	}
	if valid, other := flood(t, false); !valid || !other {
		t.Errorf("off by default: the valid token works for everybody (%v, %v)", valid, other)
	}
	if valid, other := flood(t, true); valid || !other {
		t.Errorf("opted in: the flooding address is cut off (valid=%v), another address is not (%v)", valid, other)
	}
}
