package passkey

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/elaous/gojiberry/auth"
)

// Config configures a Service. The relying party ID and the origins are configuration, never
// guessed from the request: a passkey is bound to them.
type Config struct {
	// RPID is the relying party ID: the site's domain without scheme or port ("opendocs.io").
	RPID string
	// RPName is what the browser shows ("opendocs").
	RPName string
	// Origins are the origins the ceremonies may come from ("https://opendocs.io"). Keep it to
	// the page's own origin; "http://localhost:8080" is fine for development.
	Origins []string
	// ChallengeTTL is how long a started ceremony may take; 5 minutes if zero.
	ChallengeTTL time.Duration
	// UserVerification is "required" (the default: the authenticator checks the person with a
	// biometric or PIN, which makes a passkey two factors by itself), "preferred" or
	// "discouraged".
	UserVerification protocol.UserVerificationRequirement
}

// Errors a caller may want to tell apart.
var (
	ErrNeedEmail    = errors.New("passkey: an e-mail address is needed to make an account")
	ErrBadChallenge = errors.New("passkey: the sign-in expired or was already used; start again")
	ErrLastPasskey  = errors.New("passkey: this is the only way to sign in to the account; add another passkey or sign-in method first")
)

// Service runs the passkey ceremonies for a Store and gives the people who pass them a session of
// the auth.Manager.
type Service struct {
	wa       *webauthn.WebAuthn
	store    Store
	sessions *auth.Manager
	ttl      time.Duration
	uv       protocol.UserVerificationRequirement

	mu         sync.Mutex
	challenges map[string]pending
	// HasOtherWay, if set, says whether an account has another way to sign in than its passkeys
	// (an OpenID Connect identity of the same user ID), which lets its last passkey be removed.
	HasOtherWay func(ctx context.Context, accountID string) bool
}

type pending struct {
	session webauthn.SessionData
	account string // the account a registration is for, "" for a sign-in
	email   string // for a new account
	name    string
	create  bool // the registration makes the account
	expires time.Time
}

// New makes a Service.
func New(cfg Config, store Store, sessions *auth.Manager) (*Service, error) {
	if store == nil || sessions == nil {
		return nil, errors.New("passkey: a Store and an auth.Manager are required")
	}
	if cfg.RPID == "" || len(cfg.Origins) == 0 {
		return nil, errors.New("passkey: Config.RPID and Config.Origins are required (a passkey is bound to them; they are never guessed from a request)")
	}
	uv := cfg.UserVerification
	if uv == "" {
		uv = protocol.VerificationRequired
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID: cfg.RPID, RPDisplayName: cfg.RPName, RPOrigins: cfg.Origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired, // discoverable: no user name to type
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   uv,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	ttl := cfg.ChallengeTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &Service{wa: wa, store: store, sessions: sessions, ttl: ttl, uv: uv, challenges: map[string]pending{}}, nil
}

func (s *Service) put(p pending) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	p.expires = time.Now().Add(s.ttl)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.challenges { // drop what has expired
		if now.After(v.expires) {
			delete(s.challenges, k)
		}
	}
	s.challenges[token] = p
	return token, nil
}

// take returns and removes a started ceremony: a challenge is good for one finish.
func (s *Service) take(token string) (pending, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.challenges[token]
	delete(s.challenges, token)
	if !ok || time.Now().After(p.expires) {
		return pending{}, false
	}
	return p, true
}

// RegistrationStart begins making a passkey. For a person signed in as accountID (an OpenID
// Connect user, or an earlier passkey account) it adds a passkey to that identity: this is how
// a passkey is linked to a login the person already has. With no accountID it makes a new
// account for email (and name), which is only created when the ceremony finishes. It returns
// the options for navigator.credentials.create and the token to finish with.
func (s *Service) RegistrationStart(ctx context.Context, accountID, email, name string) (*protocol.CredentialCreation, string, error) {
	var acct *Account
	create := false
	if accountID != "" {
		a, err := s.store.Get(ctx, accountID)
		switch {
		case errors.Is(err, ErrNotFound):
			// A signed-in OpenID Connect user's first passkey: the account is made for their ID.
			h, herr := newHandle()
			if herr != nil {
				return nil, "", herr
			}
			a = &Account{ID: accountID, Handle: h, Email: email, Name: name, Created: time.Now()}
			create = true
		case err != nil:
			return nil, "", err
		}
		acct = a
	} else {
		email = strings.TrimSpace(email)
		if email == "" {
			return nil, "", ErrNeedEmail
		}
		h, err := newHandle()
		if err != nil {
			return nil, "", err
		}
		id := "passkey:" + base64.RawURLEncoding.EncodeToString(h[:12])
		acct = &Account{ID: id, Handle: h, Email: email, Name: name, Created: time.Now()}
		create = true
	}
	creation, session, err := s.wa.BeginRegistration(acct,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(), UserVerification: s.uv,
		}),
		webauthn.WithExclusions(webauthn.Credentials(acct.WebAuthnCredentials()).CredentialDescriptors()),
	)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: %w", err)
	}
	token, err := s.put(pending{session: *session, account: acct.ID, email: acct.Email, name: acct.Name, create: create})
	if err != nil {
		return nil, "", err
	}
	return creation, token, nil
}

