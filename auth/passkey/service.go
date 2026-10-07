package passkey

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	// Challenges keeps started ceremonies; nil means NewMemoryChallenges (one process). Inject a
	// shared store when several replicas serve the routes (see ChallengeStore).
	Challenges ChallengeStore
	// Limiter limits the routes per client; nil means a per-address token bucket in this
	// process (NewRateLimiter, 20 at once, 30 a minute). Inject your own (a shared budget, a
	// gateway's decision) or Unlimited{} to turn it off (see Limiter).
	Limiter Limiter
	// ClientIP names the client of a request for the Limiter; nil means its remote address.
	// Behind a reverse proxy use TrustedProxies("10.0.0.0/8") so X-Forwarded-For is believed
	// only from your proxy.
	ClientIP func(*http.Request) string
}

// Errors a caller may want to tell apart.
var (
	ErrNeedEmail    = errors.New("passkey: an e-mail address is needed to make an account")
	ErrBadChallenge = errors.New("passkey: the sign-in expired or was already used; start again")
	ErrLastPasskey  = errors.New("passkey: this is the only way to sign in to the account; add another passkey or sign-in method first")
	// ErrCloneDetected is returned by LoginFinish when the credential's signature counter did
	// not go up: the authenticator may have been copied. The sign-in is refused and the
	// credential stays flagged (remove it and register again).
	ErrCloneDetected = errors.New("passkey: this passkey may have been cloned; sign-in refused")
	// ErrSessionMismatch is returned by RegistrationFinish when the ceremony was begun by
	// another session (or by none) than the one finishing it.
	ErrSessionMismatch = errors.New("passkey: this registration was started by another session")
	// ErrRateLimited is returned by the HTTP routes (429, with Retry-After) when a client is over
	// the Limiter's budget.
	ErrRateLimited = errors.New("passkey: too many requests; wait a moment and try again")
	// errStore wraps a failure of the challenge store (HTTP 500).
	errStore = errors.New("passkey: the challenge store failed")
	// ErrBusy is returned when too many ceremonies are waiting to be finished.
	ErrBusy = errors.New("passkey: too many sign-ins in progress; try again shortly")
)

// Service runs the passkey ceremonies for a Store and gives the people who pass them a session of
// the auth.Manager.
type Service struct {
	wa       *webauthn.WebAuthn
	store    Store
	sessions *auth.Manager
	ttl      time.Duration
	uv       protocol.UserVerificationRequirement

	challenges ChallengeStore
	limiter    Limiter
	clientIP   func(*http.Request) string

	// acctMu serializes read-modify-write of an account's credentials within this process when
	// the Store has no Updater (a sign-in raising a counter against a removal).
	acctMu sync.Mutex
	// HasOtherWay, if set, says whether an account has another way to sign in than its passkeys
	// (an OpenID Connect identity of the same user ID), which lets its last passkey be removed.
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
	ip := cfg.ClientIP
	if ip == nil {
		ip = ClientIP
	}
	return &Service{wa: wa, store: store, sessions: sessions, ttl: ttl, uv: uv, challenges: ch, limiter: lim, clientIP: ip}, nil
}

func (s *Service) put(ctx context.Context, p pending) (string, error) {
	p.Expires = time.Now().Add(s.ttl)
	tok, err := s.challenges.Put(ctx, p)
	if err != nil && !errors.Is(err, ErrBusy) && ctx.Err() == nil {
		// A store failure is the server's, not a bad request (ErrBusy keeps its own 503).
		return "", fmt.Errorf("%w: %w", errStore, err)
	}
	return tok, err
}

// take returns and removes a started ceremony: a challenge is good for one finish.
func (s *Service) take(ctx context.Context, token string) (pending, bool) {
	p, ok, err := s.challenges.Take(ctx, token)
	if err != nil || !ok || time.Now().After(p.Expires) {
		return pending{}, false
	}
	return p, true
}

// update changes one account atomically: through the store's Updater when it has one (a
// conditional write, safe across processes), otherwise under this process's lock.
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

// create makes an account if there is none with its ID: through the store's Creator when it has
// one, otherwise under this process's lock.
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

