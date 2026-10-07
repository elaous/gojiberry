// Package demoserver is the server side of the people-keys demo: passkey sign-in, and the
// docstore's verified e-mail key directory (POST /directory/publish, GET /directory/lookup),
// over an in-memory store. It is shared by cmd/server and the e2e test.
package demoserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// Dir is the directory the demo serves, so a test can read and write it behind the routes' back
// (what a server that holds the directory key could do).
//
// Server holds what the demo's routes share, so a test can act as an identity provider's
// signed-in user (Login) without a real provider.
type Server struct {
	Auth *auth.Manager
	Mux  *http.ServeMux
	// Dir and Store are the directory and the store behind it.
	Dir   *encrypt.AdapterDirectory
	Store docstore.Adapter
}

// New builds the routes for a page served from origin (RP ID "localhost"). The directory key is
// generated here, for this process only: a real deployment sets GOJIBERRY_DOCSTORE_DIRECTORY_KEY
// so that records survive a restart.
func New(origin string) (*Server, error) {
	ctx := context.Background()
	mgr, err := auth.NewManager(ctx, auth.Config{BaseURL: origin})
	if err != nil {
		return nil, err
	}
	svc, err := passkey.New(passkey.Config{RPID: "localhost", RPName: "gojiberry demo", Origins: []string{origin}}, passkey.NewMemoryStore(), mgr)
	if err != nil {
		return nil, err
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	signer, err := encrypt.DirectorySignerFromSeed(seed)
	if err != nil {
		return nil, err
	}
	store := docstore.NewMemoryAdapter()
	dir := &encrypt.AdapterDirectory{Store: store, Signer: signer}
	docs := docstore.NewServer(store, "memory").WithAuth(mgr).WithDirectory(encrypt.DirectoryService(dir)).
		WithDirectoryLimits(func(_ string, perMinute float64, burst int) docstore.Limiter { // the demo is clicked through quickly
			return docstore.NewRateLimiter(perMinute*20, burst*20)
		})
	mux := http.NewServeMux()
	mux.Handle("/auth/passkey/", svc.Handler())
	mux.Handle("/auth/", mgr.Routes())
	mux.Handle("/directory/", docs.Handler())
	s := &Server{Auth: mgr, Mux: mux, Dir: dir, Store: store}
	// Demo only: the directory lookup is closed to passkey sessions, so the page asks for a session
	// of a (pretend) identity provider that verified its address. A real host signs in through
	// /auth/{provider}/login instead; never mount a route like this in a deployment.
	mux.HandleFunc("/demo/provider-session", func(w http.ResponseWriter, r *http.Request) {
		tok, err := s.Login("ana@example.com")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(tok))
	})
	// Demo only: the directory's log public keys, which the page pins when it loads. A real client
	// pins them out of band (shipped with the app, or from a source other than the server it
	// audits); fetching them from the server being watched would prove only that it is consistent.
	mux.HandleFunc("/demo/log-keys", func(w http.ResponseWriter, r *http.Request) {
		keys := map[string]string{}
		for kid, k := range signer.(encrypt.PublicKeyer).PublicKeys() {
			keys[kid] = base64.StdEncoding.EncodeToString(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(keys)
	})
	return s, nil
}

// Login starts a session as a user of an OpenID Connect provider that verified the address (what
// "Sign in with Google" gives): demo and tests only, there is no route that hands one out.
func (s *Server) Login(email string) (token string, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.Auth.StartSession(auth.User{ID: "demo:" + email, Email: email, VerifiedEmail: true})
}