// RegistrationFinish checks the authenticator's answer (the JSON the browser's
// PublicKeyCredential serialises to) and saves the new passkey under label. It returns the
// account; for a new one the caller may start a session with Session.
func (s *Service) RegistrationFinish(ctx context.Context, token, label string, body []byte) (*Account, *Credential, error) {
	p, ok := s.take(token)
	if !ok || p.account == "" {
		return nil, nil, ErrBadChallenge
	}
	var acct *Account
	if p.create {
		h := p.session.UserID
		acct = &Account{ID: p.account, Handle: h, Email: p.email, Name: p.name, Created: time.Now()}
		if _, err := s.store.Get(ctx, p.account); err == nil {
			return nil, nil, ErrBadChallenge // made by someone else since: the challenge is stale
		}
	} else {
		a, err := s.store.Get(ctx, p.account)
		if err != nil {
			return nil, nil, err
		}
		acct = a
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
	cred, err := s.wa.FinishRegistration(acct, p.session, req)
	if err != nil {
		return nil, nil, fmt.Errorf("passkey: %w", err)
	}
	if label == "" {
		label = fmt.Sprintf("Passkey %d", len(acct.Credentials)+1)
	}
	c := Credential{Cred: *cred, Label: label, Created: time.Now()}
	acct.Credentials = append(acct.Credentials, c)
	if err := s.store.Put(ctx, acct); err != nil {
		return nil, nil, err
	}
	return acct, &c, nil
}

// LoginStart begins a sign-in with a discoverable passkey: no user name is typed, the
// authenticator offers the person's passkeys for this site. It returns the options for
// navigator.credentials.get and the token to finish with.
func (s *Service) LoginStart(ctx context.Context) (*protocol.CredentialAssertion, string, error) {
	assertion, session, err := s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(s.uv))
	if err != nil {
		return nil, "", fmt.Errorf("passkey: %w", err)
	}
	token, err := s.put(pending{session: *session})
	if err != nil {
		return nil, "", err
	}
	return assertion, token, nil
}

// LoginFinish checks the signed challenge, and on success raises the credential's signature
// counter and returns the account that owns it.
func (s *Service) LoginFinish(ctx context.Context, token string, body []byte) (*Account, error) {
	p, ok := s.take(token)
	if !ok || p.account != "" {
		return nil, ErrBadChallenge
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
	var found *Account
	user, cred, err := s.wa.FinishPasskeyLogin(func(_, handle []byte) (webauthn.User, error) {
		a, err := s.store.ByHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		found = a
		return a, nil
	}, p.session, req)
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	acct, _ := user.(*Account)
	if acct == nil {
		acct = found
	}
	if acct == nil {
		return nil, ErrNotFound
	}
	for i := range acct.Credentials {
		if bytes.Equal(acct.Credentials[i].Cred.ID, cred.ID) {
			acct.Credentials[i].Cred = *cred // the new signature counter and flags
			acct.Credentials[i].LastUsed = time.Now()
		}
	}
	if err := s.store.Put(ctx, acct); err != nil {
		return nil, err
	}
	return acct, nil
}

// Session starts the session of an account, the same bearer token an OpenID Connect login gives.
func (s *Service) Session(a *Account) (string, error) {
	return s.sessions.StartSession(auth.User{ID: a.ID, Email: a.Email, Name: a.Name})
}

// Credentials lists an account's passkeys.
func (s *Service) Credentials(ctx context.Context, accountID string) ([]Credential, error) {
	a, err := s.store.Get(ctx, accountID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a.Credentials, nil
}

// Remove takes a passkey out of an account. The last one stays unless the account has another
// way to sign in (HasOtherWay): losing a passkey should cost a login, not the account.
func (s *Service) Remove(ctx context.Context, accountID, credentialID string) error {
	a, err := s.store.Get(ctx, accountID)
	if err != nil {
		return err
	}
	idx := -1
	for i, c := range a.Credentials {
		if c.ID() == credentialID {
			idx = i
		}
	}
	if idx < 0 {
		return ErrNotFound
	}
	if len(a.Credentials) == 1 && (s.HasOtherWay == nil || !s.HasOtherWay(ctx, accountID)) {
		return ErrLastPasskey
	}
	a.Credentials = append(a.Credentials[:idx], a.Credentials[idx+1:]...)
	return s.store.Put(ctx, a)
}

// Handler is the passkey routes:
//
//	POST   /auth/passkey/register/begin    {email, name}  -> {options, token}   (signed in: adds a passkey to the account)
//	POST   /auth/passkey/register/finish   ?token=&label=  body: the credential -> {id, auth_token?}
//	POST   /auth/passkey/login/begin                       -> {options, token}
//	POST   /auth/passkey/login/finish      ?token=         body: the assertion -> {auth_token, user}
//	GET    /auth/passkey/credentials       (signed in)     -> the passkeys of the account
//	DELETE /auth/passkey/credentials/{id}  (signed in)
//
// Mount it beside auth.Manager.Routes() under a server that answers CORS and allows the
// Authorization header. Every route answers JSON.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/passkey/register/begin", s.handleRegisterBegin)
	mux.HandleFunc("/auth/passkey/register/finish", s.handleRegisterFinish)
	mux.HandleFunc("/auth/passkey/login/begin", s.handleLoginBegin)
	mux.HandleFunc("/auth/passkey/login/finish", s.handleLoginFinish)
	mux.HandleFunc("/auth/passkey/credentials", s.handleCredentials)
	mux.HandleFunc("/auth/passkey/credentials/", s.handleCredential)
	return mux
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrBadChallenge):
		status = http.StatusGone
	case errors.Is(err, ErrLastPasskey):
		status = http.StatusConflict
	}
	jsonOut(w, status, map[string]string{"error": err.Error()})
}

