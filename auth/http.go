package auth

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Routes returns an http.Handler for the auth endpoints.
func (m *Manager) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/providers", m.handleProviders)
	mux.HandleFunc("/auth/me", m.handleMe)
	mux.HandleFunc("/auth/logout", m.handleLogout)
	for name := range m.byName {
		name := name
		mux.HandleFunc("/auth/"+name+"/login", m.handleLoginFor(name))
		mux.HandleFunc("/auth/"+name+"/callback", m.handleCallbackFor(name))
	}
	return mux
}

func (m *Manager) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, m.Providers())
}

func (m *Manager) handleLoginFor(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ok, secs := m.allowLogin(r); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			http.Error(w, "too many login attempts", http.StatusTooManyRequests)
			return
		}
		p := m.byName[name]
		returnTo := m.safeReturnTo(r.URL.Query().Get("return_to"))
		nonce, err := randomToken()
		if err != nil {
			http.Error(w, "auth: generating nonce", http.StatusInternalServerError)
			return
		}
		verifier := oauth2.GenerateVerifier()
		state, err := m.store.PutState(r.Context(), LoginState{Provider: name, ReturnTo: returnTo, Verifier: verifier, Nonce: nonce}, stateTTL)
		if errors.Is(err, ErrStoreFull) {
			http.Error(w, "too many logins in progress", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "auth: generating state", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, p.oauth2Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), http.StatusFound)
	}
}

func (m *Manager) handleCallbackFor(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := m.byName[name]
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			http.Error(w, "auth: provider returned an error: "+errParam, http.StatusBadRequest)
			return
		}
		stateToken := r.URL.Query().Get("state")
		state, ok, err := m.store.TakeState(r.Context(), stateToken)
		if err != nil {
			log.Printf("auth: taking the login state: %v", err)
			http.Error(w, "auth: session store unavailable", http.StatusServiceUnavailable)
			return
		}
		if !ok || state.Provider != name {
			http.Error(w, "auth: missing or expired state (CSRF check failed)", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "auth: missing code", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		oauth2Token, err := p.oauth2Config.Exchange(ctx, code, oauth2.VerifierOption(state.Verifier))
		if err != nil {
			http.Error(w, "auth: exchanging code failed", http.StatusBadGateway)
			return
		}
		rawIDToken, ok := oauth2Token.Extra("id_token").(string)
		if !ok || rawIDToken == "" {
			http.Error(w, "auth: token response had no id_token", http.StatusBadGateway)
			return
		}
		idToken, err := p.verifier.Verify(ctx, rawIDToken)
		if err != nil {
			http.Error(w, "auth: id_token verification failed", http.StatusUnauthorized)
			return
		}
		if idToken.Nonce != state.Nonce {
			http.Error(w, "auth: id_token nonce mismatch", http.StatusUnauthorized)
			return
		}
		var claims idTokenClaims
		if err := idToken.Claims(&claims); err != nil {
			http.Error(w, "auth: could not read the id_token claims", http.StatusBadGateway)
			return
		}
		if claims.Subject == "" {
			http.Error(w, "auth: id_token has no subject", http.StatusUnauthorized)
			return
		}
		user := userFromClaims(name, claims, p.AssumeEmailVerified)

		sessionToken, err := m.store.PutSession(ctx, user, m.sessionTTL)
		if errors.Is(err, ErrStoreFull) {
			http.Error(w, "too many sessions", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "auth: creating session: "+err.Error(), http.StatusInternalServerError)
			return
		}

		returnTo := state.ReturnTo
		if returnTo == "" {
			returnTo = m.baseURL
		}
		sep := "#"
		if strings.Contains(returnTo, "#") {
			sep = "&"
		}
		http.Redirect(w, r, returnTo+sep+"auth_token="+sessionToken, http.StatusFound)
	}
}

func (m *Manager) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := m.CurrentUser(r)
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if token, ok := bearerToken(r); ok {
		if err := m.store.DeleteSession(r.Context(), token); err != nil {
			log.Printf("auth: deleting a session: %v", err)
			http.Error(w, "auth: session store unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) CurrentUser(r *http.Request) (User, bool) {
	token, ok := bearerToken(r)
	if !ok {
		return User{}, false
	}
	var ip string
	if m.missLimiter != nil {
		ip = m.missKey(r)
		if m.missBlockedNow(ip) {
			return User{}, false // over the miss budget: no store read
		}
	}
	u, ok, err := m.store.GetSession(r.Context(), token)
	if err != nil {
		log.Printf("auth: reading a session: %v", err)
		return User{}, false
	}
	if !ok && m.missLimiter != nil {
		m.countMiss(r.Context(), ip)
	}
	return u, ok
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(h, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
