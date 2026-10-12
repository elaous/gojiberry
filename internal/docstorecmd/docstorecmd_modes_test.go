package docstorecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/internal/webauthntest"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An unset GOJIBERRY_DOCSTORE_REPLICAS is said out loud, and a store that shows another writer
// contradicts a declared 1.
func TestReplicasUnsetAndContradictedAreSaidLoudly(t *testing.T) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://x.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_DIRECTORY_KEY", strings.Repeat("11", 32))
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "")
	a := docstore.NewMemoryAdapter()
	logs := captureLog(t)
	if err := withDirectory(ctx, docstore.NewServer(a, "memory").WithAuth(mgr), a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "GOJIBERRY_DOCSTORE_REPLICAS is not set") {
		t.Errorf("unset: %q", logs)
	}
	// Another process (a second start on the same store) is now registered: declared 1 is wrong.
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")
	logs = captureLog(t)
	if err := withDirectory(ctx, docstore.NewServer(a, "memory").WithAuth(mgr), a, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "ERROR") || !strings.Contains(logs.String(), "other server process") {
		t.Errorf("contradiction: %q", logs)
	}
}

func TestPasskeyChallengesModeSharesCeremoniesBetweenReplicas(t *testing.T) {
	ctx := context.Background()
	const rp, origin = "opendocs.io", "https://opendocs.io"
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_RPID", rp)
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS", origin)
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_NAME", "opendocs")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", "store")
	mem := docstore.NewMemoryAdapter()
	replica := func() http.Handler {
		srv := docstore.NewServer(mem, "memory")
		if _, err := withPasskeys(ctx, srv, mem, nil, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		return srv.Handler()
	}
	a, b := replica(), replica()
	post := func(h http.Handler, path string, body []byte) (int, map[string]any) {
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Code, out
	}
	pk := func(begin map[string]any) []byte {
		raw, _ := json.Marshal(map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]})
		return raw
	}
	user := webauthntest.New(rp)
	code, begin := post(a, "/auth/passkey/register/begin", []byte(`{"email":"ana@opendocs.io"}`))
	if code != 200 {
		t.Fatalf("register begin: %d %v", code, begin)
	}
	if metas, _ := docstore.ListPrefix(ctx, mem, passkey.ChallengePrefix); len(metas) != 1 {
		t.Fatalf("the ceremony is in the store: %+v", metas)
	}
	reg, err := user.Create(pk(begin), origin)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := post(b, "/auth/passkey/register/finish?token="+begin["token"].(string), reg); code != 200 {
		t.Fatalf("register begun on A, finished on B: %d %v", code, out)
	}
	code, begin = post(b, "/auth/passkey/login/begin", nil)
	if code != 200 {
		t.Fatal(code)
	}
	ans, err := user.Get(pk(begin), origin)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := post(a, "/auth/passkey/login/finish?token="+begin["token"].(string), ans); code != 200 {
		t.Fatalf("login begun on B, finished on A: %d %v", code, out)
	}
	if code, _ := post(b, "/auth/passkey/login/finish?token="+begin["token"].(string), ans); code != 410 {
		t.Fatalf("replay on the other replica = %d, want 410", code)
	}
}

