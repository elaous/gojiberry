package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Routes returns an http.Handler serving:
//
//	GET  /auth/providers          -- []ProviderInfo, every configured provider
//	GET  /auth/{provider}/login   -- redirects to that provider's own consent screen
//	GET  /auth/{provider}/callback -- the provider's own redirect_uri target
//	GET  /auth/me                 -- the signed-in User for this request's own bearer token, or 401
//	POST /auth/logout             -- invalidates this request's own bearer token
//
// Mount this under a server that already answers CORS preflight
// (OPTIONS) requests and includes "Authorization" in its own
// Access-Control-Allow-Headers -- see docstore's own corsMiddleware
// and this package's top doc comment for why a bearer token, not a
// cookie, carries the session.
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

// handleLoginFor returns the /auth/{name}/login handler for the
// already-resolved provider name -- Routes builds one per configured
// provider rather than a single handler parsing {name} back out of
// the path, since http.ServeMux has no path-parameter support on the
// Go version this module targets.
func (m *Manager) handleLoginFor(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		state, err := m.states.put(loginState{Provider: name, ReturnTo: returnTo, Verifier: verifier, Nonce: nonce}, time.Now().Add(stateTTL))
		if errors.Is(err, errStoreFull) {
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

// handleCallbackFor returns the /auth/{name}/callback handler --
// exchanges the provider's own authorization code for tokens,
// verifies the ID token (signature, issuer, audience, expiry --
// everything oidc.IDTokenVerifier.Verify checks), mints a session,
// and redirects the browser back to state's own ReturnTo with the new
// session token in the URL fragment (#auth_token=..., never a query
// parameter, so it doesn't end up in server access logs on whatever
// page loads next) for the client's own JS to pick up -- see
// identity.go's own doc comment on the OnDesign side for how that
// half works. ReturnTo defaults to BaseURL itself when LoginURL's own
// caller didn't supply one.
func (m *Manager) handleCallbackFor(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := m.byName[name]
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			http.Error(w, "auth: provider returned an error: "+errParam, http.StatusBadRequest)
			return
		}
		stateToken := r.URL.Query().Get("state")
		state, ok := m.states.take(stateToken)
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
			http.Error(w, "auth: verifying id_token: "+err.Error(), http.StatusUnauthorized)
			return
		}
		if idToken.Nonce != state.Nonce {
			http.Error(w, "auth: id_token nonce mismatch", http.StatusUnauthorized)
			return
		}
		var claims idTokenClaims
		if err := idToken.Claims(&claims); err != nil {
			http.Error(w, "auth: decoding claims: "+err.Error(), http.StatusBadGateway)
			return
		}
		if claims.Subject == "" {
			http.Error(w, "auth: id_token has no subject", http.StatusUnauthorized)
			return
		}
		user := userFromClaims(name, claims)

		sessionToken, err := m.sessions.put(user, time.Now().Add(m.sessionTTL))
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
		m.sessions.delete(token)
	}
	w.WriteHeader(http.StatusNoContent)
}

// CurrentUser resolves r's own "Authorization: Bearer <token>" header
// to a signed-in User, if any -- the primitive a document's own
// sharing/permission checks (still unbuilt) will call directly rather
// than going through handleMe's own JSON response.
func (m *Manager) CurrentUser(r *http.Request) (User, bool) {
	token, ok := bearerToken(r)
	if !ok {
		return User{}, false
	}
	return m.sessions.get(token)
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
	_ = json.NewEncoder(w).Encode(v) // a write failure here means the client disconnected -- nothing actionable to do about it, the same posture docstore's own handlers already take
}
