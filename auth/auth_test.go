package auth

import (
	"context"
	"net/http/httptest"
	"strings"
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
	yes := true
	u := userFromClaims("google", idTokenClaims{Subject: "123", Email: "a@example.com", EmailVerified: &yes, Name: "A"}, false)
	want := User{ID: "google:123", Email: "a@example.com", VerifiedEmail: true, Name: "A"}
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
	u := userFromClaims("google", idTokenClaims{Subject: "S1", Email: "ana@gojiberry.io", EmailVerified: &yes, Name: "Ana", PreferredUsername: "ana", Picture: "https://gojiberry.io/a.png"}, false)
	if u.Username != "ana" || u.Picture != "https://gojiberry.io/a.png" || u.ID != "google:S1" {
		t.Fatalf("%+v", u)
	}
	p := u.Person("editor")
	if p.ID != "google:S1" || p.Email != "ana@gojiberry.io" || p.Username != "ana" || p.Name != "Ana" || p.Role != "editor" {
		t.Fatalf("%+v", p)
	}
	no := false
	if v := userFromClaims("google", idTokenClaims{Subject: "S2", Email: "x@gojiberry.io", EmailVerified: &no}, false); v.Email != "" {
		t.Error("an unverified address identifies no one")
	}
}

func TestProviderNameAnonIsReserved(t *testing.T) {
	_, err := NewManager(context.Background(), Config{BaseURL: "https://x.example", Providers: []Provider{{Name: AnonProvider, IssuerURL: "https://issuer.invalid", ClientID: "c"}}})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("err = %v, want a reserved-name error", err)
	}
}

func TestAnonymousUser(t *testing.T) {
	u := AnonymousUser("abc")
	if u.ID != "anon:abc" || !u.IsAnonymous() || u.Email != "" || u.VerifiedEmail {
		t.Fatalf("anonymous user = %+v", u)
	}
	if (User{ID: "google:1"}).IsAnonymous() {
		t.Fatal("a provider user counts as anonymous")
	}
}

func TestMemorySessionStoreIsTheDefaultAndKeepsItsBehaviour(t *testing.T) {
	m, err := NewManager(context.Background(), Config{BaseURL: "https://gojiberry.io"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.store.(*MemorySessionStore); !ok {
		t.Fatalf("default store = %T", m.store)
	}
	ctx := context.Background()
	s := NewMemorySessionStore()
	tok, err := s.PutSession(ctx, User{ID: "x:1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if u, ok, err := s.GetSession(ctx, tok); err != nil || !ok || u.ID != "x:1" {
		t.Fatalf("%+v %v %v", u, ok, err)
	}
	_ = s.DeleteSession(ctx, tok)
	if _, ok, _ := s.GetSession(ctx, tok); ok {
		t.Fatal("deleted")
	}
	short, _ := s.PutSession(ctx, User{ID: "x:2"}, time.Nanosecond)
	time.Sleep(2 * time.Millisecond)
	if _, ok, _ := s.GetSession(ctx, short); ok {
		t.Fatal("expired")
	}
	st, _ := s.PutState(ctx, LoginState{Provider: "p"}, time.Minute)
	if _, ok, _ := s.TakeState(ctx, st); !ok {
		t.Fatal("take")
	}
	if _, ok, _ := s.TakeState(ctx, st); ok {
		t.Fatal("take twice")
	}
	m.SetSessionStore(nil) // nil keeps the store
	if m.store == nil {
		t.Fatal("SetSessionStore(nil) removed the store")
	}
}