func TestPasskeyChallengesModeIsCheckedAndFallsBackOrRefusesWithoutConditionalWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "")
	if c, err := passkeyChallenges(ctx, docstore.NewMemoryAdapter(), ""); c != nil || err != nil {
		t.Fatalf("default is the per-process store: %v %v", c, err)
	}
	if _, err := passkeyChallenges(ctx, docstore.NewMemoryAdapter(), "redis"); err == nil {
		t.Fatal("an unknown mode is an error")
	}
	// store on a store that has conditional writes: shared, quietly.
	logs := captureLog(t)
	for _, mode := range []string{"store", "store-required"} {
		c, err := passkeyChallenges(ctx, docstore.NewMemoryAdapter(), mode)
		if _, ok := c.(*passkey.StoreChallenges); !ok || err != nil {
			t.Fatalf("%s on a conditional store = %T, %v", mode, c, err)
		}
	}
	if strings.Contains(logs.String(), "WARNING") {
		t.Errorf("no warning when it works: %q", logs)
	}
	// store on one without: the server keeps running on per-process ceremonies and says so loudly.
	plain := plainAdapter{docstore.NewMemoryAdapter()}
	logs = captureLog(t)
	c, err := passkeyChallenges(ctx, plain, "store")
	if c != nil || err != nil {
		t.Fatalf("store must fall back (nil = per-process), not stop: %v %v", c, err)
	}
	for _, want := range []string{"WARNING", "NOT SHARED", "conditional writes", "GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES=store-required", "fix:", "cause:"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the warning lacks %q: %q", want, logs)
		}
	}
	if strings.Count(logs.String(), "\n") < 4 {
		t.Errorf("the warning is several lines: %q", logs)
	}
	// store-required on one without: fatal (an error to Main).
	if _, err := passkeyChallenges(ctx, plain, "store-required"); err == nil || !strings.Contains(err.Error(), "conditional") {
		t.Fatalf("store-required must refuse: %v", err)
	}
	// memory with several replicas declared: ceremonies are not shared, said once per start.
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "3")
	logs = captureLog(t)
	if c, err := passkeyChallenges(ctx, plain, "memory"); c != nil || err != nil || !strings.Contains(logs.String(), "kept in each process") {
		t.Fatalf("memory with replicas: %v %v %q", c, err, logs)
	}
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "1")
	logs = captureLog(t)
	_, _ = passkeyChallenges(ctx, plain, "memory")
	if strings.Contains(logs.String(), "WARNING") {
		t.Errorf("one replica needs no warning: %q", logs)
	}
}

// The two shared-store modes print the same kind of warning and fail the same way.
func TestSharedStoreWarningIsTheSameForSessionsAndChallenges(t *testing.T) {
	ctx := context.Background()
	plain := plainAdapter{docstore.NewMemoryAdapter()}
	logs := captureLog(t)
	for _, tc := range []struct{ env, what string }{
		{"GOJIBERRY_DOCSTORE_SESSIONS", "sign-in sessions"},
		{"GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", "passkey ceremonies"},
	} {
		logs.Reset()
		if ok, err := sharedStoreOrFallback(ctx, plain, tc.env, "store", tc.what, "x"); ok || err != nil {
			t.Fatalf("%s: %v %v", tc.env, ok, err)
		}
		for _, want := range []string{tc.env + "=store-required", tc.env + "=store needs a store with conditional writes", "cause:", "fix:"} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("%s: lacks %q: %q", tc.env, want, logs)
			}
		}
		if _, err := sharedStoreOrFallback(ctx, plain, tc.env, "store-required", tc.what, "x"); err == nil || !strings.Contains(err.Error(), tc.env) {
			t.Errorf("%s: store-required = %v", tc.env, err)
		}
	}
}

func TestPasskeysWarnsWhenChallengesIsSetWithoutRPID(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_RPID", "")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", "store")
	logs := captureLog(t)
	a := docstore.NewMemoryAdapter()
	if _, err := withPasskeys(context.Background(), docstore.NewServer(a, "memory"), a, nil, docstore.MemoryLimiters()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "PASSKEY_RPID is not set") {
		t.Errorf("%q", logs)
	}
}