func (s *Service) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var in struct{ Email, Name string }
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in)
	accountID := ""
	if u, ok := s.sessions.CurrentUser(r); ok {
		accountID = u.ID
		if in.Email == "" {
			in.Email, in.Name = u.Email, u.Name
		}
	}
	opts, token, err := s.RegistrationStart(r.Context(), accountID, in.Email, in.Name)
	if err != nil {
		fail(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"options": opts, "token": token})
}

func (s *Service) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		fail(w, err)
		return
	}
	_, signedIn := s.sessions.CurrentUser(r)
	acct, cred, err := s.RegistrationFinish(r.Context(), r.URL.Query().Get("token"), r.URL.Query().Get("label"), body)
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{"id": cred.ID(), "label": cred.Label}
	if !signedIn { // a new account: it is signed in now
		tok, err := s.Session(acct)
		if err != nil {
			fail(w, err)
			return
		}
		out["auth_token"] = tok
	}
	jsonOut(w, http.StatusOK, out)
}

func (s *Service) handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	opts, token, err := s.LoginStart(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"options": opts, "token": token})
}

func (s *Service) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		fail(w, err)
		return
	}
	acct, err := s.LoginFinish(r.Context(), r.URL.Query().Get("token"), body)
	if err != nil {
		// One answer for every failed sign-in: what went wrong is not for the caller.
		if errors.Is(err, ErrBadChallenge) {
			fail(w, err)
			return
		}
		jsonOut(w, http.StatusUnauthorized, map[string]string{"error": "passkey sign-in failed"})
		return
	}
	tok, err := s.Session(acct)
	if err != nil {
		fail(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]any{"auth_token": tok, "user": auth.User{ID: acct.ID, Email: acct.Email, Name: acct.Name}})
}

func (s *Service) handleCredentials(w http.ResponseWriter, r *http.Request) {
	u, ok := s.sessions.CurrentUser(r)
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cs, err := s.Credentials(r.Context(), u.ID)
	if err != nil {
		fail(w, err)
		return
	}
	type row struct {
		ID       string    `json:"id"`
		Label    string    `json:"label"`
		Created  time.Time `json:"created"`
		LastUsed time.Time `json:"lastUsed,omitempty"`
		Synced   bool      `json:"synced"`
	}
	out := make([]row, 0, len(cs))
	for _, c := range cs {
		out = append(out, row{ID: c.ID(), Label: c.Label, Created: c.Created, LastUsed: c.LastUsed, Synced: c.Cred.Flags.BackupState})
	}
	jsonOut(w, http.StatusOK, out)
}

func (s *Service) handleCredential(w http.ResponseWriter, r *http.Request) {
	u, ok := s.sessions.CurrentUser(r)
	if !ok {
		http.Error(w, "not signed in", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/auth/passkey/credentials/")
	if err := s.Remove(r.Context(), u.ID, id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, 64<<10)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
