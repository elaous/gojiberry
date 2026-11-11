package passkey

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/internal/clientkey"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/elaous/gojiberry/auth"
)

// Config configures a Service.
type Config struct {
	RPID string
	// RPName is what the browser shows ("opendocs").
	RPName string
	// Origins are the origins the ceremonies may come from ("https://opendocs.io").
	Origins          []string
	ChallengeTTL     time.Duration
	UserVerification protocol.UserVerificationRequirement
	// Challenges keeps started ceremonies; nil means NewMemoryChallenges (one process).
	Challenges     ChallengeStore
	Limiter        Limiter
	AccountLimiter Limiter
	ClientIP       func(*http.Request) string
}

// Errors a caller may want to tell apart.
var (
	ErrNeedEmail       = errors.New("passkey: an e-mail address is needed to make an account")
	ErrBadChallenge    = errors.New("passkey: the sign-in expired or was already used; start again")
	ErrAlreadyCreated  = errors.New("passkey: the first passkey for this account was already made, in another tab or on another device; reload and add another passkey from your account if you want one (the passkey this device just made is not used and can be deleted)")
	ErrLastPasskey     = errors.New("passkey: this is the only way to sign in to the account; add another passkey or sign-in method first")
	ErrCloneDetected   = errors.New("passkey: this passkey may have been cloned; sign-in refused")
	ErrSessionMismatch = errors.New("passkey: this registration was started by another session")
	ErrRateLimited     = errors.New("passkey: too many requests; wait a moment and try again")
	errStore           = errors.New("passkey: the challenge store failed")
	ErrBusy            = errors.New("passkey: too many sign-ins in progress; try again shortly")
)

type accountLimited struct{ wait time.Duration }

func (e *accountLimited) Error() string { return ErrRateLimited.Error() }
func (e *accountLimited) Unwrap() error { return ErrRateLimited }

type Service struct {
	wa       *webauthn.WebAuthn
	store    Store
	sessions *auth.Manager
	ttl      time.Duration
	uv       protocol.UserVerificationRequirement

	challenges ChallengeStore
	limiter    Limiter
	acctLim    Limiter
	clientIP   func(*http.Request) string

	acctMu      sync.Mutex
	HasOtherWay func(ctx context.Context, accountID string) bool
}

type pending = Challenge

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
	ch := cfg.Challenges
	if ch == nil {
		ch = NewMemoryChallenges(0)
	}
	lim := cfg.Limiter
	if lim == nil {
		lim = NewRateLimiter(defaultPerMinute, defaultBurst)
	}
	alim := cfg.AccountLimiter
	if alim == nil {
		alim = NewRateLimiter(DefaultAccountPerMinute, DefaultAccountBurst)
	}
	ip := cfg.ClientIP
	if ip == nil {
		ip = ClientIP
	}
	return &Service{wa: wa, store: store, sessions: sessions, ttl: ttl, uv: uv, challenges: ch, limiter: lim, acctLim: alim, clientIP: ip}, nil
}

func (s *Service) put(ctx context.Context, p pending) (string, error) {
	p.Expires = time.Now().Add(s.ttl)
	tok, err := s.challenges.Put(ctx, p)
	if err != nil && !errors.Is(err, ErrBusy) && ctx.Err() == nil {
		return "", fmt.Errorf("%w: %w", errStore, err)
	}
	return tok, err
}

func (s *Service) take(ctx context.Context, token string) (pending, bool) {
	p, ok, err := s.challenges.Take(ctx, token)
	if err != nil || !ok || time.Now().After(p.Expires) {
		return pending{}, false
	}
	return p, true
}

func (s *Service) update(ctx context.Context, id string, fn func(*Account) error) error {
	if u, ok := s.store.(Updater); ok {
		return u.Update(ctx, id, fn)
	}
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	a, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := fn(a); err != nil {
		return err
	}
	return s.store.Put(ctx, a)
}

func (s *Service) create(ctx context.Context, a *Account) error {
	if c, ok := s.store.(Creator); ok {
		return c.Create(ctx, a)
	}
	s.acctMu.Lock()
	defer s.acctMu.Unlock()
	if _, err := s.store.Get(ctx, a.ID); err == nil {
		return ErrExists
	}
	return s.store.Put(ctx, a)
}

