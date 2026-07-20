package auth

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
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
)

// fakeOIDCProvider wraps oidctest.Server (discovery + keys) with a
// /token endpoint of its own -- oidctest.Server doesn't implement one,
// so a real end-to-end exercise of Manager's own code-exchange path
// (which calls oauth2Config.Exchange, a real POST to /token) needs
// this small addition.
type fakeOIDCProvider struct {
	srv        *httptest.Server
	oidcServer *oidctest.Server
	priv       *rsa.PrivateKey
	keyID      string

	// nextIDToken, if set, is returned verbatim as the id_token in the
	// next /token response -- lets a test swap in a token signed with
	// unexpected claims (e.g. the wrong audience) without needing a
	// second server.
	nextIDToken string
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	f := &fakeOIDCProvider{priv: priv, keyID: "test-key"}
	f.oidcServer = &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{
			{PublicKey: priv.Public(), KeyID: f.keyID, Algorithm: oidc.RS256},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.handleToken)
	mux.Handle("/.well-known/openid-configuration", f.oidcServer)
	mux.Handle("/keys", f.oidcServer)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	f.oidcServer.SetIssuer(f.srv.URL)
	return f
}

func (f *fakeOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	idToken := f.nextIDToken
	if idToken == "" {
		idToken = f.signIDToken("test-client", "user-sub", "user@example.com", "Test User")
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "test-access-token",
		"token_type":   "Bearer",
		"id_token":     idToken,
	})
}

func (f *fakeOIDCProvider) signIDToken(aud, sub, email, name string) string {
	claims := fmt.Sprintf(`{
		"iss": %q,
		"aud": %q,
		"sub": %q,
		"exp": %d,
		"iat": %d,
		"email": %q,
		"name": %q
	}`, f.srv.URL, aud, sub, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), email, name)
	return oidctest.SignIDToken(f.priv, f.keyID, oidc.RS256, claims)
}

