package sessionstore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// fakeProvider is an OpenID Connect provider with a /token endpoint (as in package auth's tests).
type fakeProvider struct {
	srv   *httptest.Server
	priv  *rsa.PrivateKey
	nonce atomic.Value
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{priv: priv}
	o := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{PublicKey: priv.Public(), KeyID: "k", Algorithm: oidc.RS256}}}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		n, _ := f.nonce.Load().(string)
		claims := fmt.Sprintf(`{"iss":%q,"aud":"cid","sub":"sub1","exp":%d,"iat":%d,"email":"ana@gojiberry.io","email_verified":true,"name":"Ana","nonce":%q}`,
			f.srv.URL, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), n)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer", "id_token": oidctest.SignIDToken(priv, "k", oidc.RS256, claims)})
	})
	mux.Handle("/.well-known/openid-configuration", o)
	mux.Handle("/keys", o)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	o.SetIssuer(f.srv.URL)
	return f
}

func replica(t *testing.T, f *fakeProvider, store auth.SessionStore) *auth.Manager {
	t.Helper()
	m, err := auth.NewManager(context.Background(), auth.Config{
		BaseURL:   "https://app.gojiberry.io",
		Providers: []auth.Provider{{Name: "test", IssuerURL: f.srv.URL, ClientID: "cid", ClientSecret: "s"}},
		Sessions:  store,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func do(h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// Two Managers on one store: a login begun on A is finished on B, the session works on both, a
// logout on either ends it on both, and the state cannot be replayed on either.
func TestTwoManagersShareLoginSessionAndLogout(t *testing.T) {
	f := newFakeProvider(t)
	mem := docstore.NewMemoryAdapter()
	sa, _ := New(mem)
	sb, _ := New(mem)
	a, b := replica(t, f, sa), replica(t, f, sb)

	login := do(a.Routes(), "GET", "/auth/test/login?return_to=https://app.gojiberry.io/d", "")
	loc, _ := url.Parse(login.Header().Get("Location"))
	state, nonce := loc.Query().Get("state"), loc.Query().Get("nonce")
	f.nonce.Store(nonce)

	cb := "/auth/test/callback?code=c&state=" + url.QueryEscape(state)
	w := do(b.Routes(), "GET", cb, "")
	if w.Code != http.StatusFound {
		t.Fatalf("callback on B = %d %s", w.Code, w.Body.String())
	}
	token := strings.TrimPrefix(w.Header().Get("Location"), "https://app.gojiberry.io/d#auth_token=")
	if token == "" || strings.Contains(token, "http") {
		t.Fatalf("location %q", w.Header().Get("Location"))
	}
	for _, r := range []*auth.Manager{a, b} {
		if me := do(r.Routes(), "GET", "/auth/me", token); me.Code != 200 || !strings.Contains(me.Body.String(), "ana@gojiberry.io") {
			t.Fatalf("/auth/me = %d %s", me.Code, me.Body.String())
		}
	}
	// Replay of the state on the replica that began the login, and on the one that finished it.
	for _, r := range []*auth.Manager{a, b} {
		if w := do(r.Routes(), "GET", cb, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("replayed state = %d, want 400", w.Code)
		}
	}
	if w := do(a.Routes(), "POST", "/auth/logout", token); w.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", w.Code)
	}
	for _, r := range []*auth.Manager{a, b} {
		if me := do(r.Routes(), "GET", "/auth/me", token); me.Code != http.StatusUnauthorized {
			t.Fatalf("/auth/me after logout = %d", me.Code)
		}
	}
	// A session started the way a passkey sign-in starts one is shared too.
	pk, err := a.StartSession(auth.User{ID: "passkey:1", Name: "Bo"})
	if err != nil {
		t.Fatal(err)
	}
	if me := do(b.Routes(), "GET", "/auth/me", pk); me.Code != 200 {
		t.Fatalf("passkey-style session on B = %d", me.Code)
	}
	if w := do(b.Routes(), "POST", "/auth/logout", pk); w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
	if me := do(a.Routes(), "GET", "/auth/me", pk); me.Code != http.StatusUnauthorized {
		t.Fatalf("logout on B, then A: %d", me.Code)
	}
}

// A store that cannot answer signs nobody in, and a logout it cannot do is an error.
func TestManagerFailsClosedOnStoreErrors(t *testing.T) {
	f := newFakeProvider(t)
	fl := &failing{ConditionalAdapter: docstore.NewMemoryAdapter()}
	s, _ := New(fl)
	m := replica(t, f, s)
	tok, err := m.StartSession(auth.User{ID: "passkey:1"})
	if err != nil {
		t.Fatal(err)
	}
	fl.fail.Store(true)
	if w := do(m.Routes(), "GET", "/auth/me", tok); w.Code != http.StatusUnauthorized {
		t.Fatalf("me = %d", w.Code)
	}
	if w := do(m.Routes(), "POST", "/auth/logout", tok); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("logout with the store down = %d, want 503 (the session is still valid)", w.Code)
	}
	if w := do(m.Routes(), "GET", "/auth/test/callback?code=c&state=x.1", ""); w.Code != http.StatusServiceUnavailable && w.Code != http.StatusBadRequest {
		t.Fatalf("callback = %d", w.Code)
	}
}

func TestManagerAnswers503AtTheBound(t *testing.T) {
	f := newFakeProvider(t)
	s, _ := New(docstore.NewMemoryAdapter())
	s.MaxStates = 2
	m := replica(t, f, s)
	for i := 0; i < 2; i++ {
		if w := do(m.Routes(), "GET", "/auth/test/login", ""); w.Code != http.StatusFound {
			t.Fatalf("login %d = %d", i, w.Code)
		}
	}
	if w := do(m.Routes(), "GET", "/auth/test/login", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("login over the bound = %d", w.Code)
	}
}