// RegistrationStart begins making a passkey.
func (s *Service) RegistrationStart(ctx context.Context, accountID, email, name string) (*protocol.CredentialCreation, string, error) {
	var acct *Account
	create := false
	if accountID != "" {
		a, err := s.store.Get(ctx, accountID)
		switch {
		case errors.Is(err, ErrNotFound):
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
		id := accountPrefix + ":" + base64.RawURLEncoding.EncodeToString(h[:12])
		acct = &Account{ID: id, Handle: h, Email: email, Name: name, Created: time.Now()}
		create = true
	}
	creation, session, err := s.wa.BeginRegistration(acct,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, RequireResidentKey: protocol.ResidentKeyRequired(), UserVerification: s.uv,
		}),
		webauthn.WithExclusions(webauthn.Credentials(acct.WebAuthnCredentials()).CredentialDescriptors()),
		webauthn.WithExtensions(webauthn.WithExtensionPRFSupport()),
	)
	if err != nil {
		return nil, "", fmt.Errorf("passkey: %w", err)
	}
	token, err := s.put(ctx, pending{Session: *session, Account: acct.ID, BegunBy: accountID, Email: acct.Email, Name: acct.Name, Create: create})
	if err != nil {
		return nil, "", err
	}
	return creation, token, nil
}

func (s *Service) RegistrationFinish(ctx context.Context, signedIn, token, label string, body []byte) (acct *Account, cred *Credential, newSession bool, err error) {
	p, ok := s.take(ctx, token)
	if !ok || p.Account == "" {
		return nil, nil, false, ErrBadChallenge
	}
	if p.BegunBy != signedIn {
		return nil, nil, false, ErrSessionMismatch
	}
	if p.Create {
		acct = &Account{ID: p.Account, Handle: p.Session.UserID, Email: p.Email, Name: p.Name, Created: time.Now()}
	} else {
		a, err := s.store.Get(ctx, p.Account)
		if err != nil {
			return nil, nil, false, err
		}
		acct = a
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
	nc, err := s.wa.FinishRegistration(acct, p.Session, req)
	if err != nil {
		return nil, nil, false, fmt.Errorf("passkey: %w", err)
	}
	add := func(a *Account) Credential {
		l := label
		if l == "" {
			l = fmt.Sprintf("Passkey %d", len(a.Credentials)+1)
		}
		c := Credential{Cred: *nc, Label: l, Created: time.Now()}
		a.Credentials = append(a.Credentials, c)
		return c
	}
	var c Credential
	if p.Create {
		c = add(acct)
		if err := s.create(ctx, acct); err != nil {
			if errors.Is(err, ErrExists) {
				return nil, nil, false, ErrAlreadyCreated
			}
			return nil, nil, false, err
		}
	} else {
		err := s.update(ctx, p.Account, func(a *Account) error {
			a.Credentials = slices.DeleteFunc(a.Credentials, func(x Credential) bool { return bytes.Equal(x.Cred.ID, nc.ID) })
			c = add(a)
			acct = a
			return nil
		})
		if err != nil {
			return nil, nil, false, err
		}
	}
	return acct, &c, p.Create && p.BegunBy == "", nil
}

func (s *Service) LoginStart(ctx context.Context) (*protocol.CredentialAssertion, string, error) {
	assertion, session, err := s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(s.uv))
	if err != nil {
		return nil, "", fmt.Errorf("passkey: %w", err)
	}
	token, err := s.put(ctx, pending{Session: *session})
	if err != nil {
		return nil, "", err
	}
	return assertion, token, nil
}

