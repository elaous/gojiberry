// Package demoserver is the server side of the people-keys demo: passkey sign-in, and the
// docstore's verified e-mail key directory (POST /directory/publish, GET /directory/lookup),
// over an in-memory store. It is shared by cmd/server and the e2e test.
package demoserver

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/auth/passkey"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// Server holds what the demo's routes share, so a test can act as an identity provider's
// signed-in user (Login) without a real provider.
type Server struct {
	Auth *auth.Manager
	Mux  *http.ServeMux
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
	docs := docstore.NewServer(store, "memory").WithAuth(mgr).WithDirectory(encrypt.DirectoryService(dir))
	mux := http.NewServeMux()
	mux.Handle("/auth/passkey/", svc.Handler())
	mux.Handle("/auth/", mgr.Routes())
	mux.Handle("/directory/", docs.Handler())
	s := &Server{Auth: mgr, Mux: mux}
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
	return s, nil
}

// Login starts a session as a user of an OpenID Connect provider that verified the address (what
// "Sign in with Google" gives): demo and tests only, there is no route that hands one out.
func (s *Server) Login(email string) (token string, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.Auth.StartSession(auth.User{ID: "demo:" + email, Email: email, VerifiedEmail: true})
}
