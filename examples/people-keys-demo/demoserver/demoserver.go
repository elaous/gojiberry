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

type Server struct {
	Auth  *auth.Manager
	Mux   *http.ServeMux
	Dir   *encrypt.AdapterDirectory
	Store docstore.Adapter
}

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
	mux.HandleFunc("/demo/provider-session", func(w http.ResponseWriter, r *http.Request) {
		tok, err := s.Login("ana@example.com")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(tok))
	})
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

func (s *Server) Login(email string) (token string, err error) {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.Auth.StartSession(auth.User{ID: "demo:" + email, Email: email, VerifiedEmail: true})
}