func (s *Service) LoginFinish(ctx context.Context, token string, body []byte) (*Account, error) {
	p, ok := s.take(ctx, token)
	if !ok || p.Account != "" {
		return nil, ErrBadChallenge
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
	var found *Account
	var limited *accountLimited
	user, cred, err := s.wa.FinishPasskeyLogin(func(_, handle []byte) (webauthn.User, error) {
		a, err := s.store.ByHandle(ctx, handle)
		if err != nil {
			return nil, err
		}
		found = a
		if ok, wait := s.acctLim.Allow(ctx, "account|"+a.ID); !ok {
			limited = &accountLimited{wait: wait}
			return nil, ErrRateLimited
		}
		return a, nil
	}, p.Session, req)
	if limited != nil {
		return nil, limited
	}
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
	var fresh *Account
	err = s.update(ctx, acct.ID, func(a *Account) error {
		hit := false
		for i := range a.Credentials {
			if bytes.Equal(a.Credentials[i].Cred.ID, cred.ID) {
				a.Credentials[i].Cred = *cred // the new signature counter and flags
				a.Credentials[i].LastUsed = time.Now()
				hit = true
			}
		}
		if !hit {
			return ErrNotFound // removed while this sign-in was being checked
		}
		fresh = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	if cred.Authenticator.CloneWarning {
		log.Printf("passkey: clone warning for account %s, credential %s: the signature counter did not go up; sign-in refused", fresh.ID, base64.RawURLEncoding.EncodeToString(cred.ID))
		return nil, ErrCloneDetected
	}
	return fresh, nil
}

func (s *Service) Session(a *Account) (string, error) {
	return s.sessions.StartSession(sessionUser(a))
}

func sessionUser(a *Account) auth.User { return auth.User{ID: a.ID, Name: a.Name} }

func OtherWayFromProviders(providers []auth.ProviderInfo) func(ctx context.Context, accountID string) bool {
	return func(_ context.Context, id string) bool {
		name, _, ok := strings.Cut(id, ":")
		if !ok || name == "" || name == accountPrefix {
			return false
		}
		for _, p := range providers {
			if p.Name == name {
				return true
			}
		}
		return false
	}
}

const accountPrefix = "passkey"

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

// Remove takes a passkey out of an account.
func (s *Service) Remove(ctx context.Context, accountID, credentialID string) error {
	return s.update(ctx, accountID, func(a *Account) error {
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
		return nil
	})
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/passkey/register/begin", s.limited("register", s.handleRegisterBegin))
	mux.HandleFunc("/auth/passkey/register/finish", s.limited("register", s.handleRegisterFinish))
	mux.HandleFunc("/auth/passkey/login/begin", s.limited("login", s.handleLoginBegin))
	mux.HandleFunc("/auth/passkey/login/finish", s.limited("login", s.handleLoginFinish))
	mux.HandleFunc("/auth/passkey/credentials", s.handleCredentials)
	mux.HandleFunc("/auth/passkey/credentials/", s.handleCredential)
	return mux
}

func (s *Service) limited(group string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := s.limiter.Allow(r.Context(), group+"|"+clientkey.Of(s.clientIP(r))); !ok {
			secs := int(wait/time.Second) + 1
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			jsonOut(w, http.StatusTooManyRequests, map[string]string{"error": ErrRateLimited.Error()})
			return
		}
		h(w, r)
	}
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := ""
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrBadChallenge):
		status = http.StatusGone
	case errors.Is(err, ErrLastPasskey):
		status = http.StatusConflict
	case errors.Is(err, ErrAlreadyCreated):
		status = http.StatusConflict
		code = "account_exists"
	case errors.Is(err, ErrSessionMismatch):
		status = http.StatusForbidden
	case errors.Is(err, ErrBusy):
		status = http.StatusServiceUnavailable
	case errors.Is(err, errStore):
		status = http.StatusInternalServerError
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		log.Printf("passkey: %v", err)
		msg = "passkey: internal error; try again shortly"
	}
	out := map[string]string{"error": msg}
	if code != "" {
		out["code"] = code // for a client that tells this 409 from ErrLastPasskey's
	}
	jsonOut(w, status, out)
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
		in.Email, in.Name = u.Email, u.Name
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
	signedIn := ""
	if u, ok := s.sessions.CurrentUser(r); ok {
		signedIn = u.ID
	}
	acct, cred, newSession, err := s.RegistrationFinish(r.Context(), signedIn, r.URL.Query().Get("token"), r.URL.Query().Get("label"), body)
	if err != nil {
		fail(w, err)
		return
	}
	out := map[string]any{"id": cred.ID(), "label": cred.Label}
	if newSession {
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
		var al *accountLimited
		if errors.As(err, &al) {
			w.Header().Set("Retry-After", strconv.Itoa(int(al.wait/time.Second)+1))
			jsonOut(w, http.StatusTooManyRequests, map[string]string{"error": ErrRateLimited.Error()})
			return
		}
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
	jsonOut(w, http.StatusOK, map[string]any{"auth_token": tok, "user": sessionUser(acct)})
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