func newTestManager(t *testing.T, issuerURL string) *Manager {
	t.Helper()
	m, err := NewManager(context.Background(), Config{
		BaseURL: "https://app.example.com",
		Providers: []Provider{
			{Name: "test", DisplayName: "Test Provider", IssuerURL: issuerURL, ClientID: "test-client", ClientSecret: "test-secret"},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestProvidersListsConfiguredProviders(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/auth/providers", nil)
	w := httptest.NewRecorder()
	m.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var got []ProviderInfo
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	want := []ProviderInfo{{Name: "test", DisplayName: "Test Provider"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Providers = %+v; want %+v", got, want)
	}
}

func TestLoginRedirectsToProvider(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/auth/test/login?return_to=https://app.example.com/doc/1", nil)
	w := httptest.NewRecorder()
	m.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing Location: %v", err)
	}
	if !strings.HasPrefix(loc.String(), f.srv.URL) {
		t.Fatalf("Location = %q; want it to start with the provider's own issuer %q", loc.String(), f.srv.URL)
	}
	if loc.Query().Get("client_id") != "test-client" {
		t.Fatalf("client_id = %q; want %q", loc.Query().Get("client_id"), "test-client")
	}
	if loc.Query().Get("state") == "" {
		t.Fatal("login redirect is missing a state parameter")
	}
}

// TestCallbackFullFlow drives the whole login round trip: a /login
// request to mint real server-side state, then a /callback request
// carrying that state plus a fake authorization code, and checks the
// session it produces actually works against /auth/me and /auth/logout.
func TestCallbackFullFlow(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	routes := m.Routes()

	loginReq := httptest.NewRequest(http.MethodGet, "/auth/test/login?return_to=https://app.example.com/doc/1", nil)
	loginW := httptest.NewRecorder()
	routes.ServeHTTP(loginW, loginReq)
	loginLoc, err := url.Parse(loginW.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parsing login Location: %v", err)
	}
	state := loginLoc.Query().Get("state")

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	callbackW := httptest.NewRecorder()
	routes.ServeHTTP(callbackW, callbackReq)

	if callbackW.Code != http.StatusFound {
		t.Fatalf("callback status = %d; want 302; body: %s", callbackW.Code, callbackW.Body.String())
	}
	callbackLoc := callbackW.Header().Get("Location")
	if !strings.HasPrefix(callbackLoc, "https://app.example.com/doc/1#auth_token=") {
		t.Fatalf("callback Location = %q; want it to start with the ReturnTo plus #auth_token=", callbackLoc)
	}
	sessionToken := strings.TrimPrefix(callbackLoc, "https://app.example.com/doc/1#auth_token=")
	if sessionToken == "" {
		t.Fatal("callback produced an empty session token")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+sessionToken)
	meW := httptest.NewRecorder()
	routes.ServeHTTP(meW, meReq)
	if meW.Code != http.StatusOK {
		t.Fatalf("/auth/me status = %d; want 200; body: %s", meW.Code, meW.Body.String())
	}
	var user User
	if err := json.Unmarshal(meW.Body.Bytes(), &user); err != nil {
		t.Fatalf("decoding /auth/me response: %v", err)
	}
	wantUser := User{ID: "test:user-sub", Email: "user@example.com", Name: "Test User"}
	if user != wantUser {
		t.Fatalf("/auth/me user = %+v; want %+v", user, wantUser)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+sessionToken)
	logoutW := httptest.NewRecorder()
	routes.ServeHTTP(logoutW, logoutReq)
	if logoutW.Code != http.StatusNoContent {
		t.Fatalf("/auth/logout status = %d; want 204", logoutW.Code)
	}

	meAfterLogoutReq := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	meAfterLogoutReq.Header.Set("Authorization", "Bearer "+sessionToken)
	meAfterLogoutW := httptest.NewRecorder()
	routes.ServeHTTP(meAfterLogoutW, meAfterLogoutReq)
	if meAfterLogoutW.Code != http.StatusUnauthorized {
		t.Fatalf("/auth/me after logout status = %d; want 401", meAfterLogoutW.Code)
	}
}

func TestCallbackRejectsUnknownState(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=fake-code&state=never-issued", nil)
	w := httptest.NewRecorder()
	m.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
}

func TestCallbackStateIsSingleUse(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	routes := m.Routes()

	loginReq := httptest.NewRequest(http.MethodGet, "/auth/test/login", nil)
	loginW := httptest.NewRecorder()
	routes.ServeHTTP(loginW, loginReq)
	loginLoc, _ := url.Parse(loginW.Header().Get("Location"))
	state := loginLoc.Query().Get("state")

	first := httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	firstW := httptest.NewRecorder()
	routes.ServeHTTP(firstW, first)
	if firstW.Code != http.StatusFound {
		t.Fatalf("first callback status = %d; want 302", firstW.Code)
	}

	second := httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	secondW := httptest.NewRecorder()
	routes.ServeHTTP(secondW, second)
	if secondW.Code != http.StatusBadRequest {
		t.Fatalf("replayed-state callback status = %d; want 400", secondW.Code)
	}
}

func TestCallbackRejectsWrongAudienceIDToken(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	routes := m.Routes()

	loginReq := httptest.NewRequest(http.MethodGet, "/auth/test/login", nil)
	loginW := httptest.NewRecorder()
	routes.ServeHTTP(loginW, loginReq)
	loginLoc, _ := url.Parse(loginW.Header().Get("Location"))
	state := loginLoc.Query().Get("state")

	f.nextIDToken = f.signIDToken("some-other-client", "user-sub", "user@example.com", "Test User")

	req := httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=fake-code&state="+url.QueryEscape(state), nil)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401; body: %s", w.Code, w.Body.String())
	}
}

func TestMeWithoutBearerToken(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()
	m.Routes().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	tests := []struct {
		header    string
		wantToken string
		wantOK    bool
	}{
		{"Bearer abc123", "abc123", true},
		{"", "", false},
		{"Basic abc123", "", false},
		{"Bearer ", "", false},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		token, ok := bearerToken(req)
		if token != tt.wantToken || ok != tt.wantOK {
			t.Errorf("bearerToken(%q) = %q, %v; want %q, %v", tt.header, token, ok, tt.wantToken, tt.wantOK)
		}
	}
}