// RegistrationStart begins making a passkey. For a person signed in as accountID (an OpenID
// Connect user, or an earlier passkey account) it adds a passkey to that identity: this is how
// a passkey is linked to a login the person already has. With no accountID it makes a new
// account for email (and name), which is only created when the ceremony finishes. It returns
// the options for navigator.credentials.create and the token to finish with. The e-mail of a
// new account is a stored label only (it names the passkey in the browser); it is never the
// session's identity, because nothing here has verified it.
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
		// Ask for PRF support while the passkey is made: some authenticators only offer it to
		// credentials created with it, and it lets a passkey later be an encryption key.
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

// RegistrationFinish checks the authenticator's answer (the JSON the browser's
// PublicKeyCredential serialises to) and saves the new passkey under label. signedIn is the
// user ID of the session that is finishing it ("" for none) and must be the one that began it
// (ErrSessionMismatch otherwise): a token started while signed in links a passkey to that
// identity and cannot be finished by anyone else, and a token started signed out cannot be
// finished into someone's session. It returns the account, and whether it was just made
// by this call for a person with no session, in which case the caller starts one with
// Session; a passkey added to a signed-in identity gives no new session.
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
				return nil, nil, false, ErrBadChallenge // made by someone else since: the challenge is stale
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

// LoginStart begins a sign-in with a discoverable passkey: no user name is typed, the
// authenticator offers the person's passkeys for this site. It returns the options for
// navigator.credentials.get and the token to finish with.
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

// LoginFinish checks the signed challenge, and on success raises the credential's signature
// counter and returns the account that owns it.
func (s *Service) LoginFinish(ctx context.Context, token string, body []byte) (*Account, error) {
	p, ok := s.take(ctx, token)
	if !ok || p.Account != "" {
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
	}, p.Session, req)
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
	// Raise the counter on the stored account as it is now (a removal or another sign-in may
	// have changed it since it was read for the check).
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

// Session starts the session of an account, the same bearer token an OpenID Connect login gives.
// The session carries the account's ID and name but no e-mail address: the address an account
// was made with was typed by whoever made it and nothing has verified it, and the rest of the
// library treats User.Email as verified (an access list that names an address would match it).
// An account linked to an OpenID Connect identity signs in as that identity's own ID, so it
// is that provider's verified address that counts, in that session. Marking a passkey
// account's address as verified (by a link mailed to it) is future work.
func (s *Service) Session(a *Account) (string, error) {
	return s.sessions.StartSession(sessionUser(a))
}

func sessionUser(a *Account) auth.User { return auth.User{ID: a.ID, Name: a.Name} }

// OtherWayFromProviders is a HasOtherWay for the OpenID Connect providers (auth.Manager.Providers):
// an account has another way in when its ID is "<provider>:<subject>" for a configured provider,
// matching the provider's name exactly. The name "passkey" is this package's own and is never a
// provider.
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

// accountPrefix is the first part of the ID of an account a passkey made; no provider may be
// called that.
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

// Remove takes a passkey out of an account. The last one stays unless the account has another
// way to sign in (HasOtherWay): losing a passkey should cost a login, not the account.
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
	mux.HandleFunc("/auth/passkey/register/begin", s.limited("register", s.handleRegisterBegin))
	mux.HandleFunc("/auth/passkey/register/finish", s.limited("register", s.handleRegisterFinish))
	mux.HandleFunc("/auth/passkey/login/begin", s.limited("login", s.handleLoginBegin))
	mux.HandleFunc("/auth/passkey/login/finish", s.limited("login", s.handleLoginFinish))
	mux.HandleFunc("/auth/passkey/credentials", s.handleCredentials)
	mux.HandleFunc("/auth/passkey/credentials/", s.handleCredential)
	return mux
}

// limited asks the Limiter before running h, per route group and client; over budget it answers
// 429 with Retry-After.
func (s *Service) limited(group string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, wait := s.limiter.Allow(r.Context(), group+"|"+s.clientIP(r)); !ok {
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
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrBadChallenge):
		status = http.StatusGone
	case errors.Is(err, ErrLastPasskey):
		status = http.StatusConflict
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
	jsonOut(w, status, map[string]string{"error": msg})
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
		// Signed in: the passkey is linked to this identity, and what the body says about who
		// it is does not count.
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
	if newSession { // a new account for someone with no session: it is signed in now
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
