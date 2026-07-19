package docstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/elaous/gojiberry/auth"
)

func TestNewAuthManagerNilWhenNoProviders(t *testing.T) {
	m, err := NewAuthManager(context.Background(), AuthConfig{})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	if m != nil {
		t.Fatal("NewAuthManager with no providers should return a nil Manager")
	}
}

func TestNewAuthManagerRequiresBaseURLWhenProvidersSet(t *testing.T) {
	_, err := NewAuthManager(context.Background(), AuthConfig{
		Providers: []auth.Provider{{Name: "google", IssuerURL: "https://accounts.google.com", ClientID: "id"}},
	})
	if err == nil {
		t.Fatal("NewAuthManager with providers but no BaseURL should error")
	}
}

func TestHandlerMountsAuthRoutesWhenConfigured(t *testing.T) {
	m, err := NewAuthManager(context.Background(), AuthConfig{
		BaseURL: "https://app.example.com",
		Providers: []auth.Provider{
			{Name: "google", IssuerURL: "https://accounts.google.com", ClientID: "id", ClientSecret: "secret"},
		},
	})
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	srv := NewServer(NewMemoryAdapter(), "memory").WithAuth(m)

	req := httptest.NewRequest(http.MethodGet, "/auth/providers", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/providers status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerHasNoAuthRoutesWhenUnconfigured(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/auth/providers", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /auth/providers on an auth-less server status = %d, want 404", rec.Code)
	}
}

func TestConfigFromEnvParsesAuthProviders(t *testing.T) {
	t.Setenv("GOJIBERRY_DOCSTORE_AUTH_BASE_URL", "https://app.example.com")
	t.Setenv("GOJIBERRY_DOCSTORE_AUTH_PROVIDERS", `[{"name":"google","displayName":"Google","issuerURL":"https://accounts.google.com","clientID":"id","clientSecret":"secret"}]`)
	t.Setenv("GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL", "1h")
	defer func() {
		os.Unsetenv("GOJIBERRY_DOCSTORE_AUTH_BASE_URL")
		os.Unsetenv("GOJIBERRY_DOCSTORE_AUTH_PROVIDERS")
		os.Unsetenv("GOJIBERRY_DOCSTORE_AUTH_SESSION_TTL")
	}()

	cfg := ConfigFromEnv()
	if cfg.Auth.BaseURL != "https://app.example.com" {
		t.Fatalf("Auth.BaseURL = %q", cfg.Auth.BaseURL)
	}
	if len(cfg.Auth.Providers) != 1 || cfg.Auth.Providers[0].Name != "google" {
		t.Fatalf("Auth.Providers = %+v", cfg.Auth.Providers)
	}
	if cfg.Auth.SessionTTL.String() != "1h0m0s" {
		t.Fatalf("Auth.SessionTTL = %v", cfg.Auth.SessionTTL)
	}
}

func TestConfigFromEnvAuthDisabledByDefault(t *testing.T) {
	cfg := ConfigFromEnv()
	if len(cfg.Auth.Providers) != 0 {
		t.Fatalf("Auth.Providers = %+v, want empty by default", cfg.Auth.Providers)
	}
}