func TestSessionsModeSharesPasskeySessionsBetweenReplicas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const rp, origin = "opendocs.io", "https://opendocs.io"
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_RPID", rp)
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_ORIGINS", origin)
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_CHALLENGES", "store")
	t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_NAME", "opendocs")
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "store")
	mem := docstore.NewMemoryAdapter()
	replica := func() http.Handler {
		srv := docstore.NewServer(mem, "memory")
		m, err := withPasskeys(ctx, srv, mem, nil, docstore.MemoryLimiters())
		if err != nil {
			t.Fatal(err)
		}
		if err := withSessions(ctx, mem, m, docstore.MemoryLimiters()); err != nil {
			t.Fatal(err)
		}
		return srv.WithAuth(m).Handler()
	}
	a, b := replica(), replica()
	do := func(h http.Handler, method, path, token string, body []byte) (int, map[string]any) {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&out)
		return rec.Code, out
	}
	code, begin := do(a, "POST", "/auth/passkey/register/begin", "", []byte(`{"email":"ana@opendocs.io"}`))
	if code != 200 {
		t.Fatalf("begin: %d %v", code, begin)
	}
	raw, _ := json.Marshal(map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]})
	reg, err := webauthntest.New(rp).Create(raw, origin)
	if err != nil {
		t.Fatal(err)
	}
	code, out := do(b, "POST", "/auth/passkey/register/finish?token="+begin["token"].(string), "", reg)
	if code != 200 || out["auth_token"] == nil {
		t.Fatalf("finish on B: %d %v", code, out)
	}
	tok := out["auth_token"].(string)
	if code, _ := do(a, "GET", "/auth/me", tok, nil); code != 200 {
		t.Fatalf("a session made on B is unknown to A: %d", code)
	}
	if metas, _ := docstore.ListPrefix(ctx, mem, docstore.SessionPrefix); len(metas) != 1 {
		t.Fatalf("session records: %+v", metas)
	}
	if code, _ := do(a, "POST", "/auth/logout", tok, nil); code != http.StatusNoContent {
		t.Fatal(code)
	}
	if code, _ := do(b, "GET", "/auth/me", tok, nil); code != http.StatusUnauthorized {
		t.Fatalf("logout on A, then B: %d", code)
	}
}

func TestSessionsModeIsCheckedAndFallsBackOrRefusesWithoutConditionalWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://opendocs.io"})
	if err != nil {
		t.Fatal(err)
	}
	plain := plainAdapter{docstore.NewMemoryAdapter()}

	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "redis")
	if err := withSessions(ctx, plain, m, docstore.MemoryLimiters()); err == nil {
		t.Fatal("an unknown mode is an error")
	}
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "store")
	logs := captureLog(t)
	if err := withSessions(ctx, plain, m, docstore.MemoryLimiters()); err != nil {
		t.Fatalf("store falls back, it does not stop: %v", err)
	}
	if !strings.Contains(logs.String(), "WARNING") || !strings.Contains(logs.String(), "conditional writes") || !strings.Contains(logs.String(), "store-required") {
		t.Errorf("the warning names the problem and the fix: %q", logs)
	}
	tok, _ := m.StartSession(auth.User{ID: "passkey:1"})
	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if _, ok := m.CurrentUser(req); !ok {
		t.Error("the fallback keeps working sessions in memory")
	}
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "store-required")
	if err := withSessions(ctx, plain, m, docstore.MemoryLimiters()); err == nil || !strings.Contains(err.Error(), "conditional") {
		t.Fatalf("store-required on such a store must refuse: %v", err)
	}
	// Memory with several replicas declared says what that means.
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "")
	t.Setenv("GOJIBERRY_DOCSTORE_REPLICAS", "3")
	logs = captureLog(t)
	if err := withSessions(ctx, plain, m, docstore.MemoryLimiters()); err != nil || !strings.Contains(logs.String(), "unknown to the others") {
		t.Errorf("%v %q", err, logs)
	}
	// No sign-in at all: a set variable is reported, not obeyed.
	t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", "store")
	logs = captureLog(t)
	if err := withSessions(ctx, docstore.NewMemoryAdapter(), nil, docstore.MemoryLimiters()); err != nil || !strings.Contains(logs.String(), "ignored") {
		t.Errorf("%v %q", err, logs)
	}
}

