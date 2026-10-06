package auth

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenStorePutGet(t *testing.T) {
	s := newTokenStore[string]()
	token, err := s.put("hello", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if token == "" {
		t.Fatal("put returned an empty token")
	}
	got, ok := s.get(token)
	if !ok || got != "hello" {
		t.Fatalf("get(%q) = %q, %v; want \"hello\", true", token, got, ok)
	}
}

func TestTokenStoreGetUnknown(t *testing.T) {
	s := newTokenStore[string]()
	if _, ok := s.get("nope"); ok {
		t.Fatal("get of an unknown token should return false")
	}
}

func TestTokenStoreGetExpired(t *testing.T) {
	s := newTokenStore[string]()
	token, err := s.put("hello", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, ok := s.get(token); ok {
		t.Fatal("get of an expired token should return false")
	}
}

func TestTokenStoreTakeConsumesOnce(t *testing.T) {
	s := newTokenStore[string]()
	token, err := s.put("hello", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	got, ok := s.take(token)
	if !ok || got != "hello" {
		t.Fatalf("first take(%q) = %q, %v; want \"hello\", true", token, got, ok)
	}
	if _, ok := s.take(token); ok {
		t.Fatal("second take of the same token should return false")
	}
	if _, ok := s.get(token); ok {
		t.Fatal("get after take should also return false")
	}
}

func TestTokenStoreTakeExpired(t *testing.T) {
	s := newTokenStore[string]()
	token, err := s.put("hello", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, ok := s.take(token); ok {
		t.Fatal("take of an expired token should return false")
	}
}

func TestTokenStoreDelete(t *testing.T) {
	s := newTokenStore[string]()
	token, err := s.put("hello", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	s.delete(token)
	if _, ok := s.get(token); ok {
		t.Fatal("get after delete should return false")
	}
}

func TestUserFromClaims(t *testing.T) {
	u := userFromClaims("google", idTokenClaims{Subject: "123", Email: "a@example.com", Name: "A"})
	want := User{ID: "google:123", Email: "a@example.com", Name: "A"}
	if u != want {
		t.Fatalf("userFromClaims = %+v; want %+v", u, want)
	}
}

func TestNewManagerRequiresBaseURL(t *testing.T) {
	_, err := NewManager(context.TODO(), Config{Providers: []Provider{{Name: "google", IssuerURL: "https://accounts.google.com", ClientID: "id"}}})
	if err == nil {
		t.Fatal("NewManager with an empty BaseURL should error")
	}
}

// A server that signs people in with passkeys only has no OpenID Connect provider but still
// needs the Manager for its sessions.
func TestNewManagerWithoutProvidersIsASessionOnlyManager(t *testing.T) {
	m, err := NewManager(context.TODO(), Config{BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Providers()) != 0 {
		t.Errorf("%v", m.Providers())
	}
	tok, err := m.StartSession(User{ID: "passkey:1", Email: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if u, ok := m.CurrentUser(req); !ok || u.ID != "passkey:1" {
		t.Errorf("%+v %v", u, ok)
	}
}

func TestUserFromClaimsKeepsUsernamePictureAndMakesAPerson(t *testing.T) {
	yes := true
	u := userFromClaims("google", idTokenClaims{Subject: "S1", Email: "ana@gojiberry.io", EmailVerified: &yes, Name: "Ana", PreferredUsername: "ana", Picture: "https://gojiberry.io/a.png"})
	if u.Username != "ana" || u.Picture != "https://gojiberry.io/a.png" || u.ID != "google:S1" {
		t.Fatalf("%+v", u)
	}
	p := u.Person("editor")
	if p.ID != "google:S1" || p.Email != "ana@gojiberry.io" || p.Username != "ana" || p.Name != "Ana" || p.Role != "editor" {
		t.Fatalf("%+v", p)
	}
	no := false
	if v := userFromClaims("google", idTokenClaims{Subject: "S2", Email: "x@gojiberry.io", EmailVerified: &no}); v.Email != "" {
		t.Error("an unverified address identifies no one")
	}
}
