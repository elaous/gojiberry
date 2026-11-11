package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
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

// fakeOIDCProvider adds a /token endpoint to oidctest.Server.
type fakeOIDCProvider struct {
	srv        *httptest.Server
	oidcServer *oidctest.Server
	priv       *rsa.PrivateKey
	keyID      string

	// nextIDToken, if set, is the id_token of the next response.
	nextIDToken string
	// nonce is echoed in the id_token, as a provider does.
	nonce string
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
		"email_verified": true,
		"name": %q,
		"nonce": %q
	}`, f.srv.URL, aud, sub, time.Now().Add(time.Hour).Unix(), time.Now().Unix(), email, name, f.nonce)
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

// TestCallbackFullFlow drives a whole login: /login, then /callback.
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
	f.nonce = loginLoc.Query().Get("nonce")

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
	wantUser := User{ID: "test:user-sub", Email: "user@example.com", VerifiedEmail: true, Name: "Test User"}
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
	f.nonce = loginLoc.Query().Get("nonce")

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
	f.nonce = loginLoc.Query().Get("nonce")

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

func TestReturnToIsRestrictedToKnownOrigins(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m, err := NewManager(context.Background(), Config{
		BaseURL:       "https://app.example.com",
		ReturnOrigins: []string{"http://localhost:3000"},
		Providers:     []Provider{{Name: "test", IssuerURL: f.srv.URL, ClientID: "test-client", ClientSecret: "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	routes := m.Routes()
	for _, tc := range []struct{ returnTo, want string }{
		{"https://app.example.com/doc/1", "https://app.example.com/doc/1"},
		{"http://localhost:3000/x", "http://localhost:3000/x"},
		{"/doc/2", "https://app.example.com/doc/2"},
		{"https://evil.example/", "https://app.example.com"},
		{"//evil.example/x", "https://app.example.com"},
		{"https://app.example.com.evil.example/", "https://app.example.com"},
		{"https://user@evil.example@app.example.com/", "https://app.example.com"},
		{"javascript:alert(1)", "https://app.example.com"},
		{"", "https://app.example.com"},
	} {
		login := httptest.NewRecorder()
		routes.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/test/login?return_to="+url.QueryEscape(tc.returnTo), nil))
		loc, _ := url.Parse(login.Header().Get("Location"))
		f.nonce = loc.Query().Get("nonce")
		cb := httptest.NewRecorder()
		routes.ServeHTTP(cb, httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), nil))
		got := cb.Header().Get("Location")
		if !strings.HasPrefix(got, tc.want+"#auth_token=") {
			t.Errorf("return_to %q: redirected to %q, want %q", tc.returnTo, got, tc.want)
		}
	}
}

func TestLoginUsesPKCEAndNonceAndChecksThem(t *testing.T) {
	f := newFakeOIDCProvider(t)
	m := newTestManager(t, f.srv.URL)
	routes := m.Routes()
	login := httptest.NewRecorder()
	routes.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/test/login", nil))
	loc, _ := url.Parse(login.Header().Get("Location"))
	if loc.Query().Get("code_challenge") == "" || loc.Query().Get("code_challenge_method") != "S256" || loc.Query().Get("nonce") == "" {
		t.Fatalf("authorization request lacks PKCE or nonce: %s", loc.RawQuery)
	}
	f.nonce = "someone-elses-nonce"
	cb := httptest.NewRecorder()
	routes.ServeHTTP(cb, httptest.NewRequest(http.MethodGet, "/auth/test/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), nil))
	if cb.Code != http.StatusUnauthorized {
		t.Fatalf("a token with another login's nonce: status %d, want 401", cb.Code)
	}
}

func TestUnverifiedEmailIsDropped(t *testing.T) {
	no, yes := false, true
	if u := userFromClaims("p", idTokenClaims{Subject: "s", Email: "a@b", EmailVerified: &no}, false); u.Email != "" || u.VerifiedEmail {
		t.Fatalf("unverified email kept: %+v", u)
	}
	if u := userFromClaims("p", idTokenClaims{Subject: "s", Email: "a@b", EmailVerified: &no}, true); u.Email != "" || u.VerifiedEmail {
		t.Fatalf("an explicit false must win over AssumeEmailVerified: %+v", u)
	}
	if u := userFromClaims("p", idTokenClaims{Subject: "s", Email: "a@b", EmailVerified: &yes}, false); u.Email != "a@b" || !u.VerifiedEmail {
		t.Fatalf("verified email lost: %+v", u)
	}
	// An absent claim is not verification: no Email, no VerifiedEmail.
	if u := userFromClaims("p", idTokenClaims{Subject: "s", Email: "a@b"}, false); u.Email != "" || u.VerifiedEmail {
		t.Fatalf("an absent email_verified claim was treated as verified: %+v", u)
	}
	// Only the per-provider opt-in accepts an absent claim.
	if u := userFromClaims("p", idTokenClaims{Subject: "s", Email: "a@b"}, true); u.Email != "a@b" || !u.VerifiedEmail {
		t.Fatalf("AssumeEmailVerified ignored: %+v", u)
	}
}

func TestTokenStorePrunesAndIsBounded(t *testing.T) {
	s := newTokenStore[int]()
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	for i := 0; i < pruneAt; i++ {
		if _, err := s.put(i, past); err != nil {
			t.Fatal(err)
		}
	}
	s.swept = time.Time{}
	if _, err := s.put(1, future); err != nil {
		t.Fatal(err)
	}
	if n := len(s.entries); n != 1 {
		t.Fatalf("expired entries were not swept: %d left", n)
	}
	for i := 0; i < maxEntries; i++ {
		if _, err := s.put(i, future); err != nil {
			if !errors.Is(err, errStoreFull) {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("the store never refused an entry")
}

func TestProvidersKeepConfiguredOrder(t *testing.T) {
	f := newFakeOIDCProvider(t)
	var ps []Provider
	for _, n := range []string{"zeta", "alpha", "mid"} {
		ps = append(ps, Provider{Name: n, IssuerURL: f.srv.URL, ClientID: "c", ClientSecret: "s"})
	}
	m, err := NewManager(context.Background(), Config{BaseURL: "https://app.example.com", Providers: ps})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got := m.Providers()
		if got[0].Name != "zeta" || got[1].Name != "alpha" || got[2].Name != "mid" {
			t.Fatalf("order: %+v", got)
		}
	}
}