// The OpenID Connect login route writes a state record without a sign-in, so withSessions gives it
// a budget per client address in every mode (memory, store, and the fallback to memory).
func TestWithSessionsLimitsLoginsPerClientInEveryMode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oidcSrv := &oidctest.Server{}
	ts := httptest.NewServer(oidcSrv)
	defer ts.Close()
	oidcSrv.SetIssuer(ts.URL)

	for _, tc := range []struct {
		name, mode string
		adapter    docstore.Adapter
	}{
		{"memory", "", docstore.NewMemoryAdapter()},
		{"store", "store", docstore.NewMemoryAdapter()},
		{"fallback", "store", plainAdapter{docstore.NewMemoryAdapter()}},
	} {
		for _, proxies := range []string{"", "10.0.0.0/8"} {
			t.Run(tc.name+"/proxies="+proxies, func(t *testing.T) {
				t.Setenv("GOJIBERRY_DOCSTORE_SESSIONS", tc.mode)
				t.Setenv("GOJIBERRY_DOCSTORE_PASSKEY_TRUSTED_PROXIES", proxies)
				logs := captureLog(t)
				m, err := auth.NewManager(ctx, auth.Config{BaseURL: "https://opendocs.io",
					Providers: []auth.Provider{{Name: "test", IssuerURL: ts.URL, ClientID: "c", ClientSecret: "s"}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := withSessions(ctx, tc.adapter, m, docstore.MemoryLimiters()); err != nil {
					t.Fatal(err)
				}
				if warned := strings.Contains(logs.String(), "TRUSTED_PROXIES is not set"); warned != (proxies == "") {
					t.Errorf("proxy warning = %v with proxies %q: %q", warned, proxies, logs)
				}
				h := m.Routes()
				login := func(remote, xff string) *httptest.ResponseRecorder {
					req := httptest.NewRequest("GET", "/auth/test/login", nil)
					req.RemoteAddr = remote
					if xff != "" {
						req.Header.Set("X-Forwarded-For", xff)
					}
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, req)
					return rec
				}
				for i := 0; i < passkey.DefaultBurst; i++ {
					if rec := login("198.51.100.1:1000", ""); rec.Code != http.StatusFound {
						t.Fatalf("login %d: %d", i+1, rec.Code)
					}
				}
				// An untrusted peer cannot pick its own key with the header.
				rec := login("198.51.100.1:1001", "203.0.113.7")
				if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
					t.Fatalf("over budget: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
				}
				if rec := login("198.51.100.2:1000", ""); rec.Code != http.StatusFound {
					t.Fatalf("another address: %d", rec.Code)
				}
				if proxies != "" {
					// A trusted proxy's header names the client; the proxy's own budget is not spent.
					for i := 0; i < passkey.DefaultBurst; i++ {
						if rec := login("10.1.1.1:1000", "203.0.113.50"); rec.Code != http.StatusFound {
							t.Fatalf("via proxy, login %d: %d", i+1, rec.Code)
						}
					}
					if rec := login("10.1.1.1:1000", "203.0.113.50"); rec.Code != http.StatusTooManyRequests {
						t.Fatalf("via proxy, over budget: %d", rec.Code)
					}
					if rec := login("10.1.1.1:1000", "203.0.113.51"); rec.Code != http.StatusFound {
						t.Fatalf("via proxy, another client: %d", rec.Code)
					}
				}
			})
		}
	}
}

func TestPublicQuotaModes(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	if q, err := publicQuota(ctx, store, 100, ""); err != nil || q != nil {
		t.Errorf("default: %v %v", q, err)
	}
	if q, err := publicQuota(ctx, store, 100, "store"); err != nil || q == nil {
		t.Errorf("store: %v %v", q, err)
	}
	if _, err := publicQuota(ctx, store, 100, "everywhere"); err == nil {
		t.Error("a bad mode was accepted")
	}
}

func TestPublicSessionsModes(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	if s, err := publicSessions(ctx, store, 100, ""); err != nil || s != nil {
		t.Errorf("default: %v %v", s, err)
	}
	if s, err := publicSessions(ctx, store, 100, "store"); err != nil || s == nil {
		t.Errorf("store: %v %v", s, err)
	}
	if _, err := publicSessions(ctx, store, 100, "everywhere"); err == nil {
		t.Error("a bad mode was accepted")
	}
}

func TestPublicPriorityQuotaModes(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	if q, err := publicPriorityQuota(ctx, store, 5, ""); err != nil || q != nil {
		t.Errorf("default: %v %v", q, err)
	}
	if q, err := publicPriorityQuota(ctx, store, 5, "store"); err != nil || q == nil {
		t.Errorf("store: %v %v", q, err)
	}
	if _, err := publicPriorityQuota(ctx, store, 5, "everywhere"); err == nil {
		t.Error("a bad mode was accepted")
	}
}
